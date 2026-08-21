package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/open-octo/octo-agent/internal/memory"
	"github.com/open-octo/octo-agent/internal/server"
)

// runMemory handles `octo memory <subcommand> [dir]`:
//   - path: print the project's and inherited memory directories
//   - list: list the files in them (MEMORY.md + topic files)
//
// The optional dir argument resolves memory for somewhere other than the
// current directory — how the agent finds ANOTHER repo's memory dir when a
// durable fact belongs there rather than in this session's project (see
// memory.RenderInjection). A single command rather than `cd X && octo memory
// path`, since chaining with && is not available in legacy PowerShell.
//
// Memory is plain markdown the agent manages with its file tools; this command
// is just a viewer/locator.
func runMemory(args []string, stdout, stderr io.Writer) int {
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	if sub != "list" && sub != "path" {
		fmt.Fprintln(stderr, "Usage: octo memory [list|path] [dir]")
		return 2
	}

	cwd, _ := os.Getwd()
	if len(args) > 1 {
		target := args[1]
		abs, err := filepath.Abs(target)
		if err != nil {
			fmt.Fprintf(stderr, "octo memory: %v\n", err)
			return 1
		}
		if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
			fmt.Fprintf(stderr, "octo memory: not a directory: %s\n", target)
			return 1
		}
		// No symlink resolution here: memory.Dir normalizes what it is given,
		// which is the one place it happens.
		cwd = abs
	}
	// Memory belongs to projects, and a directory can be referenced by any
	// number of them. Resolve through the registry: a unique claim is that
	// project's tier; several are listed rather than guessed between; none
	// means the shared tier — pointing at a path-derived directory nothing
	// reads anymore would send notes into a black hole.
	claims := server.ProjectsClaimingDir(cwd)
	if len(claims) > 1 {
		fmt.Fprintf(stdout, "%d projects reference this directory — each keeps its own memory:\n", len(claims))
		for _, c := range claims {
			if d, derr := c.MemoryDir(); derr == nil {
				fmt.Fprintf(stdout, "  %s\t%s\n", c.Name, d)
			}
		}
		return 0
	}
	homeDir, _ := memory.HomeDir()
	var dir string
	shared := false
	if len(claims) == 1 {
		d, derr := claims[0].MemoryDir()
		if derr != nil {
			fmt.Fprintf(stderr, "octo memory: %v\n", derr)
			return 1
		}
		dir = d
	} else {
		// No project references this directory: its notes are the shared tier.
		dir, shared = homeDir, true
		homeDir = ""
	}

	if sub == "path" {
		fmt.Fprintln(stdout, dir)
		if homeDir != "" {
			fmt.Fprintf(stdout, "Inherited: %s\n", homeDir)
		}
		return 0
	}

	if shared {
		// Otherwise the header reads as if this directory had memory of its
		// own, when in fact these are the notes every session gets wherever it
		// runs.
		fmt.Fprintln(stdout, "No project references this directory — these are the shared notes every session reads.")
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

// memoryWriteRoots returns the write-allowlist roots for the permission engine,
// mirroring the server's Server.memoryWriteRoots: the whole ~/.octo/memories
// tree rather than just this session's two directories, so a durable fact about
// ANOTHER repo can be filed in that repo's memory dir (see
// memory.RenderInjection's cross-project guidance) without a prompt per save.
//
// memDir == "" means memory is off or unresolvable — then there is nothing to
// whitelist, and no standing write pass is handed out. Falls back to the
// concrete dirs if the root itself can't be resolved.
func memoryWriteRoots(memDir, homeMemDir string) []string {
	if memDir == "" {
		return nil
	}
	if root, err := memory.RootDir(); err == nil {
		return []string{root}
	}
	return []string{memDir, homeMemDir}
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
