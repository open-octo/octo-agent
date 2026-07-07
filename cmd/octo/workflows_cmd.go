package main

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/open-octo/octo-agent/internal/tools"
)

// runWorkflows handles `octo workflows [list|path]`. Bare `octo workflows`
// defaults to list. Read-only, unlike `octo skills`: a saved workflow is
// created/edited conversationally (the workflow_save tool, guided by the
// workflow-creator skill), so there's no CLI writer to mirror.
func runWorkflows(args []string, stdout, stderr io.Writer) int {
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "list":
		return workflowsList(stdout)
	case "path":
		return workflowsPath(stdout)
	default:
		fmt.Fprintf(stderr, "octo workflows: unknown subcommand %q (want list | path)\n", sub)
		return 2
	}
}

func workflowsList(stdout io.Writer) int {
	all := tools.ListNamedWorkflows()
	if len(all) == 0 {
		fmt.Fprintln(stdout, "No workflows found.")
		fmt.Fprintln(stdout, "Defaults ship with the binary; add your own under ~/.octo/workflows or ./.octo/workflows, or ask the agent to build one (the workflow-creator skill).")
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
	fmt.Fprintln(stdout, "Workflows (run with the workflow tool's `name` param; project overrides user overrides default):")
	for _, w := range all {
		fmt.Fprintf(stdout, "  %-20s [%-7s] %s\n", w.Name, w.Source, w.Description)
	}
	return 0
}

func workflowsPath(stdout io.Writer) int {
	cwd, _ := os.Getwd()
	fmt.Fprintln(stdout, "Workflow roots (lowest → highest precedence):")
	fmt.Fprintln(stdout, "  default  (embedded in the binary, not on disk)")
	fmt.Fprintf(stdout, "  user     %s\n", tools.UserWorkflowsRoot())
	fmt.Fprintf(stdout, "  project  %s\n", tools.ProjectWorkflowsRoot(cwd))
	return 0
}
