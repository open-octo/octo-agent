package main

import (
	"fmt"
	"io"
	"os"

	"github.com/open-octo/octo-agent/internal/memory"
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
	dir, inProject, err := memory.DirForSession(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "octo memory: %v\n", err)
		return 1
	}
	homeDir, _ := memory.HomeDir()
	if homeDir == dir {
		homeDir = "" // same as project (not a repo, or running in home) — don't duplicate
	}

	if sub == "path" {
		fmt.Fprintln(stdout, dir)
		if homeDir != "" {
			fmt.Fprintf(stdout, "Inherited: %s\n", homeDir)
		}
		return 0
	}

	if !inProject {
		// Say so explicitly: the notes written here are the shared/global set,
		// not a project's — otherwise the header reads as if this scratch
		// directory had project memory of its own.
		fmt.Fprintf(stdout, "%s is not a git repo — using the shared memory directory.\n", cwd)
	}
	fmt.Fprintf(stdout, "Memory directory: %s\n", dir)
	printDirEntries(stdout, dir)
	printLint(stdout, dir)

	if homeDir != "" {
		fmt.Fprintf(stdout, "\nInherited memories: %s\n", homeDir)
		printDirEntries(stdout, homeDir)
		printLint(stdout, homeDir)
	}
	return 0
}

// printLint surfaces MEMORY.md problems that would otherwise fail silently
// (over-budget truncation, un-recallable triggered rules).
func printLint(w io.Writer, dir string) {
	for _, warn := range memory.Lint(dir) {
		fmt.Fprintf(w, "  ⚠ %s\n", warn)
	}
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
