package main

import (
	"flag"
	"strings"
)

// The address a profile binds to is decided in internal/serveproc, shared with
// the desktop hub so both backends answer on the same port for a given profile.
// What stays here is the CLI-only half: reading whether the user passed --addr,
// and rewriting it into the args handed to the daemon child and the worker.

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
