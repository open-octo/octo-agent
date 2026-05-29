package main

import (
	"context"
	"strings"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/memory"
	"github.com/Leihb/octo-agent/internal/tools"
)

// Consolidation trigger thresholds. Memory is captured live via the `remember`
// tool; this folds those accumulated entries into the summary when due.
const (
	consolidateMinEntries = 5
	consolidateMinAge     = 24 * time.Hour
)

// maybeProcessMemory runs the consolidation trigger at session start. It folds
// the live-captured entries (from the `remember` tool) into the summary when
// due. Best-effort: any side-call error is swallowed so a memory hiccup never
// blocks the session.
func maybeProcessMemory(a *agent.Agent, store *memory.Store) {
	if store == nil {
		return
	}
	ctx := context.Background()
	st := store.LoadState()

	consolidateIfDue(ctx, a, store, &st)

	_ = store.SaveState(st)
}

func consolidateIfDue(ctx context.Context, a *agent.Agent, store *memory.Store, st *memory.State) {
	if !consolidateDue(*st, store) {
		return
	}
	buckets, err := store.ActiveNotesByCwd()
	if err != nil || len(buckets) == 0 {
		return // no new active entries — nothing to fold in
	}

	// One bucket per project root ("" = global). Each folds into its own
	// summary file so a project's facts don't leak into other projects'
	// injected context. Archive happens per-bucket: a bucket that fails to
	// consolidate leaves its entries active and is retried next time, without
	// blocking the buckets that succeeded.
	consolidatedAny := false
	for cwd, newNotes := range buckets {
		if newNotes == "" {
			continue
		}
		priorSummary := store.ReadSummary(cwd)

		// Prefer the sub-agent path (#6) when a Spawner is registered: a child
		// agent runs in its own context with read-only filesystem tools, can
		// read the actual entry files, and returns the new summary text. Falls
		// back to the side-call (priorSummary + newNotes only, no tools) when
		// no Spawner is wired or the sub-agent declines.
		summary := consolidateViaSubAgent(ctx, store, cwd, priorSummary, newNotes)
		if summary == "" {
			summary, err = a.ConsolidateMemory(ctx, priorSummary, newNotes)
			if err != nil || summary == "" {
				continue
			}
		}
		if err := store.WriteSummary(cwd, summary); err != nil {
			continue
		}
		if err := store.ArchiveCwd(cwd); err != nil {
			continue
		}
		consolidatedAny = true
	}
	if consolidatedAny {
		st.LastConsolidated = time.Now().Format("2006-01-02")
	}
}

// consolidationToolAllowlist restricts the consolidator sub-agent to read-only
// research. It must not have write_file / edit_file (the parent calls
// Store.WriteSummary, which adds the v1 marker and commits via git), terminal
// (out of scope), remember (would mutate memory mid-consolidation), or
// launch_agent (recursion is filtered out by the spawner anyway, listing it
// explicitly is defense in depth).
var consolidationToolAllowlist = []string{"read_file", "grep", "glob"}

// consolidateViaSubAgent runs the consolidation pass as a sub-agent with
// read-only filesystem tools. Returns the new summary text on success or ""
// when no spawner is configured / the sub-agent errored / the sub-agent
// returned nothing. The empty case is non-fatal — caller falls back to the
// side-call.
//
// The sub-agent's win over the side-call: it can read the actual <slug>.md
// frontmatter for context that didn't make it into newNotes, and check
// MEMORY.md for cross-references — autonomously, in its
// own context window.
func consolidateViaSubAgent(ctx context.Context, store *memory.Store, cwd, priorSummary, newNotes string) string {
	spawner := tools.ActiveSpawner()
	if spawner == nil {
		return ""
	}

	res, err := spawner.Spawn(ctx, tools.SpawnRequest{
		Description: "Consolidate cross-session memory",
		Prompt:      buildConsolidationPrompt(store.Dir(), cwd, priorSummary, newNotes),
		Tools:       consolidationToolAllowlist,
	})
	if err != nil {
		return ""
	}
	out := strings.TrimSpace(res.Reply)
	// Strip a leading code fence the sub-agent might wrap the summary in.
	if strings.HasPrefix(out, "```") {
		if nl := strings.IndexByte(out, '\n'); nl >= 0 {
			out = out[nl+1:]
		}
		if idx := strings.LastIndex(out, "```"); idx >= 0 {
			out = out[:idx]
		}
		out = strings.TrimSpace(out)
	}
	return out
}

// buildConsolidationPrompt assembles the sub-agent's instructions. The prompt
// is self-contained: the sub-agent doesn't see the parent's conversation, so
// the current summary, the new notes, and pointers to the on-disk memory dir
// must all be inline here.
func buildConsolidationPrompt(memDir, cwd, priorSummary, newNotes string) string {
	var b strings.Builder
	b.WriteString("You are consolidating cross-session memory for the octo coding agent.\n\n")
	if cwd == "" {
		b.WriteString("Scope: the GLOBAL bucket — facts not tied to any one project " +
			"(who the user is, cross-cutting preferences, general references).\n\n")
	} else {
		b.WriteString("Scope: facts specific to the project at " + cwd + ". " +
			"Keep them project-scoped; do not fold in unrelated global facts.\n\n")
	}
	b.WriteString("Memory layout (read-only access via read_file / grep / glob):\n")
	if memDir != "" {
		b.WriteString("- Root: " + memDir + "\n")
		b.WriteString("- " + memDir + "/MEMORY.md (index of slugs)\n")
		b.WriteString("- " + memDir + "/<slug>.md (one fact per file, with frontmatter incl. cwd)\n")
	}
	b.WriteString("\nCurrent consolidated summary for this scope (may be empty on first pass):\n\n")
	if priorSummary != "" {
		b.WriteString(priorSummary)
	} else {
		b.WriteString("(empty — this is the first consolidation pass)")
	}
	b.WriteString("\n\nNew memory entries since the last consolidation (the index digest):\n\n")
	b.WriteString(newNotes)
	b.WriteString("\n\nYour task: produce the UPDATED consolidated summary. Fold the new entries into the current summary, dedupe, drop anything stale or trivial, and keep load-bearing facts. Be terse — bullet points, grouped loosely by kind (who the user is, how they like to work, ongoing project context, useful references). If you need more context than the digest gives you (a specific quote, the rationale behind a feedback fact), use read_file / grep / glob to look at the actual files.\n\n")
	b.WriteString("Output ONLY the new summary text. No preamble, no code fences, no commentary about what you changed. The parent writes your output to this scope's summary file (it adds the protocol marker automatically — you don't need to).\n")
	return b.String()
}

func consolidateDue(st memory.State, store *memory.Store) bool {
	entries, err := store.List()
	if err != nil || len(entries) < consolidateMinEntries {
		return false
	}
	if st.LastConsolidated == "" {
		return true
	}
	last, err := time.Parse("2006-01-02", st.LastConsolidated)
	if err != nil {
		return true
	}
	return time.Since(last) >= consolidateMinAge
}
