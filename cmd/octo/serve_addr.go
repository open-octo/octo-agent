package main

import (
	"flag"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/open-octo/octo-agent/internal/serveproc"
)

// autoPortBase is where a named profile starts looking when it has no pinned
// address yet. 8088 belongs to the default profile — every client ships with
// that number baked in — so a second profile starts one above it.
const autoPortBase = 8089

// autoPortSpan bounds the search. Nobody runs a hundred profiles; the bound is
// there so a machine with something odd going on fails with a message instead
// of walking to 65535.
const autoPortSpan = 100

// autoPortHost is the loopback host auto-selected addresses bind to. Exposing a
// profile on a LAN interface is a deliberate act, so it stays an explicit
// --addr rather than something a port scan can arrive at.
const autoPortHost = "127.0.0.1"

// Test seams, mirroring the daemon's aliases in serve_daemon.go.
var (
	readPinnedAddr  = serveproc.ReadAddr
	writePinnedAddr = serveproc.WriteAddr
	pinnedAddrPath  = serveproc.AddrPath
	probeAddr       = probeAddrListen
)

// resolveServeAddr decides what `octo serve` binds to.
//
// The default profile is untouched: 127.0.0.1:8088, the number every client
// already knows. A named profile would collide with it, so one is chosen for
// the user and then *kept* — written to serve.addr in the profile's data root
// and reused verbatim on every later start, because an address a phone or an
// Obsidian plugin stored is only useful if it survives a restart.
//
// That is also why a pinned port that turns out to be busy is an error instead
// of a slide to the next free one. Something else holding your port is exactly
// the case where quietly landing somewhere else strands every client that knows
// the old number, and does it silently. An explicit --addr re-pins, which is
// both the escape hatch from a busy pin and the way to move a profile on
// purpose.
func resolveServeAddr(profile string, explicit bool, addr string) (string, error) {
	if profile == "" {
		return addr, nil
	}
	if explicit {
		if err := writePinnedAddr(addr); err != nil {
			return "", fmt.Errorf("pin %s for profile %q: %w", addr, profile, err)
		}
		return addr, nil
	}
	if pinned, ok := readPinnedAddr(); ok {
		if err := probeAddr(pinned); err != nil {
			return "", pinnedAddrBusy(profile, pinned, err)
		}
		return pinned, nil
	}
	for i := 0; i < autoPortSpan; i++ {
		candidate := net.JoinHostPort(autoPortHost, strconv.Itoa(autoPortBase+i))
		if err := probeAddr(candidate); err != nil {
			continue
		}
		if err := writePinnedAddr(candidate); err != nil {
			return "", fmt.Errorf("pin %s for profile %q: %w", candidate, profile, err)
		}
		return candidate, nil
	}
	return "", fmt.Errorf("profile %q: no free port between %d and %d — free one, or pass --addr",
		profile, autoPortBase, autoPortBase+autoPortSpan-1)
}

// pinnedAddrBusy explains a busy pin in the terms the user has to act on: which
// profile, which address, where the pin is recorded, and the two ways out.
func pinnedAddrBusy(profile, addr string, cause error) error {
	var b strings.Builder
	fmt.Fprintf(&b, "profile %q is pinned to %s, but that address is in use (%v)", profile, addr, cause)
	if path, err := pinnedAddrPath(); err == nil {
		fmt.Fprintf(&b, "\n  pinned by: %s", path)
	}
	fmt.Fprintf(&b, "\n  if this profile's own backend is already up: octo serve --profile %s status", profile)
	fmt.Fprintf(&b, "\n  to move this profile somewhere else: octo serve --profile %s --addr %s:<port>", profile, autoPortHost)
	return fmt.Errorf("%s", b.String())
}

// probeAddrListen reports whether addr can be bound right now, by binding it
// and letting go again. The gap between this and the real listen is why the
// worker still reports its own bind errors — this answers "is this port taken",
// not "reserve this port".
func probeAddrListen(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return ln.Close()
}

// flagWasSet reports whether the user actually passed name, as opposed to
// getting its default value. flag exposes no accessor for that, but Visit walks
// only the flags that were set.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// withAddrArg replaces any --addr in args with the resolved one, so the value
// this process chose is what the daemon child and the supervisor's worker bind
// — they must not re-run the search and land somewhere else.
func withAddrArg(args []string, addr string) []string {
	out := make([]string, 0, len(args)+1)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-addr" || a == "--addr":
			i++ // drop the separate value too
		case strings.HasPrefix(a, "-addr=") || strings.HasPrefix(a, "--addr="):
		default:
			out = append(out, a)
		}
	}
	return append(out, "--addr="+addr)
}
