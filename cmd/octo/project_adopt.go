package main

import (
	"fmt"
	"io"

	"github.com/open-octo/octo-agent/internal/server"
)

// The CLI's three-state project adoption. The terminal's cwd never moves —
// this decides only which project a fresh session is filed under (and thus
// which memory tier it composes with):
//
//   - cwd claimed by exactly one project        → that one;
//   - claimed by several                        → ask (interactive) or stay a task (headless);
//   - claimed by none, but inside a git repo    → create a project mounting it;
//   - claimed by none and not a repo            → stay a task, shared memory tier.
//
// The git gate on the unclaimed branch is not about project *shape* (the Web
// UI mounts repos and plain folders alike) — it is "is this directory worth a
// project row": `cd ~/Downloads && octo` must not leave a Downloads project in
// everyone's sidebar. Same judgement as the old "non-repo cwd reads the
// shared tier" rule, now derived instead of a standalone case.

// projectDecision is what decideProjectForCwd resolved for a fresh session.
type projectDecision struct {
	ProjectID string // "" = the session stays a task
	MemDir    string // the project's memory dir; "" = shared tier only
	Note      string // one line for stderr when something needs saying
}

// decideProjectForCwd resolves the three-state rule for cwd. The project for
// an unclaimed repo is created HERE, at startup, so this very session's
// prompt composes with the project's memory — filing the session into it
// still waits for its first save (see the afterFirstSave hook).
func decideProjectForCwd(cwd string, interactive bool, stdin io.Reader, stdout io.Writer) projectDecision {
	claims := server.ProjectsClaimingDir(cwd)
	switch {
	case len(claims) == 1:
		return decisionFor(claims[0])
	case len(claims) > 1:
		if !interactive {
			return projectDecision{Note: fmt.Sprintf("%d projects reference this directory; the session stays a loose task (run interactively to file it under one)", len(claims))}
		}
		items := make([]selectItem, 0, len(claims)+1)
		for _, c := range claims {
			items = append(items, selectItem{label: c.Name, desc: c.WorkspaceDir, value: c.ID})
		}
		items = append(items, selectItem{label: "none — keep this session a loose task", value: ""})
		picked, ok := runSelect(stdin, stdout, "This directory belongs to several projects — file this session under:", items, claims[0].ID)
		if !ok || picked.value == "" {
			return projectDecision{}
		}
		for _, c := range claims {
			if c.ID == picked.value {
				return decisionFor(c)
			}
		}
		return projectDecision{}
	case insideGitRepo(cwd):
		ref, err := server.EnsureProjectForDirOnly(cwd)
		if err != nil {
			return projectDecision{Note: fmt.Sprintf("could not create a project for this repository: %v", err)}
		}
		if ref.ID == "" {
			// The workspace default itself — never adopted, and nothing worth
			// a note: the session simply stays a task.
			return projectDecision{}
		}
		return decisionFor(ref)
	default:
		return projectDecision{}
	}
}

func decisionFor(ref server.ProjectRef) projectDecision {
	d := projectDecision{ProjectID: ref.ID}
	if dir, err := ref.MemoryDir(); err == nil {
		d.MemDir = dir
	}
	return d
}

// insideGitRepo is the server's gate (server.DirInsideGitRepo) under the local
// name the decision logic reads naturally — ONE implementation, because the
// CLI's three-state rule and the server's adoption pass must judge "worth a
// project row" identically or one will overturn the other on the next start.
func insideGitRepo(dir string) bool { return server.DirInsideGitRepo(dir) }
