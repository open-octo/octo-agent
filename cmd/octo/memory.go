package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/open-octo/octo-agent/internal/memory"
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
		// Resolve symlinks so an explicit path lands on the same slug a session
		// running in that directory would get: os.Getwd() reports the resolved
		// form, and on macOS /tmp/x vs /private/tmp/x hash to different slugs.
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		cwd = abs
	}
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
		// Say so explicitly: the notes here are the shared/global set, not a
		// project's — otherwise the header reads as if this directory had
		// project memory of its own. "no project of its own" rather than "not a
		// git repo": a bare repo IS a repo, it just has no work tree.
		fmt.Fprintf(stdout, "%s has no project of its own — using the shared memory directory.\n", cwd)
		// Sessions that ran here before non-repo dirs shared the global tier
		// wrote into a slug directory for this path. Those notes are no longer
		// injected anywhere, so name the directory rather than orphaning it
		// silently — moving them is a judgement call, not something to do
		// behind the user's back.
		if legacy, err := memory.Dir(cwd); err == nil && legacy != dir {
			if n := countMarkdown(legacy); n > 0 {
				fmt.Fprintf(stdout, "Note: %d earlier note file(s) for this directory are no longer loaded, in:\n  %s\n"+
					"Move anything still useful into the shared directory below.\n", n, legacy)
			}
		}
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

// countMarkdown counts the .md files directly in dir — "does this directory
// hold notes worth telling the user about", not a recursive inventory.
func countMarkdown(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".md" {
			n++
		}
	}
	return n
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
