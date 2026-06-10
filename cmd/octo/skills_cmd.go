package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Leihb/octo-agent/internal/skills"
	"github.com/Leihb/octo-agent/internal/version"
)

// runSkills handles `octo skills [list|add|update|path]`. Bare `octo skills`
// defaults to list. Skills are discovered from three roots (default < user <
// project); the default set ships embedded in the binary and is materialized to
// disk on startup (see internal/skills/defaults.go).
func runSkills(args []string, stdout, stderr io.Writer) int {
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "list":
		return skillsList(stdout)
	case "add":
		return skillsAdd(args[1:], stdout, stderr)
	case "update":
		return skillsUpdate(stdout, stderr)
	case "path":
		return skillsPath(stdout)
	default:
		fmt.Fprintf(stderr, "octo skills: unknown subcommand %q (want list | add | update | path)\n", sub)
		return 2
	}
}

// skillsAdd installs a skill from a public GitHub repository into the
// user-level root (~/.octo/skills). The skill's content is fetched directly
// from the source repository onto this machine — octo redistributes nothing,
// so source-available skills (e.g. the document skills in anthropics/skills)
// can be installed without octo shipping their content.
func skillsAdd(args []string, stdout, stderr io.Writer) int {
	var source string
	force, bad := false, false
	for _, a := range args {
		switch {
		case a == "--force" || a == "-f":
			force = true
		case strings.HasPrefix(a, "-") || source != "":
			bad = true
		default:
			source = a
		}
	}
	if bad || source == "" {
		fmt.Fprintln(stderr, "usage: octo skills add <owner/repo[/sub/path] | github.com tree URL> [--force]")
		fmt.Fprintln(stderr, "  e.g. octo skills add anthropics/skills/skills/docx")
		fmt.Fprintln(stderr, "       octo skills add https://github.com/anthropics/skills/tree/main/skills/pdf")
		return 2
	}
	src, err := skills.ParseSource(source)
	if err != nil {
		fmt.Fprintf(stderr, "octo skills add: %v\n", err)
		return 2
	}
	name, desc, err := skills.Install(src, skills.UserRoot(), force)
	if err != nil {
		fmt.Fprintf(stderr, "octo skills add: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Installed /%s → %s\n", name, filepath.Join(skills.UserRoot(), name))
	fmt.Fprintf(stdout, "  %s\n", desc)
	fmt.Fprintln(stdout, "The skill was fetched from its source repository for your local use; check that repository's license before redistributing it.")
	return 0
}

func skillsList(stdout io.Writer) int {
	cwd, _ := os.Getwd()
	reg := skills.Discover(cwd)
	all := reg.List()
	if len(all) == 0 {
		fmt.Fprintln(stdout, "No skills found.")
		fmt.Fprintln(stdout, "Defaults ship with the binary; add your own under ~/.octo/skills or ./.octo/skills.")
		return 0
	}
	// Group by source for a readable overview: default → user → project.
	order := map[string]int{"default": 0, "user": 1, "project": 2}
	sort.SliceStable(all, func(i, j int) bool {
		if order[all[i].Source] != order[all[j].Source] {
			return order[all[i].Source] < order[all[j].Source]
		}
		return all[i].Name < all[j].Name
	})
	fmt.Fprintln(stdout, "Skills (trigger with /<name>; project overrides user overrides default):")
	for _, s := range all {
		fmt.Fprintf(stdout, "  /%-18s [%-7s] %s\n", s.Name, s.Source, s.Description)
	}
	return 0
}

func skillsUpdate(stdout, stderr io.Writer) int {
	if err := skills.UpdateDefaults(version.Version); err != nil {
		fmt.Fprintf(stderr, "octo skills update: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Default skills refreshed → %s\n", skills.DefaultRoot())
	return 0
}

func skillsPath(stdout io.Writer) int {
	cwd, _ := os.Getwd()
	fmt.Fprintln(stdout, "Skill roots (lowest → highest precedence):")
	fmt.Fprintf(stdout, "  default  %s\n", skills.DefaultRoot())
	fmt.Fprintf(stdout, "  user     %s\n", skills.UserRoot())
	fmt.Fprintf(stdout, "  project  %s\n", filepath.Join(cwd, ".octo", "skills"))
	return 0
}
