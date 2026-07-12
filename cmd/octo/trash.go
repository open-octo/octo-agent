package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/trash"
)

// runTrash handles `octo trash <subcommand>` — the terminal twin of the Web
// UI's file-recall view, so a CLI/TUI user can list and recover files the
// agent deleted or overwrote without opening a browser.
//
//	octo trash [list]                 list recoverable files
//	octo trash restore <id|substring> restore one (prompts via flags on conflict)
//	octo trash rm <id|substring>      permanently delete one
//	octo trash empty --all|--old|--orphans
func runTrash(args []string, stdout, stderr io.Writer) int {
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "list", "ls":
		return trashList(stdout, stderr)
	case "restore":
		return trashRestore(args, stdout, stderr)
	case "rm", "delete", "remove":
		return trashRm(args, stdout, stderr)
	case "empty":
		return trashEmpty(args, stdout, stderr)
	default:
		fmt.Fprintln(stderr, trashUsage)
		return 2
	}
}

const trashUsage = `Usage:
  octo trash [list]                    list recoverable files (newest first)
  octo trash restore <id|substring>    restore a file to its original path
        --overwrite                      if the path is taken, trash the current file first
        --as-copy                        if the path is taken, restore as <name>.restored-<ts>
  octo trash rm <id|substring>         permanently delete one entry
  octo trash empty --all|--old|--orphans`

func trashList(stdout, stderr io.Writer) int {
	entries, err := trash.List()
	if err != nil {
		fmt.Fprintf(stderr, "octo trash: %v\n", err)
		return 1
	}
	if len(entries) == 0 {
		fmt.Fprintln(stdout, "Trash is empty.")
		return 0
	}
	var total int64
	for _, e := range entries {
		total += e.Size
	}
	fmt.Fprintf(stdout, "%d recoverable item(s), %s (newest first):\n\n", len(entries), fmtSize(total))
	for _, e := range entries {
		tag := ""
		if e.Orphan {
			tag = "  [orphan]"
		}
		fmt.Fprintf(stdout, "  %-24s  %8s  %-10s%s\n", trashDisplayName(e), fmtSize(e.Size), fmtAge(e.DeletedAt), tag)
		fmt.Fprintf(stdout, "    %s\n", e.Original)
		fmt.Fprintf(stdout, "    id: %s\n", e.ID)
	}
	fmt.Fprintln(stdout, "\nRestore with `octo trash restore <id or a unique part of the path>`.")
	return 0
}

func trashRestore(args []string, stdout, stderr io.Writer) int {
	policy := trash.ConflictAbort
	var query string
	for _, a := range args {
		switch a {
		case "--overwrite":
			policy = trash.ConflictBackupExisting
		case "--as-copy":
			policy = trash.ConflictRename
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(stderr, "octo trash restore: unknown flag %q\n", a)
				return 2
			}
			query = a
		}
	}
	if query == "" {
		fmt.Fprintln(stderr, "usage: octo trash restore <id|substring> [--overwrite|--as-copy]")
		return 2
	}
	e, errCode := resolveEntry(query, stdout, stderr)
	if e == nil {
		return errCode
	}
	res, err := trash.Restore(e.ID, policy)
	if err != nil {
		if err == trash.ErrRestoreConflict {
			fmt.Fprintf(stderr, "A file already exists at %s.\n", e.Original)
			fmt.Fprintln(stderr, "Re-run with --overwrite (trash the current file first) or --as-copy (restore alongside it).")
			return 1
		}
		fmt.Fprintf(stderr, "octo trash restore: %v\n", err)
		return 1
	}
	if res.BackedUpExisting {
		fmt.Fprintln(stdout, "Moved the current file into the trash, then restored the old one.")
	}
	fmt.Fprintf(stdout, "Restored to %s\n", res.RestoredTo)
	return 0
}

func trashRm(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, "usage: octo trash rm <id|substring>")
		return 2
	}
	e, errCode := resolveEntry(args[0], stdout, stderr)
	if e == nil {
		return errCode
	}
	freed, err := trash.Delete(e.ID)
	if err != nil {
		fmt.Fprintf(stderr, "octo trash rm: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Permanently deleted %s (freed %s).\n", trashDisplayName(*e), fmtSize(freed))
	return 0
}

func trashEmpty(args []string, stdout, stderr io.Writer) int {
	mode := ""
	for _, a := range args {
		switch a {
		case "--all":
			mode = "all"
		case "--old":
			mode = "old"
		case "--orphans":
			mode = "orphans"
		default:
			fmt.Fprintf(stderr, "octo trash empty: unknown argument %q\n", a)
			return 2
		}
	}
	if mode == "" {
		fmt.Fprintln(stderr, "usage: octo trash empty --all|--old|--orphans")
		fmt.Fprintln(stderr, "  --all      remove everything")
		fmt.Fprintln(stderr, "  --old      remove entries older than 7 days")
		fmt.Fprintln(stderr, "  --orphans  remove entries whose original project is gone")
		return 2
	}
	count, freed, err := trash.Empty(mode)
	if err != nil {
		fmt.Fprintf(stderr, "octo trash empty: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Removed %d item(s), freed %s.\n", count, fmtSize(freed))
	return 0
}

// resolveEntry finds the single trash entry matching query — an exact ID, or a
// unique substring of the ID, original path, or label. Zero matches → error;
// several → the candidates are printed so the user can disambiguate. On any
// non-unique result it returns (nil, exitCode); the caller returns that code.
func resolveEntry(query string, stdout, stderr io.Writer) (*trash.Entry, int) {
	entries, err := trash.List()
	if err != nil {
		fmt.Fprintf(stderr, "octo trash: %v\n", err)
		return nil, 1
	}
	var matches []trash.Entry
	for _, e := range entries {
		if e.ID == query {
			return &e, 0 // exact ID wins outright
		}
		if strings.Contains(e.ID, query) || strings.Contains(e.Original, query) ||
			(e.Label != "" && strings.Contains(e.Label, query)) {
			matches = append(matches, e)
		}
	}
	switch len(matches) {
	case 0:
		fmt.Fprintf(stderr, "No trash entry matches %q. Run `octo trash` to list.\n", query)
		return nil, 1
	case 1:
		return &matches[0], 0
	default:
		fmt.Fprintf(stderr, "%q matches %d entries — narrow it down:\n", query, len(matches))
		for _, e := range matches {
			fmt.Fprintf(stderr, "  %s  (%s)\n", e.ID, e.Original)
		}
		return nil, 1
	}
}

// trashDisplayName is the human-facing name for an entry: its label (e.g. a
// session title) when derived, else the original file's basename.
func trashDisplayName(e trash.Entry) string {
	if e.Label != "" {
		return e.Label
	}
	return filepath.Base(e.Original)
}

func fmtSize(b int64) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%dB", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(b)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(b)/(1024*1024))
	}
}

func fmtAge(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours())/24)
	}
}
