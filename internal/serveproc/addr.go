package serveproc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// AutoPortBase is where a named profile starts looking when it has no pinned
// address yet. 8088 belongs to the default profile — every client ships with
// that number baked in — so a second profile starts one above it.
const AutoPortBase = 8089

// autoPortSpan bounds the search. Nobody runs a hundred profiles; the bound is
// there so a machine with something odd going on fails with a message instead
// of walking to 65535.
const autoPortSpan = 100

// autoPortHost is the loopback host auto-selected addresses bind to. Exposing a
// profile on a LAN interface is a deliberate act, so it stays an explicit
// choice rather than something a port scan can arrive at.
const autoPortHost = "127.0.0.1"

// Test seams.
var probeAddr = probeAddrListen

// AddrPath returns the path of the pinned bind address (serve.addr in the
// profile's data root, e.g. ~/.octo-work/serve.addr), creating the root if
// needed. Only named profiles use it: the default profile keeps the fixed
// 127.0.0.1:8088 that every client already knows.
func AddrPath() (string, error) {
	dir, err := octoDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "serve.addr"), nil
}

// ReadAddr returns the bind address this profile is pinned to. A missing,
// unreadable, or malformed file reports no pin rather than an error: the file
// is a remembered choice, not state the backend depends on, so the caller is
// free to pick again.
func ReadAddr() (string, bool) {
	addr, present := readAddrRaw()
	if !present {
		return "", false
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return "", false
	}
	return addr, true
}

// readAddrRaw returns the pin file's content without validating it, so
// ResolveAddr can tell "no pin yet" apart from "a pin we can't parse" — the
// former picks a fresh port, the latter must not silently become one.
func readAddrRaw() (string, bool) {
	path, err := AddrPath()
	if err != nil {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	addr := strings.TrimSpace(string(data))
	return addr, addr != ""
}

// WriteAddr pins this profile to addr so later starts reuse it.
func WriteAddr(addr string) error {
	path, err := AddrPath()
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(addr+"\n"), 0o644)
}

// ResolveAddr decides what a backend for profile binds to. Both backends use
// it — `octo serve` and the desktop hub — so a profile answers to the same
// address whichever one is running.
//
// The default profile is untouched: whatever the caller's default is, which is
// 127.0.0.1:8088, the number every client ships with. A named profile would
// collide with it, so one is chosen for the user and then *kept* — written to
// serve.addr in the profile's data root and reused verbatim on every later
// start, because an address a phone or an Obsidian plugin stored is only useful
// if it survives a restart.
//
// That is also why a pinned port that turns out to be busy is an error instead
// of a slide to the next free one. Something else holding the port is exactly
// the case where quietly landing somewhere else strands every client that knows
// the old number, and does it silently. A pin file that exists but doesn't
// parse is an error for the same reason — guessing would overwrite it.
// explicit says the user named the
// address themselves, which wins and re-pins — both the escape hatch from a
// busy pin and the way to move a profile on purpose.
func ResolveAddr(profile string, explicit bool, addr string) (string, error) {
	if profile == "" {
		return addr, nil
	}
	if explicit {
		if err := WriteAddr(addr); err != nil {
			return "", fmt.Errorf("pin %s for profile %q: %w", addr, profile, err)
		}
		return addr, nil
	}
	if pinned, ok := ReadAddr(); ok {
		if err := probeAddr(pinned); err != nil {
			return "", pinnedAddrBusy(profile, pinned, err)
		}
		return pinned, nil
	}
	if raw, present := readAddrRaw(); present {
		return "", malformedAddr(profile, raw)
	}
	for i := 0; i < autoPortSpan; i++ {
		candidate := net.JoinHostPort(autoPortHost, strconv.Itoa(AutoPortBase+i))
		if err := probeAddr(candidate); err != nil {
			continue
		}
		if err := WriteAddr(candidate); err != nil {
			return "", fmt.Errorf("pin %s for profile %q: %w", candidate, profile, err)
		}
		return candidate, nil
	}
	return "", fmt.Errorf("profile %q: no free port between %d and %d — free one, or pass --addr",
		profile, AutoPortBase, AutoPortBase+autoPortSpan-1)
}

// pinnedAddrBusy explains a busy pin in the terms the user has to act on: which
// profile, which address, where the pin is recorded, and the two ways out. The
// desktop hub shows this in a dialog, so it has to read without a terminal
// around it.
func pinnedAddrBusy(profile, addr string, cause error) error {
	var b strings.Builder
	fmt.Fprintf(&b, "profile %q is pinned to %s, but that address is in use (%v)", profile, addr, cause)
	if path, err := AddrPath(); err == nil {
		fmt.Fprintf(&b, "\n  pinned by: %s", path)
	}
	fmt.Fprintf(&b, "\n  if this profile's own backend is already up: octo serve --profile %s status", profile)
	fmt.Fprintf(&b, "\n  to move this profile somewhere else: octo serve --profile %s --addr %s:<port>", profile, autoPortHost)
	return errors.New(b.String())
}

// malformedAddr refuses to start over a pin file we can't parse. Silently
// treating it as "no pin" would pick a fresh port and overwrite the file —
// the same silent relocation a busy pin is an error to avoid, just quieter.
func malformedAddr(profile, raw string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "profile %q: the pinned address %q is not a host:port", profile, raw)
	if path, err := AddrPath(); err == nil {
		fmt.Fprintf(&b, "\n  pinned by: %s", path)
	}
	fmt.Fprintf(&b, "\n  fix the address in that file, or delete it and a fresh port will be chosen")
	return errors.New(b.String())
}

// probeAddrListen reports whether addr can be bound right now, by binding it
// and letting go again. The gap between this and the real listen is why callers
// still report their own bind errors — this answers "is this port taken", not
// "reserve this port".
func probeAddrListen(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return ln.Close()
}
