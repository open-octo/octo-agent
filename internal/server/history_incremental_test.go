package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

// seedHistorySession saves a session with a two-turn persisted transcript and
// returns it. The events the messages endpoint generates for it are:
// history_user_message, assistant_message, history_user_message, assistant_message.
func seedHistorySession(t *testing.T) *agent.Session {
	t.Helper()
	sess := agent.NewSession("stub-model", "")
	sess.Messages = []agent.Message{
		agent.NewUserMessage("first question"),
		agent.NewAssistantMessage("first answer"),
		agent.NewUserMessage("second question"),
		agent.NewAssistantMessage("second answer"),
	}
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	return sess
}

type messagesResponse struct {
	Incremental bool             `json:"incremental"`
	Epoch       int              `json:"epoch"`
	HistoryLen  int              `json:"history_len"`
	Events      []map[string]any `json:"events"`
}

func getMessages(t *testing.T, srv *Server, id, query string) messagesResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+id+"/messages"+query, nil)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var body messagesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// TestHandleGetSessionMessages_CursorFields: every response — full or
// incremental — carries the cursor fields the web client's reconnect resync
// caches for its next request (#2745).
func TestHandleGetSessionMessages_CursorFields(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := seedHistorySession(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	body := getMessages(t, srv, sess.ID, "")
	if body.Incremental {
		t.Fatal("a parameterless request must answer a full (non-incremental) response")
	}
	if body.HistoryLen != len(sess.Messages) {
		t.Fatalf("history_len = %d, want %d", body.HistoryLen, len(sess.Messages))
	}
	if body.Epoch != 0 {
		t.Fatalf("epoch = %d, want 0 (no rewrite has happened)", body.Epoch)
	}
	if len(body.Events) != 4 {
		t.Fatalf("events = %d, want 4; events=%v", len(body.Events), body.Events)
	}
}

// TestHandleGetSessionMessages_IncrementalTail: a resync whose cursor still
// matches the history version gets only the tail persisted since — with
// absolute message_index values, since branch/edit address the full history.
func TestHandleGetSessionMessages_IncrementalTail(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := seedHistorySession(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	// The client consumed the first two messages (after=2); the epoch is still
	// the initial 0, so the response is the tail only.
	body := getMessages(t, srv, sess.ID, "?after=2&epoch=0")
	if !body.Incremental {
		t.Fatal("matching cursor must answer incrementally")
	}
	if body.HistoryLen != len(sess.Messages) {
		t.Fatalf("history_len = %d, want %d (the full served prefix, not the tail)", body.HistoryLen, len(sess.Messages))
	}
	if len(body.Events) != 2 {
		t.Fatalf("events = %d, want 2 (tail only); events=%v", len(body.Events), body.Events)
	}
	if body.Events[0]["type"] != "history_user_message" || body.Events[0]["content"] != "second question" {
		t.Fatalf("tail starts with %+v, want the second user message", body.Events[0])
	}
	if idx, ok := body.Events[0]["message_index"].(float64); !ok || int(idx) != 2 {
		t.Fatalf("tail message_index = %v, want 2 (absolute persisted index)", body.Events[0]["message_index"])
	}
	if idx, ok := body.Events[1]["message_index"].(float64); !ok || int(idx) != 3 {
		t.Fatalf("tail message_index = %v, want 3 (absolute persisted index)", body.Events[1]["message_index"])
	}
}

// TestHandleGetSessionMessages_IncrementalEmptyTail: nothing persisted while
// the client was disconnected — the common short-blip reconnect — answers an
// empty tail instead of re-serving the whole transcript.
func TestHandleGetSessionMessages_IncrementalEmptyTail(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := seedHistorySession(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	body := getMessages(t, srv, sess.ID, fmt.Sprintf("?after=%d&epoch=0", len(sess.Messages)))
	if !body.Incremental {
		t.Fatal("a cursor at the end of history must still answer incrementally")
	}
	if len(body.Events) != 0 {
		t.Fatalf("events = %d, want 0; events=%v", len(body.Events), body.Events)
	}
}

// TestHandleGetSessionMessages_EpochMismatchFallsBack: an edit / rollback /
// compaction moved the history version while the client was down, so its
// cursor no longer names this history — the response must be the full
// transcript with the NEW epoch, never a silently wrong tail.
func TestHandleGetSessionMessages_EpochMismatchFallsBack(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := seedHistorySession(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	// Move the epoch the way an edit does: truncate and save.
	sess.TruncateTo(2)
	if err := sess.Save(); err != nil {
		t.Fatalf("save after truncate: %v", err)
	}

	// The client still holds the pre-rewrite cursor: after=4 (it had all four
	// messages), epoch=0.
	body := getMessages(t, srv, sess.ID, "?after=4&epoch=0")
	if body.Incremental {
		t.Fatal("a stale epoch must not answer incrementally")
	}
	if body.Epoch != 1 {
		t.Fatalf("epoch = %d, want 1 (the rewrite bumped it)", body.Epoch)
	}
	if body.HistoryLen != 2 {
		t.Fatalf("history_len = %d, want 2 (truncated history)", body.HistoryLen)
	}
	if len(body.Events) != 2 {
		t.Fatalf("events = %d, want 2 (full response of the rewritten history); events=%v", len(body.Events), body.Events)
	}
}

// TestHandleGetSessionMessages_AfterBeyondHistoryFallsBack: even with a
// matching epoch, a cursor past the served history (e.g. it was shortened
// under a same-epoch path, or the client is simply confused) must fall back
// to a full response rather than slice out of bounds.
func TestHandleGetSessionMessages_AfterBeyondHistoryFallsBack(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := seedHistorySession(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	body := getMessages(t, srv, sess.ID, "?after=99&epoch=0")
	if body.Incremental {
		t.Fatal("a cursor beyond the served history must not answer incrementally")
	}
	if len(body.Events) != 4 {
		t.Fatalf("events = %d, want 4 (full response); events=%v", len(body.Events), body.Events)
	}
}

// TestHandleGetSessionMessages_WatermarkCursor covers the subtlest cursor
// transition: a load during a running turn caches history_len at the watermark,
// excluding the in-flight turn's own messages. Once the turn completes and the
// cap lifts, an incremental request from that cursor must serve exactly those
// formerly-hidden messages, with their absolute indices.
func TestHandleGetSessionMessages_WatermarkCursor(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := seedHistorySession(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initWS()

	// The second turn is in flight: only the first user/assistant round predates
	// its watermark, even though progress has already persisted all four messages.
	srv.liveStateMu.Lock()
	srv.liveStates[sess.ID] = &sessionLiveState{historyWatermark: 2}
	srv.liveStateMu.Unlock()

	capped := getMessages(t, srv, sess.ID, "")
	if capped.HistoryLen != 2 || len(capped.Events) != 2 {
		t.Fatalf("mid-turn response: history_len=%d events=%d, want 2/2", capped.HistoryLen, len(capped.Events))
	}

	// Reconnecting while the same turn still runs must not make the tail overlap
	// the WS replay: after=2 is exactly the cap, so the incremental tail is empty.
	midTurn := getMessages(t, srv, sess.ID, "?after=2&epoch=0")
	if !midTurn.Incremental || len(midTurn.Events) != 0 || midTurn.HistoryLen != 2 {
		t.Fatalf("mid-turn incremental response = incremental:%v events:%d history_len:%d, want true/0/2",
			midTurn.Incremental, len(midTurn.Events), midTurn.HistoryLen)
	}

	// The turn finishes: its persisted rounds become history and the watermark
	// goes away with the live state.
	srv.liveStateMu.Lock()
	delete(srv.liveStates, sess.ID)
	srv.liveStateMu.Unlock()

	resumed := getMessages(t, srv, sess.ID, "?after=2&epoch=0")
	if !resumed.Incremental {
		t.Fatal("the capped cursor must remain valid after the turn completes")
	}
	if len(resumed.Events) != 2 {
		t.Fatalf("events = %d, want 2 (the formerly-hidden turn); events=%v", len(resumed.Events), resumed.Events)
	}
	if resumed.Events[0]["content"] != "second question" || resumed.Events[1]["content"] != "second answer" {
		t.Fatalf("tail content = %v / %v, want the second turn", resumed.Events[0]["content"], resumed.Events[1]["content"])
	}
	for i, ev := range resumed.Events {
		if idx, ok := ev["message_index"].(float64); !ok || int(idx) != i+2 {
			t.Fatalf("tail event %d message_index = %v, want %d", i, ev["message_index"], i+2)
		}
	}
}

// TestHandleGetSessionMessages_MalformedCursorFallsBack: garbage parameters
// answer a full response, keeping old or hand-crafted clients unaffected.
func TestHandleGetSessionMessages_MalformedCursorFallsBack(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	sess := seedHistorySession(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	for _, q := range []string{"?after=abc&epoch=0", "?after=2", "?epoch=0", "?after=-1&epoch=0"} {
		body := getMessages(t, srv, sess.ID, q)
		if body.Incremental {
			t.Fatalf("query %q must not answer incrementally", q)
		}
		if len(body.Events) != 4 {
			t.Fatalf("query %q: events = %d, want 4 (full response)", q, len(body.Events))
		}
	}
}
