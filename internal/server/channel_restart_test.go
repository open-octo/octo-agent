package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/channel"
)

// crashingAdapterState is shared across every instance one test's
// constructor produces — each restart attempt builds a fresh
// *crashingFakeAdapter (matching production's rebuild-on-restart behavior),
// so per-instance state can't track "how many times has this platform
// failed overall" on its own.
type crashingAdapterState struct {
	mu          sync.Mutex
	failCount   int32   // Start fails this many times total, across all instances
	constructed int32   // instances built so far
	startedOn   []int32 // construction id Start was invoked on, in call order

	// gateConstruction/ctorGate let a test deterministically land a restart
	// loop's rebuild inside its ctor(pc) call — the one gap that doesn't
	// observe ctx cancellation — so a concurrent stop/reload can race it.
	// The construction with id == gateConstruction blocks until ctorGate is
	// closed; every other construction proceeds immediately.
	gateConstruction int32
	ctorGate         chan struct{}
}

// crashingFakeAdapter fails its first N Start calls (simulating a crash —
// returns a non-nil error before ctx is ever cancelled), then behaves like a
// normal adapter (blocks on ctx.Done()).
type crashingFakeAdapter struct {
	id    int32 // this instance's construction sequence number
	state *crashingAdapterState
}

func newCrashingAdapterCtorWithState(state *crashingAdapterState, failCount int32) func(channel.PlatformConfig) (channel.Adapter, error) {
	state.failCount = failCount
	return func(channel.PlatformConfig) (channel.Adapter, error) {
		// Guarded by state.mu (not atomic) so it's synchronized with the
		// mutex-protected reads/writes of the other fields below — mixing
		// atomic and mutex-guarded access to the same field isn't safe even
		// though each individual access looks correct in isolation.
		state.mu.Lock()
		state.constructed++
		id := state.constructed
		gate := state.ctorGate
		gateID := state.gateConstruction
		state.mu.Unlock()
		if gate != nil && id == gateID {
			<-gate
		}
		return &crashingFakeAdapter{id: id, state: state}, nil
	}
}

func (a *crashingFakeAdapter) Platform() string { return "crashfake" }

func (a *crashingFakeAdapter) Start(ctx context.Context, _ func(channel.InboundEvent)) error {
	a.state.mu.Lock()
	a.state.startedOn = append(a.state.startedOn, a.id)
	shouldFail := a.state.failCount > 0
	if shouldFail {
		a.state.failCount--
	}
	a.state.mu.Unlock()

	if shouldFail {
		return fmt.Errorf("simulated crash (instance %d)", a.id)
	}
	<-ctx.Done()
	return nil
}

func (a *crashingFakeAdapter) Stop() error { return nil }
func (a *crashingFakeAdapter) SendText(chatID, text, replyTo string) channel.SendResult {
	return channel.SendResult{OK: true}
}
func (a *crashingFakeAdapter) SendFile(chatID, path, name, replyTo string) channel.SendResult {
	return channel.SendResult{OK: true}
}
func (a *crashingFakeAdapter) UpdateMessage(chatID, messageID, text string) bool { return true }
func (a *crashingFakeAdapter) SupportsMessageUpdates() bool                      { return false }
func (a *crashingFakeAdapter) SendTyping(chatID, contextToken string) error      { return nil }
func (a *crashingFakeAdapter) StopTyping(chatID, contextToken string) error      { return nil }
func (a *crashingFakeAdapter) Flush(chatID string)                               {}
func (a *crashingFakeAdapter) SupportsButtons() bool                             { return false }
func (a *crashingFakeAdapter) SendButtons(chatID, text string, buttons []channel.Button, replyTo string) channel.SendResult {
	return channel.SendResult{OK: true}
}
func (a *crashingFakeAdapter) ValidateConfig(channel.PlatformConfig) []string { return nil }

func writeChannelsYML(t *testing.T, yml string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	cfgDir := filepath.Join(tmp, ".octo")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "channels.yml"), []byte(yml), 0600); err != nil {
		t.Fatal(err)
	}
}

