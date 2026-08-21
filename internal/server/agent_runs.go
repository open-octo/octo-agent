package server

// Persistence and review for sub-agent / workflow event trails (see
// dev-docs/agent-run-panel-design.md).
//
// Every sub_agent_event / workflow_event the server broadcasts is also
// appended, in its WS wire shape, to a per-session sidecar file
// (<sessions dir>/<sid>.agent-events.jsonl). GET /api/sessions/{id}/agent-runs
// reduces the file into per-run trail snapshots so a reloaded tab can render
// the full trail inside the transcript's sub_agent / workflow tool cards —
// the in-memory retention buffers only serve still-running work.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
)

const agentEventsSuffix = ".agent-events.jsonl"

// agentEventsReadCap bounds how much of the sidecar the reducer reads: an
// oversized file is read from its tail (aligned to the next full line), so
// the oldest runs' trails degrade first. agentEventsWriteCap is the hard
// append ceiling — a session that somehow exceeds it stops persisting new
// events rather than growing without bound (the live panels are unaffected).
const (
	agentEventsReadCap  = 8 << 20
	agentEventsWriteCap = 32 << 20
)

// agentEventsPath resolves the sidecar file for a session id, applying the
// same containment rules as the session store's resolveSessionPath.
func agentEventsPath(sid string) (string, error) {
	if sid == "" {
		return "", fmt.Errorf("session id is empty")
	}
	if filepath.IsAbs(sid) || sid != filepath.Base(sid) || strings.Contains(sid, "..") {
		return "", fmt.Errorf("invalid session id %q", sid)
	}
	dir, err := agent.SessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sid+agentEventsSuffix), nil
}

