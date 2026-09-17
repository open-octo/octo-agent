package main

import (
	"errors"
	"flag"
	"strconv"
	"strings"
	"testing"
)

// stubPins redirects the pin file and the port probe at in-memory fakes, so the
// tests below never touch a real ~/.octo or a real socket. It returns a pointer
// to whatever the code under test pinned.
func stubPins(t *testing.T, start string, busy ...string) *string {
	t.Helper()
	pinned := start
	hasPin := start != ""
	taken := map[string]bool{}
	for _, a := range busy {
		taken[a] = true
	}
	origRead, origWrite, origPath, origProbe := readPinnedAddr, writePinnedAddr, pinnedAddrPath, probeAddr
	t.Cleanup(func() {
		readPinnedAddr, writePinnedAddr, pinnedAddrPath, probeAddr = origRead, origWrite, origPath, origProbe
	})
	readPinnedAddr = func() (string, bool) { return pinned, hasPin }
	writePinnedAddr = func(addr string) error {
		pinned = addr
		hasPin = true
		return nil
	}
	pinnedAddrPath = func() (string, error) { return "/tmp/fake/serve.addr", nil }
	probeAddr = func(addr string) error {
		if taken[addr] {
			return errors.New("address already in use")
		}
		return nil
	}
	return &pinned
}

func TestResolveServeAddr_DefaultProfileUntouched(t *testing.T) {
	stubPins(t, "")
	got, err := resolveServeAddr("", false, "127.0.0.1:8088")
	if err != nil {
		t.Fatal(err)
	}
	if got != "127.0.0.1:8088" {
		t.Errorf("default profile addr = %q, want 127.0.0.1:8088", got)
	}
}

func TestResolveServeAddr_FirstRunTakesBaseAndPinsIt(t *testing.T) {
	pinned := stubPins(t, "")
	got, err := resolveServeAddr("work", false, "127.0.0.1:8088")
	if err != nil {
		t.Fatal(err)
	}
	if want := "127.0.0.1:8089"; got != want {
		t.Errorf("addr = %q, want %q", got, want)
	}
	if *pinned != got {
		t.Errorf("pinned %q, want the address it returned (%q)", *pinned, got)
	}
}

func TestResolveServeAddr_FirstRunScansPastBusyPorts(t *testing.T) {
	stubPins(t, "", "127.0.0.1:8089", "127.0.0.1:8090")
	got, err := resolveServeAddr("work", false, "127.0.0.1:8088")
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
	got, err := resolveServeAddr("work", false, "127.0.0.1:8088")
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
	_, err := resolveServeAddr("work", false, "127.0.0.1:8088")
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
	got, err := resolveServeAddr("work", true, "127.0.0.1:9100")
	if err != nil {
		t.Fatal(err)
	}
	if want := "127.0.0.1:9100"; got != want {
		t.Errorf("addr = %q, want %q", got, want)
	}
	if *pinned != "127.0.0.1:9100" {
		t.Errorf("explicit --addr should re-pin the profile; pin is %q", *pinned)
	}
}

func TestResolveServeAddr_ExhaustedRangeExplainsItself(t *testing.T) {
	busy := make([]string, 0, autoPortSpan)
	for i := 0; i < autoPortSpan; i++ {
		busy = append(busy, "127.0.0.1:"+strconv.Itoa(autoPortBase+i))
	}
	stubPins(t, "", busy...)
	_, err := resolveServeAddr("work", false, "127.0.0.1:8088")
	if err == nil {
		t.Fatal("an exhausted range should fail")
	}
	if !strings.Contains(err.Error(), "--addr") {
		t.Errorf("error should point at --addr; got %v", err)
	}
}

func TestWithAddrArg_ReplacesEveryForm(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string
	}{
		{"no addr", []string{"--no-supervisor"}},
		{"separate value", []string{"--addr", "127.0.0.1:8088", "--no-supervisor"}},
		{"single dash", []string{"-addr", "127.0.0.1:8088"}},
		{"equals form", []string{"--addr=127.0.0.1:8088", "--tunnel"}},
		{"single dash equals", []string{"-addr=127.0.0.1:8088"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := withAddrArg(tc.in, "127.0.0.1:8091")
			n := 0
			for _, a := range got {
				if strings.HasPrefix(a, "-addr") || strings.HasPrefix(a, "--addr") {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("want exactly one addr flag, got %d: %q", n, got)
			}
			if got[len(got)-1] != "--addr=127.0.0.1:8091" {
				t.Errorf("last argument = %q, want the resolved addr", got[len(got)-1])
			}
		})
	}
}

// The daemon derives the URL it waits on and prints from the same args, so the
// rewrite has to be legible to it.
func TestWithAddrArg_IsReadBackByDaemonDialAddr(t *testing.T) {
	args := withAddrArg([]string{"--addr", "127.0.0.1:8088"}, "127.0.0.1:8091")
	if got := daemonDialAddr(args); got != "127.0.0.1:8091" {
		t.Errorf("daemonDialAddr = %q, want 127.0.0.1:8091", got)
	}
}

func TestFlagWasSet(t *testing.T) {
	newFS := func() (*flag.FlagSet, *string) {
		fs := flag.NewFlagSet("serve", flag.ContinueOnError)
		addr := fs.String("addr", "127.0.0.1:8088", "")
		return fs, addr
	}
	fs, _ := newFS()
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if flagWasSet(fs, "addr") {
		t.Error("addr reported as set when it only has its default")
	}
	fs, _ = newFS()
	if err := fs.Parse([]string{"--addr", "127.0.0.1:9000"}); err != nil {
		t.Fatal(err)
	}
	if !flagWasSet(fs, "addr") {
		t.Error("addr reported as unset after being passed")
	}
}