// #1121: a crashing adapter used to go permanently, silently dead — `_ =
// a.Start(...)` discarded the error and nothing ever ran again. This pins
// that a transient crash is retried (with a fresh adapter instance each
// time, since Start isn't guaranteed reentrant) until it recovers.
func TestChannelRestart_RecoversFromTransientCrash(t *testing.T) {
	orig := channelRestartDelays
	channelRestartDelays = []time.Duration{time.Millisecond}
	t.Cleanup(func() { channelRestartDelays = orig })

	state := &crashingAdapterState{}
	channel.Register("crashfake-recovers", newCrashingAdapterCtorWithState(state, 2))

	writeChannelsYML(t, "channels:\n  crashfake-recovers:\n    enabled: true\n")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	// Wait for the restart-loop goroutine to actually return (not just for its
	// context to be cancelled) before the channelRestartDelays cleanup above
	// restores the package-level var out from under it — otherwise the
	// goroutine's still-live read of that slice races the restore.
	t.Cleanup(func() {
		srv.stopChannels()
		srv.channelWG.Wait()
	})

	srv.reloadChannel("crashfake-recovers")

	// Poll until a 3rd instance has actually been constructed (initial +
	// 2 crash-retries), not just until isAdapterRunning()+channelIssue=="" —
	// that pair is also (mis)true for an instant right after the very first
	// construction, before its Start() has even run once, since the adapter
	// is stored in runningAdapters synchronously before the goroutine that
	// calls Start gets scheduled.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state.mu.Lock()
		constructed := state.constructed
		state.mu.Unlock()
		if constructed >= 3 && srv.isAdapterRunning("crashfake-recovers") && srv.channelIssue("crashfake-recovers") == "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !srv.isAdapterRunning("crashfake-recovers") {
		t.Fatal("adapter should be running again after recovering from 2 simulated crashes")
	}
	if issue := srv.channelIssue("crashfake-recovers"); issue != "" {
		t.Errorf("channelIssue = %q, want cleared after recovery", issue)
	}

	state.mu.Lock()
	constructed := state.constructed
	startedOn := append([]int32(nil), state.startedOn...)
	state.mu.Unlock()
	if constructed < 3 {
		t.Errorf("constructed = %d, want at least 3 (initial + 2 retries)", constructed)
	}
	if len(startedOn) < 3 {
		t.Errorf("Start was called %d times, want at least 3", len(startedOn))
	}
	// Every Start call must have run on a distinct, freshly-constructed
	// instance — never the same crashed one twice.
	seen := map[int32]bool{}
	for _, id := range startedOn {
		if seen[id] {
			t.Errorf("instance %d had Start called on it more than once — should rebuild fresh each retry", id)
		}
		seen[id] = true
	}
}