// appendAgentEvent appends one WS-shaped event payload to the session's
// sidecar. Best-effort: persistence must never fail a live turn, so errors
// are swallowed (the live panel still got the event over the WS).
func (s *Server) appendAgentEvent(sid string, payload map[string]any) {
	path, err := agentEventsPath(sid)
	if err != nil {
		return
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	s.agentEventsMu.Lock()
	defer s.agentEventsMu.Unlock()
	if st, err := os.Stat(path); err == nil && st.Size() > agentEventsWriteCap {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// removeAgentEvents deletes a session's sidecar; called when the session
// itself is deleted. Best-effort.
func (s *Server) removeAgentEvents(sid string) {
	if path, err := agentEventsPath(sid); err == nil {
		_ = os.Remove(path)
	}
}

// ── Reduction to trail snapshots ─────────────────────────────────────────────

// agentTrailStep is one entry in a trail: a tool call (with its capped
// output once finished) or an assistant text block, in event order.
type agentTrailStep struct {
	Kind   string         `json:"kind"` // "tool" | "text"
	ID     string         `json:"id,omitempty"`
	Name   string         `json:"name,omitempty"`
	Input  map[string]any `json:"input,omitempty"`
	Output string         `json:"output,omitempty"`
	Done   bool           `json:"done,omitempty"`
	Error  bool           `json:"error,omitempty"`
	Text   string         `json:"text,omitempty"`
}

// subAgentRunTrail is one sub-agent's reduced trail, keyed by the agent_id the
// sub_agent tool result carries in its UI payload.
type subAgentRunTrail struct {
	AgentID     string           `json:"agent_id"`
	Description string           `json:"description"`
	AgentType   string           `json:"agent_type,omitempty"`
	Status      string           `json:"status"` // running | done | error | cancelled
	StopReason  string           `json:"stop_reason,omitempty"`
	Steps       []agentTrailStep `json:"steps"`
	Result      string           `json:"result,omitempty"`
}

// workflowAgentTrail is one agent()/skill() call inside a workflow run.
type workflowAgentTrail struct {
	AgentID string           `json:"agent_id"`
	Label   string           `json:"label"`
	Status  string           `json:"status"` // running | done | error
	Steps   []agentTrailStep `json:"steps"`
	Reply   string           `json:"reply,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// workflowRunTrail is one workflow run's reduced trail, keyed by the run_id
// the workflow tool result carries in its UI payload.
type workflowRunTrail struct {
	RunID       string                `json:"run_id"`
	Description string                `json:"description"`
	Status      string                `json:"status"` // running | done | error
	Logs        []string              `json:"logs"`
	Agents      []*workflowAgentTrail `json:"agents"`
}

// maxTrailLogLines mirrors the web panel's progress-line cap.
const maxTrailLogLines = 200

type agentRunsResponse struct {
	SubAgents []*subAgentRunTrail `json:"sub_agents"`
	Workflows []*workflowRunTrail `json:"workflows"`
}

// handleGetSessionAgentRuns serves GET /api/sessions/{id}/agent-runs: the
// session's persisted sub-agent and workflow trails, for transcript review.
func (s *Server) handleGetSessionAgentRuns(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("id")
	path, err := agentEventsPath(sid)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := reduceAgentEventsFile(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// reduceAgentEventsFile reads a sidecar (tail-capped) and folds it into trail
// snapshots. A missing file is an empty response, not an error.
func reduceAgentEventsFile(path string) (agentRunsResponse, error) {
	resp := agentRunsResponse{SubAgents: []*subAgentRunTrail{}, Workflows: []*workflowRunTrail{}}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return resp, nil
		}
		return resp, err
	}
	defer f.Close()

	// Oversized sidecar: read only the tail, aligned to the next full line, so
	// the newest runs stay complete and the oldest degrade first.
	if st, err := f.Stat(); err == nil && st.Size() > agentEventsReadCap {
		if _, err := f.Seek(st.Size()-agentEventsReadCap, io.SeekStart); err == nil {
			r := bufio.NewReader(f)
			_, _ = r.ReadBytes('\n') // drop the partial line at the cut
			return reduceAgentEvents(r), nil
		}
	}
	return reduceAgentEvents(f), nil
}

func reduceAgentEvents(r io.Reader) agentRunsResponse {
	resp := agentRunsResponse{SubAgents: []*subAgentRunTrail{}, Workflows: []*workflowRunTrail{}}
	subAgents := map[string]*subAgentRunTrail{}
	workflows := map[string]*workflowRunTrail{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal(line, &ev); err != nil {
			continue // skip a torn/corrupt line rather than failing the whole read
		}
		switch ev["type"] {
		case "sub_agent_event":
			foldSubAgentEvent(ev, subAgents, &resp)
		case "workflow_event":
			foldWorkflowEvent(ev, workflows, &resp)
		}
	}
	return resp
}

func evStr(ev map[string]any, key string) string {
	v, _ := ev[key].(string)
	return v
}

func foldSubAgentEvent(ev map[string]any, byID map[string]*subAgentRunTrail, resp *agentRunsResponse) {
	id := evStr(ev, "agent_id")
	if id == "" {
		return
	}
	t := byID[id]
	if t == nil {
		t = &subAgentRunTrail{AgentID: id, Status: "running", Steps: []agentTrailStep{}}
		byID[id] = t
		resp.SubAgents = append(resp.SubAgents, t)
	}
	if d := evStr(ev, "description"); d != "" {
		t.Description = d
	}
	if at := evStr(ev, "agent_type"); at != "" && t.AgentType == "" {
		t.AgentType = at
	}
	foldTrailStep(&t.Steps, ev)
	switch evStr(ev, "kind") {
	case "started":
		// A Continue round re-opens the trail; earlier steps are kept — the
		// review surface shows the whole history, not just the last round.
		t.Status = "running"
	case "done":
		t.StopReason = evStr(ev, "stop_reason")
		t.Status = doneStatus(t.StopReason)
		if r := evStr(ev, "result"); r != "" {
			t.Result = r
		}
	}
}

func foldWorkflowEvent(ev map[string]any, byID map[string]*workflowRunTrail, resp *agentRunsResponse) {
	id := evStr(ev, "run_id")
	if id == "" {
		return
	}
	t := byID[id]
	if t == nil {
		t = &workflowRunTrail{RunID: id, Status: "running", Logs: []string{}, Agents: []*workflowAgentTrail{}}
		byID[id] = t
		resp.Workflows = append(resp.Workflows, t)
	}
	if d := evStr(ev, "description"); d != "" {
		t.Description = d
	}
	switch evStr(ev, "kind") {
	case "progress":
		if line := evStr(ev, "line"); line != "" {
			t.Logs = append(t.Logs, line)
			if n := len(t.Logs) - maxTrailLogLines; n > 0 {
				t.Logs = t.Logs[n:]
			}
		}
	case "done":
		if evStr(ev, "status") == "error" {
			t.Status = "error"
		} else {
			t.Status = "done"
		}
	case "agent_started", "agent_tool", "agent_tool_done", "agent_tool_error", "agent_text", "agent_done":
		aid := evStr(ev, "agent_id")
		if aid == "" {
			return
		}
		var a *workflowAgentTrail
		for _, cand := range t.Agents {
			if cand.AgentID == aid {
				a = cand
				break
			}
		}
		if a == nil {
			a = &workflowAgentTrail{AgentID: aid, Status: "running", Steps: []agentTrailStep{}}
			t.Agents = append(t.Agents, a)
		}
		if l := evStr(ev, "agent_label"); l != "" {
			a.Label = l
		}
		foldTrailStep(&a.Steps, ev)
		if evStr(ev, "kind") == "agent_done" {
			if e := evStr(ev, "error"); e != "" {
				a.Error = e
				a.Status = "error"
			} else {
				a.Reply = evStr(ev, "reply")
				a.Status = "done"
			}
		}
	}
}

// foldTrailStep folds the shared tool/text step kinds. Sub-agent events use
// bare kinds ("tool", "tool_done", …); workflow agent events carry the same
// payloads under "agent_"-prefixed kinds.
func foldTrailStep(steps *[]agentTrailStep, ev map[string]any) {
	kind := strings.TrimPrefix(evStr(ev, "kind"), "agent_")
	switch kind {
	case "tool":
		input, _ := ev["tool_input"].(map[string]any)
		*steps = append(*steps, agentTrailStep{
			Kind:  "tool",
			ID:    evStr(ev, "tool_id"),
			Name:  evStr(ev, "tool_name"),
			Input: input,
		})
	case "tool_done", "tool_error":
		isErr := kind == "tool_error"
		id := evStr(ev, "tool_id")
		// Complete the matching open tool step (newest first); an unmatched
		// completion (e.g. the "tool" line fell past the read cap) is appended
		// so its output still shows.
		for i := len(*steps) - 1; i >= 0; i-- {
			st := &(*steps)[i]
			if st.Kind != "tool" || st.Done {
				continue
			}
			if (id != "" && st.ID == id) || (id == "" && st.Name == evStr(ev, "tool_name")) {
				st.Output = evStr(ev, "tool_output")
				st.Done = true
				st.Error = isErr
				return
			}
		}
		*steps = append(*steps, agentTrailStep{
			Kind:   "tool",
			ID:     id,
			Name:   evStr(ev, "tool_name"),
			Output: evStr(ev, "tool_output"),
			Done:   true,
			Error:  isErr,
		})
	case "text":
		if txt := evStr(ev, "text"); txt != "" {
			*steps = append(*steps, agentTrailStep{Kind: "text", Text: txt})
		}
	}
}

// doneStatus mirrors the web panel's stop-reason mapping (stores.ts
// applySubAgentEvent): killed → cancelled, empty/error → error, else done.
func doneStatus(stopReason string) string {
	switch stopReason {
	case "killed":
		return "cancelled"
	case "", "error":
		return "error"
	default:
		return "done"
	}
}
