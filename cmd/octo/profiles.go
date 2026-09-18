package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/open-octo/octo-agent/internal/datahome"
	"github.com/open-octo/octo-agent/internal/profiles"
)

// runProfiles handles `octo profiles <subcommand>` — the terminal side of
// profile management (the Web UI has the same set under 设置 → 数据管理).
// A profile is one ~/.octo-<name> data root; `--profile` picks between them.
//
//	octo profiles [list]            list the roots on disk (current, running, size)
//	octo profiles create <name>     make an empty root for a new profile
//	octo profiles rm <name> --yes   delete a root and everything in it
//	octo profiles path [name]       print a profile's data root
func runProfiles(args []string, stdout, stderr io.Writer) int {
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "list", "ls":
		return profilesList(stdout, stderr)
	case "create", "add", "new":
		return profilesCreate(args, stdout, stderr)
	case "rm", "delete", "remove":
		return profilesRm(args, stdout, stderr)
	case "path":
		return profilesPath(args, stdout, stderr)
	case "help", "--help", "-h":
		printProfilesUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "octo profiles: unknown subcommand %q\n\n", sub)
		printProfilesUsage(stderr)
		return 2
	}
}

func printProfilesUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: octo profiles [list]            list profiles (data roots) on this machine")
	fmt.Fprintln(w, "       octo profiles create <name>     create an empty profile (~/.octo-<name>)")
	fmt.Fprintln(w, "       octo profiles rm <name> --yes   delete a profile and all of its data")
	fmt.Fprintln(w, "       octo profiles path [name]       print a profile's data root")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Select a profile for any command with the global --profile <name> flag (or OCTO_PROFILE).")
	fmt.Fprintln(w, "The default profile (~/.octo) and the one in use cannot be removed; stop a profile's")
	fmt.Fprintln(w, "backend first with `octo serve --profile <name> stop`.")
}

// profileLabel is how a root is named in listings: the default root has no
// name of its own, so it prints as "default".
func profileLabel(name string) string {
	if name == "" {
		return "default"
	}
	return name
}

func profilesList(stdout, stderr io.Writer) int {
	infos, err := profiles.List()
	if err != nil {
		fmt.Fprintf(stderr, "octo profiles: %v\n", err)
		return 1
	}
	if len(infos) == 0 {
		fmt.Fprintln(stdout, "No profiles yet — the default root is created on first run.")
		return 0
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSIZE\tSTATUS\tPATH")
	for _, in := range infos {
		status := "-"
		running := "running"
		if in.Pid != 0 {
			running = fmt.Sprintf("running (pid %d)", in.Pid)
		}
		switch {
		case in.Running && in.Current:
			status = "current, " + running
		case in.Running:
			status = running
		case in.Current:
			status = "current"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", profileLabel(in.Name), fmtSize(in.SizeBytes), status, in.Path)
	}
	tw.Flush()
	return 0
}

func profilesCreate(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "Usage: octo profiles create <name>")
		return 2
	}
	info, err := profiles.Create(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "octo profiles: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Created profile %q at %s\n", info.Name, info.Path)
	fmt.Fprintf(stdout, "Use it with: octo --profile %s\n", info.Name)
	return 0
}

func profilesRm(args []string, stdout, stderr io.Writer) int {
	var name string
	yes := false
	for _, a := range args {
		switch a {
		case "--yes", "-y", "--force", "-f":
			yes = true
		default:
			if name != "" {
				fmt.Fprintln(stderr, "Usage: octo profiles rm <name> --yes")
				return 2
			}
			name = a
		}
	}
	if name == "" {
		fmt.Fprintln(stderr, "Usage: octo profiles rm <name> --yes")
		return 2
	}
	if name == "default" {
		// The listing shows the default root as "default"; say why it is
		// refused instead of reporting a profile by that name as missing.
		fmt.Fprintf(stderr, "octo profiles: %v\n", profiles.ErrDefault)
		return 1
	}
	dir, err := datahome.DirFor(name)
	if err != nil {
		fmt.Fprintf(stderr, "octo profiles: %v\n", profiles.ErrInvalidName)
		return 1
	}
	if _, err := os.Stat(dir); err != nil {
		// Say "not found" before asking anyone to confirm deleting it.
		fmt.Fprintf(stderr, "octo profiles: %v: %s\n", profiles.ErrNotFound, name)
		return 1
	}
	if !yes {
		// Deletion is final and there is no recycle bin for a whole root, so
		// the confirmation is an explicit re-run rather than a prompt: it
		// works the same in a script, a pipe and a terminal.
		fmt.Fprintf(stdout, "This permanently deletes profile %q and everything under %s:\n", name, dir)
		fmt.Fprintln(stdout, "its config and API keys, sessions, memory, skills, IM credentials and logs.")
		fmt.Fprintf(stdout, "Re-run with --yes to confirm: octo profiles rm %s --yes\n", name)
		return 1
	}
	if err := profiles.Remove(name); err != nil {
		fmt.Fprintf(stderr, "octo profiles: %v\n", err)
		if errors.Is(err, profiles.ErrCurrent) {
			fmt.Fprintln(stderr, "Run this from another profile, e.g. without --profile.")
		}
		return 1
	}
	fmt.Fprintf(stdout, "Deleted profile %q (%s)\n", name, dir)
	return 0
}

func profilesPath(args []string, stdout, stderr io.Writer) int {
	name := datahome.Current()
	switch len(args) {
	case 0:
	case 1:
		name = args[0]
		if name == "default" {
			name = ""
		}
	default:
		fmt.Fprintln(stderr, "Usage: octo profiles path [name]")
		return 2
	}
	dir, err := datahome.DirFor(name)
	if err != nil {
		fmt.Fprintf(stderr, "octo profiles: %v\n", profiles.ErrInvalidName)
		return 1
	}
	fmt.Fprintln(stdout, dir)
	return 0
}