// A crashed adapter's restart loop rebuilds via ctor(pc) — a call that does
// not observe ctx cancellation — before writing the fresh instance back to
// runningAdapters. If an operator stops/reloads the same platform while that
// rebuild is still in flight (entirely plausible: this fix is what makes the
// crash visible as an "Error" status in the Channels view in the first
// place, which is exactly what would prompt someone to intervene), the
// generation check added alongside runningAdapters.Store must detect it's
// been superseded and discard the stale instance instead of clobbering the
// reload's fresh one.
func TestChannelRestart_ConcurrentReloadDuringRebuildDiscardsStaleInstance(t *testing.T) {
	orig := channelRestartDelays
	channelRestartDelays = []time.Duration{time.Millisecond}
	t.Cleanup(func() { channelRestartDelays = orig })

	state := &crashingAdapterState{}
	// Instance #1's Start crashes once; instance #2 (the stale rebuild) is
	// gated so the test can hold it inside ctor(pc); instance #3 (the
	// reload's fresh start) must succeed and keep running.
	channel.Register("crashfake-superseded", newCrashingAdapterCtorWithState(state, 1))
	state.ctorGate = make(chan struct{})
	state.gateConstruction = 2

	writeChannelsYML(t, "channels:\n  crashfake-superseded:\n    enabled: true\n")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	t.Cleanup(func() {
		srv.stopChannels()
		srv.channelWG.Wait()
	})

	srv.reloadChannel("crashfake-superseded")

	// Wait until the restart loop's rebuild (construction #2) has started
	// and is blocked in ctor(pc) — state.constructed is bumped before the
	// gate wait, so seeing it reach 2 proves the goroutine is parked there.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state.mu.Lock()
		constructed := state.constructed
		state.mu.Unlock()
		if constructed >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	state.mu.Lock()
	constructed := state.constructed
	state.mu.Unlock()
	if constructed < 2 {
		t.Fatal("restart loop never reached the gated rebuild (construction #2)")
	}

	// Simulate an operator reacting to the crash-loop: reload the platform
	// while the stale rebuild is still parked in ctor(pc). This stops
	// generation 1 (cancelling its ctx, which the parked ctor call can't see)
	// and starts generation 2 with a fresh instance #3.
	srv.reloadChannel("crashfake-superseded")

	// Now release the stale rebuild. Without the generation check, it would
	// go on to overwrite runningAdapters with instance #2 (never Started).
	close(state.ctorGate)

	// Poll for the state to settle: exactly instance #3 owns runningAdapters,
	// and it never moves off that once settled.
	var stableSince time.Time
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		v, ok := srv.runningAdapters.Load("crashfake-superseded")
		fa, isFake := v.(*crashingFakeAdapter)
		if ok && isFake && fa.id == 3 {
			if stableSince.IsZero() {
				stableSince = time.Now()
			}
			if time.Since(stableSince) > 100*time.Millisecond {
				break // held steady long enough to be confident it's final
			}
		} else {
			stableSince = time.Time{}
		}
		time.Sleep(5 * time.Millisecond)
	}

	v, ok := srv.runningAdapters.Load("crashfake-superseded")
	if !ok {
		t.Fatal("expected an adapter running after reload, found none")
	}
	fa, isFake := v.(*crashingFakeAdapter)
	if !isFake {
		t.Fatalf("runningAdapters holds unexpected type %T", v)
	}
	if fa.id != 3 {
		t.Errorf("runningAdapters holds instance %d, want instance 3 (the reload's fresh start) — "+
			"the stale rebuild (instance 2) must have clobbered it", fa.id)
	}

	state.mu.Lock()
	startedOn := append([]int32(nil), state.startedOn...)
	state.mu.Unlock()
	for _, id := range startedOn {
		if id == 2 {
			t.Error("instance 2 (the discarded stale rebuild) had Start called on it — it should have been discarded unstarted")
		}
	}
}

// A platform that keeps crashing past maxChannelRestarts must be marked with
// a clear "gave up" reason and left stopped, not retried forever.
func TestChannelRestart_GivesUpAfterMaxRestarts(t *testing.T) {
	orig := channelRestartDelays
	channelRestartDelays = []time.Duration{time.Millisecond}
	t.Cleanup(func() { channelRestartDelays = orig })

	state := &crashingAdapterState{}
	channel.Register("crashfake-giveup", newCrashingAdapterCtorWithState(state, maxChannelRestarts+5))

	writeChannelsYML(t, "channels:\n  crashfake-giveup:\n    enabled: true\n")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	// Wait for the restart-loop goroutine to actually return (not just for its
	// context to be cancelled) before the channelRestartDelays cleanup above
	// restores the package-level var out from under it — otherwise the
	// goroutine's still-live read of that slice races the restore.
	t.Cleanup(func() {
		srv.stopChannels()
		srv.channelWG.Wait()
	})

	srv.reloadChannel("crashfake-giveup")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if issue := srv.channelIssue("crashfake-giveup"); issue != "" && !srv.isAdapterRunning("crashfake-giveup") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if srv.isAdapterRunning("crashfake-giveup") {
		t.Fatal("adapter should not be marked running after exceeding the restart limit")
	}
	issue := srv.channelIssue("crashfake-giveup")
	if issue == "" {
		t.Fatal("expected a recorded issue after giving up")
	}
	t.Logf("recorded issue: %s", issue)
}

