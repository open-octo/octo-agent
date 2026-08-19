package server

import (
	"log/slog"
)

// dissolvePlainGroups retires the plain group — a name with sessions under it
// and no working directory — from registries written while it was a concept the
// user could create. The sidebar now has two things the user makes, a task and
// a project, and a plain group was neither: it took a row that looked like a
// project and carried none of what a project carries (a directory for the
// tools, a memory tier, a notes block), so the same list showed three kinds of
// row for two kinds of thing.
//
// Dissolving is deleting the group, nothing else. Its member sessions are not
// touched and not deleted — a session in no group is a task, which is exactly
// what those sessions were being organised as. Anything one of them carries in
// its own WorkingDir is then picked up by adoptTaskWorkingDirs, so a session
// that really was working in a directory still ends up in a project for it.
// That ordering is the reason this runs first.
//
// The one group the user did not create survives: a cron task's run cluster,
// which legitimately has no directory. Those predate the TaskID field, so the
// scheduler is asked which group belongs to which task and the field is
// backfilled before anything is deleted — without that, every scheduled task's
// history would be dissolved along with the plain groups.
//
// Idempotent: a second run finds no plain groups and nothing to backfill.
func (s *Server) dissolvePlainGroups() {
	// Which groups the scheduler still claims. Read before taking groupMu —
	// the scheduler has its own lock and no ordering with this one.
	claimedBy := map[string]string{} // group ID → task ID
	s.schedulerMu.Lock()
	sch := s.scheduler
	s.schedulerMu.Unlock()
	if sch != nil {
		for _, t := range sch.List() {
			if t.SessionGroupID != "" {
				claimedBy[t.SessionGroupID] = t.ID
			}
		}
	}

	groupMu.Lock()
	defer groupMu.Unlock()
	groups, err := loadSessionGroups()
	if err != nil {
		slog.Warn("dissolve plain groups: load registry", "err", err)
		return
	}

	kept := make([]sessionGroup, 0, len(groups))
	changed := false
	dissolved := 0
	for _, g := range groups {
		if taskID, ok := claimedBy[g.ID]; ok && g.TaskID == "" {
			g.TaskID = taskID // written before the field existed
			changed = true
		}
		if g.isProject() || g.isCronCluster() {
			kept = append(kept, g)
			continue
		}
		slog.Info("dissolved a plain group; its sessions are tasks again",
			"group", g.Name, "sessions", len(g.SessionIDs))
		dissolved++
		changed = true
	}
	if !changed {
		return
	}
	if err := saveSessionGroups(kept); err != nil {
		slog.Warn("dissolve plain groups: save registry", "dissolved", dissolved, "err", err)
	}
}
