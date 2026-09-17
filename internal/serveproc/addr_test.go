package serveproc

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// stubPins redirects the pin file and the port probe at in-memory fakes, so the
// tests below never touch a real ~/.octo or a real socket. It returns a reader
// for whatever the code under test pinned.
func stubPins(t *testing.T, start string, busy ...string) func() string {
	t.Helper()
	pinned := start
	hasPin := start != ""
	taken := map[string]bool{}
	for _, a := range busy {
		taken[a] = true
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("OCTO_PROFILE", "work")
	if hasPin {
		dir := filepath.Join(home, ".octo-work")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "serve.addr"), []byte(pinned+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	origProbe := probeAddr
	t.Cleanup(func() { probeAddr = origProbe })
	probeAddr = func(addr string) error {
		if taken[addr] {
			return errors.New("address already in use")
		}
		return nil
	}
	return func() string {
		got, _ := ReadAddr()
		return got
	}
}

func TestResolveServeAddr_DefaultProfileUntouched(t *testing.T) {
	stubPins(t, "")
	got, err := ResolveAddr("", false, "127.0.0.1:8088")
	if err != nil {
		t.Fatal(err)
	}
	if got != "127.0.0.1:8088" {
		t.Errorf("default profile addr = %q, want 127.0.0.1:8088", got)
	}
}

func TestResolveServeAddr_FirstRunTakesBaseAndPinsIt(t *testing.T) {
	pinned := stubPins(t, "")
	got, err := ResolveAddr("work", false, "127.0.0.1:8088")
	if err != nil {
		t.Fatal(err)
	}
	if want := "127.0.0.1:8089"; got != want {
		t.Errorf("addr = %q, want %q", got, want)
	}
	if pinned() != got {
		t.Errorf("pinned %q, want the address it returned (%q)", pinned(), got)
	}
}

func TestResolveServeAddr_FirstRunScansPastBusyPorts(t *testing.T) {
	stubPins(t, "", "127.0.0.1:8089", "127.0.0.1:8090")
	got, err := ResolveAddr("work", false, "127.0.0.1:8088")
	if err != nil {
		t.Fatal(err)
	}
	if want := "127.0.0.1:8091"; got != want {
		t.Errorf("addr = %q, want %q", got, want)
	}
}

// The point of pinning: the same profile keeps the same port, so an address a
// client stored stays valid. Without this the scan would hand 8089 to whoever
// starts first.
func TestResolveServeAddr_ReusesThePinEvenWhenLowerPortsAreFree(t *testing.T) {
	stubPins(t, "127.0.0.1:8093")
	got, err := ResolveAddr("work", false, "127.0.0.1:8088")
	if err != nil {
		t.Fatal(err)
	}
	if want := "127.0.0.1:8093"; got != want {
		t.Errorf("addr = %q, want the pinned %q", got, want)
	}
}

// A busy pin is an error, not a slide to the next free port: moving silently is
// exactly what strands the clients that know the old number.
func TestResolveServeAddr_BusyPinIsAnError(t *testing.T) {
	stubPins(t, "127.0.0.1:8089", "127.0.0.1:8089")
	_, err := ResolveAddr("work", false, "127.0.0.1:8088")
	if err == nil {
		t.Fatal("a busy pin should fail rather than pick another port")
	}
	for _, want := range []string{"work", "127.0.0.1:8089", "in use", "--addr", "status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q; got:\n%s", want, err)
		}
	}
}

func TestResolveServeAddr_ExplicitAddrWinsAndRepins(t *testing.T) {
	pinned := stubPins(t, "127.0.0.1:8089", "127.0.0.1:8089")
	got, err := ResolveAddr("work", true, "127.0.0.1:9100")
	if err != nil {
		t.Fatal(err)
	}
	if want := "127.0.0.1:9100"; got != want {
		t.Errorf("addr = %q, want %q", got, want)
	}
	if pinned() != "127.0.0.1:9100" {
		t.Errorf("explicit --addr should re-pin the profile; pin is %q", pinned())
	}
}

func TestResolveServeAddr_ExhaustedRangeExplainsItself(t *testing.T) {
	busy := make([]string, 0, autoPortSpan)
	for i := 0; i < autoPortSpan; i++ {
		busy = append(busy, "127.0.0.1:"+strconv.Itoa(AutoPortBase+i))
	}
	stubPins(t, "", busy...)
	_, err := ResolveAddr("work", false, "127.0.0.1:8088")
	if err == nil {
		t.Fatal("an exhausted range should fail")
	}
	if !strings.Contains(err.Error(), "--addr") {
		t.Errorf("error should point at --addr; got %v", err)
	}
}

// A pin file that exists but doesn't parse must not silently become "no pin" —
// picking a fresh port would overwrite someone's edit without a word, the same
// silent relocation a busy pin is an error to avoid.
func TestResolveServeAddr_MalformedPinIsAnError(t *testing.T) {
	stubPins(t, "not-an-address")
	_, err := ResolveAddr("work", false, "127.0.0.1:8088")
	if err == nil {
		t.Fatal("a malformed pin should fail rather than be discarded")
	}
	for _, want := range []string{"work", "not-an-address", "serve.addr", "delete"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q; got:\n%s", want, err)
		}
	}
}

// ReadAddr itself stays forgiving — it serves status displays too, where a
// missing or malformed pin just means "nothing to report". The strictness
// lives in ResolveAddr, which is about to act on the answer.
func TestReadAddr(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		stubPins(t, "")
		if addr, ok := ReadAddr(); ok || addr != "" {
			t.Errorf("ReadAddr = %q, %v, want no pin", addr, ok)
		}
	})
	t.Run("malformed content", func(t *testing.T) {
		stubPins(t, "not-an-address")
		if addr, ok := ReadAddr(); ok || addr != "" {
			t.Errorf("ReadAddr = %q, %v, want no pin", addr, ok)
		}
	})
	t.Run("valid pin", func(t *testing.T) {
		stubPins(t, "127.0.0.1:8093")
		if addr, ok := ReadAddr(); !ok || addr != "127.0.0.1:8093" {
			t.Errorf("ReadAddr = %q, %v, want the pin", addr, ok)
		}
	})
}