// The Channels REST API surfaces the recorded issue (#1121's "somewhere
// queryable" requirement) instead of leaving the only diagnostic in server logs.
func TestChannelIssue_SurfacedViaListChannels(t *testing.T) {
	orig := channelRestartDelays
	channelRestartDelays = []time.Duration{time.Millisecond}
	t.Cleanup(func() { channelRestartDelays = orig })

	state := &crashingAdapterState{}
	channel.Register("crashfake-api", newCrashingAdapterCtorWithState(state, maxChannelRestarts+5))

	writeChannelsYML(t, "channels:\n  crashfake-api:\n    enabled: true\n")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	// Wait for the restart-loop goroutine to actually return (not just for its
	// context to be cancelled) before the channelRestartDelays cleanup above
	// restores the package-level var out from under it — otherwise the
	// goroutine's still-live read of that slice races the restore.
	t.Cleanup(func() {
		srv.stopChannels()
		srv.channelWG.Wait()
	})
	srv.reloadChannel("crashfake-api")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && srv.channelIssue("crashfake-api") == "" {
		time.Sleep(10 * time.Millisecond)
	}

	info := channelInfo{Platform: "crashfake-api"}
	info.Running = srv.isAdapterRunning("crashfake-api")
	info.Issue = srv.channelIssue("crashfake-api")
	if info.Issue == "" {
		t.Fatal("expected channelInfo.Issue to carry the recorded reason")
	}
}

// A config validation failure must be recorded immediately (no restart loop
// needed — it will never succeed until the config changes).
func TestChannelIssue_RecordedOnInvalidConfig(t *testing.T) {
	channel.Register("invalidcfg-fake", func(channel.PlatformConfig) (channel.Adapter, error) {
		return &validatingFakeAdapter{}, nil
	})

	writeChannelsYML(t, "channels:\n  invalidcfg-fake:\n    enabled: true\n")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	t.Cleanup(func() {
		srv.stopChannels()
		srv.channelWG.Wait()
	})
	srv.reloadChannel("invalidcfg-fake")

	if srv.isAdapterRunning("invalidcfg-fake") {
		t.Fatal("adapter with invalid config should not be running")
	}
	if issue := srv.channelIssue("invalidcfg-fake"); issue == "" {
		t.Fatal("expected an invalid-config issue to be recorded")
	}
}

// validatingFakeAdapter always fails ValidateConfig, for
// TestChannelIssue_RecordedOnInvalidConfig.
type validatingFakeAdapter struct{}

func (a *validatingFakeAdapter) Platform() string { return "invalidcfg-fake" }
func (a *validatingFakeAdapter) Start(ctx context.Context, _ func(channel.InboundEvent)) error {
	<-ctx.Done()
	return nil
}
func (a *validatingFakeAdapter) Stop() error { return nil }
func (a *validatingFakeAdapter) SendText(chatID, text, replyTo string) channel.SendResult {
	return channel.SendResult{OK: true}
}
func (a *validatingFakeAdapter) SendFile(chatID, path, name, replyTo string) channel.SendResult {
	return channel.SendResult{OK: true}
}
func (a *validatingFakeAdapter) UpdateMessage(chatID, messageID, text string) bool { return true }
func (a *validatingFakeAdapter) SupportsMessageUpdates() bool                      { return false }
func (a *validatingFakeAdapter) SendTyping(chatID, contextToken string) error      { return nil }
func (a *validatingFakeAdapter) StopTyping(chatID, contextToken string) error      { return nil }
func (a *validatingFakeAdapter) Flush(chatID string)                               {}
func (a *validatingFakeAdapter) SupportsButtons() bool                             { return false }
func (a *validatingFakeAdapter) SendButtons(chatID, text string, buttons []channel.Button, replyTo string) channel.SendResult {
	return channel.SendResult{OK: true}
}
func (a *validatingFakeAdapter) ValidateConfig(channel.PlatformConfig) []string {
	return []string{"missing required field: token"}
}
