package main

import (
	"flag"
	"strings"
	"testing"
)

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
