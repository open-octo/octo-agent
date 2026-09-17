package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/datahome"
	"github.com/open-octo/octo-agent/internal/serveproc"
	"github.com/open-octo/octo-agent/internal/server"
)

// awaitPidEnv carries the outgoing process's pid to the one replacing it during
// a profile switch. The new process has to hold off until the old one is gone:
// it would otherwise register as a second instance and be told to go away, and
// it would open serve.log while the old backend still holds it.
const awaitPidEnv = "OCTO_DESKTOP_AWAIT_PID"

// awaitPredecessorTimeout bounds that wait. A hub that refuses to die is a
// worse problem than a profile switch. If the wait runs out we start anyway:
// application.New then hands us to the still-living instance as a second
// launch and we exit — the user sees the old profile's window pop up while
// the choice file already points at the new one. Confusing, but the next cold
// start lands on the new profile, which beats an app that never appears.
const awaitPredecessorTimeout = 20 * time.Second

// awaitPredecessor blocks until the process that asked us to replace it has
// exited. A no-op on a normal launch. The variable is cleared either way, so a
// later relaunch (an update restart, say) doesn't inherit a stale pid.
func awaitPredecessor() {
	raw := os.Getenv(awaitPidEnv)
	_ = os.Unsetenv(awaitPidEnv)
	pid, err := strconv.Atoi(raw)
	if err != nil || pid <= 0 {
		return
	}
	deadline := time.Now().Add(awaitPredecessorTimeout)
	for time.Now().Before(deadline) {
		if !serveproc.IsAlive(pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// switchProfile is the tray's profile picker acting. It records the choice and
// restarts the app into it, because one process serves one profile: the data
// root is process-global, which is what keeps every path in the codebase able
// to resolve it without being handed one.
//
// A turn in flight is lost by that restart, so it is worth a question first.
// The two cases read differently on purpose — octo working on something is a
// reason to hesitate, octo waiting on an answer is a reason to go back and give
// it — and neither is phrased as an error, because switching anyway is a
// perfectly reasonable thing to decide.
func (b *nativeBridge) switchProfile(profile string) {
	current := os.Getenv(datahome.ProfileEnv)
	if profile == current {
		return
	}
	if !b.confirmProfileSwitch(profile) {
		return
	}
	if err := writeDesktopProfile(profile); err != nil {
		b.showError(L().errTitle, fmt.Sprintf(L().profileSaveErrFmt, err))
		return
	}
	if err := relaunchSelf(); err != nil {
		// The choice is already recorded but this process is staying — put the
		// record back to what's actually running, so the next cold start doesn't
		// switch behind the user's back right after we said we couldn't.
		_ = writeDesktopProfile(current)
		b.showError(L().errTitle, fmt.Sprintf(L().profileRelaunchErrFmt, err))
		return
	}
	b.allowQuit.Store(true)
	b.app.Quit()
}

// confirmProfileSwitch asks only when there is something to lose. An idle hub
// switches without a dialog: the restart is the whole cost, and asking about it
// every time would train the user to dismiss the question that matters.
func (b *nativeBridge) confirmProfileSwitch(profile string) bool {
	srv := b.srv.Load()
	if srv == nil {
		return true
	}
	name := desktopProfileLabel(profile)
	switch srv.Activity() {
	case server.ActivityAsk:
		return b.confirm(L().profileSwitchTitle,
			fmt.Sprintf(L().profileSwitchAskFmt, name),
			L().profileSwitchOK, L().profileSwitchCancel)
	case server.ActivityBusy:
		return b.confirm(L().profileSwitchTitle,
			fmt.Sprintf(L().profileSwitchBusyFmt, name),
			L().profileSwitchOK, L().profileSwitchCancel)
	}
	return true
}

// relaunchSelf starts a fresh copy of this app that waits for this process to
// exit before doing anything. Exec'ing the binary directly is right on all
// three platforms — on macOS the executable's path inside the bundle is what
// identifies the app, which is the same thing isBundled reads.
func relaunchSelf() error {
	cmd, err := relaunchCommand()
	if err != nil {
		return err
	}
	return cmd.Start()
}

// relaunchCommand builds that copy. Split out from relaunchSelf so the part
// that can be wrong — which binary, and the pid it is told to wait for — is
// checkable without actually starting a second app.
func relaunchCommand() (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe)
	// A process running under a named profile has OCTO_PROFILE set — Configure
	// put it there. The replacement must NOT inherit it: selectDesktopProfile
	// treats a set OCTO_PROFILE as an explicit choice and returns before ever
	// reading the recorded one, which would restart us into the profile we were
	// asked to leave. (Passing --profile as argv instead doesn't work — the
	// default profile is the empty string, which the flag parser rejects.)
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, datahome.ProfileEnv+"=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, awaitPidEnv+"="+strconv.Itoa(os.Getpid()))
	// The replacement must not be tied to this process's lifetime or console.
	detachRelaunch(cmd)
	return cmd, nil
}
