package main

import (
	"fmt"
	"io"
	"os"

	"github.com/Leihb/octo-agent/internal/memory"
)

// runMemory handles `octo memory <subcommand>`:
//   - path: print the current project's and inherited memory directories
//   - list: list the files in them (MEMORY.md + topic files)
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
	homeDir, _ := memory.HomeDir()
	if homeDir == dir {
		homeDir = "" // same as project (running in home) — don't duplicate
	}

	if sub == "path" {
		fmt.Fprintln(stdout, dir)
		if homeDir != "" {
			fmt.Fprintf(stdout, "Inherited: %s\n", homeDir)
		}
		return 0
	}

	fmt.Fprintf(stdout, "Memory directory: %s\n", dir)
	printDirEntries(stdout, dir)

	if homeDir != "" {
		fmt.Fprintf(stdout, "\nInherited memories: %s\n", homeDir)
		printDirEntries(stdout, homeDir)
	}
	return 0
}

func printDirEntries(w io.Writer, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		fmt.Fprintln(w, "  (empty — nothing remembered yet)")
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			fmt.Fprintf(w, "  %s/\n", name)
			continue
		}
		if info, ierr := e.Info(); ierr == nil {
			fmt.Fprintf(w, "  %-28s %6dB\n", name, info.Size())
		} else {
			fmt.Fprintf(w, "  %s\n", name)
		}
	}
}
