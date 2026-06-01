package main

import (
	"fmt"
	"io"
	"os"

	"github.com/Leihb/octo-agent/internal/memory"
)

// runMemory handles `octo memory <subcommand>`:
//   - path: print the current project's memory directory
//   - list: list the files in it (MEMORY.md + topic files)
//
// Memory is plain markdown the agent manages with its file tools; this command
// is just a viewer/locator.
func runMemory(args []string, stdout, stderr io.Writer) int {
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	if sub != "list" && sub != "path" {
		fmt.Fprintln(stderr, "Usage: octo memory [list|path]")
		return 2
	}

	cwd, _ := os.Getwd()
	dir, err := memory.Dir(memory.ProjectRoot(cwd))
	if err != nil {
		fmt.Fprintf(stderr, "octo memory: %v\n", err)
		return 1
	}

	if sub == "path" {
		fmt.Fprintln(stdout, dir)
		return 0
	}

	fmt.Fprintf(stdout, "Memory directory: %s\n", dir)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		fmt.Fprintln(stdout, "  (empty — nothing remembered yet)")
		return 0
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			fmt.Fprintf(stdout, "  %s/\n", name)
			continue
		}
		if info, ierr := e.Info(); ierr == nil {
			fmt.Fprintf(stdout, "  %-28s %6dB\n", name, info.Size())
		} else {
			fmt.Fprintf(stdout, "  %s\n", name)
		}
	}
	return 0
}
