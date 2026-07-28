package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/agent"
)

func TestBatchQueuedTurns(t *testing.T) {
	item := func(text string) agent.InboxItem { return agent.InboxItem{Text: text} }

	tests := []struct {
		name   string
		queued []queuedTurn
		want   [][]string
	}{
		{
			name: "consecutive steers fold into one turn",
			queued: []queuedTurn{
				{item: item("a")}, {item: item("b")},
			},
			want: [][]string{{"a", "b"}},
		},
		{
			name: "each queued message gets its own turn",
			queued: []queuedTurn{
				{item: item("a"), standalone: true}, {item: item("b"), standalone: true},
			},
			want: [][]string{{"a"}, {"b"}},
		},
		{
			name: "a queued message splits the steers around it",
			queued: []queuedTurn{
				{item: item("s1")},
				{item: item("q"), standalone: true},
				{item: item("s2")}, {item: item("s3")},
			},
			want: [][]string{{"s1"}, {"q"}, {"s2", "s3"}},
		},
		{
			name:   "empty queue yields no turns",
			queued: nil,
			want:   nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := batchQueuedTurns(tc.queued)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d batches, want %d: %+v", len(got), len(tc.want), got)
			}
			for i, batch := range got {
				if len(batch) != len(tc.want[i]) {
					t.Fatalf("batch %d has %d items, want %d", i, len(batch), len(tc.want[i]))
				}
				for j, q := range batch {
					if q.item.Text != tc.want[i][j] {
						t.Errorf("batch %d item %d = %q, want %q", i, j, q.item.Text, tc.want[i][j])
					}
				}
			}
		})
	}
}

// drainFoldableSteer feeds the Inbox of a turn being built. A message the user
// explicitly queued must survive it — injecting it into that turn is exactly
// the steer behaviour the queue exists to avoid.
func TestDrainFoldableSteer_LeavesQueuedParked(t *testing.T) {
	srv := &Server{steerQueues: make(map[string][]queuedTurn)}
	const sid = "foldable"

	srv.enqueueSteer(sid, agent.InboxItem{Text: "steer-1"})
	srv.enqueueQueued(sid, agent.InboxItem{Text: "queued-1"})
	srv.enqueueSteer(sid, agent.InboxItem{Text: "steer-2"})

	foldable := srv.drainFoldableSteer(sid)
	if len(foldable) != 2 || foldable[0].Text != "steer-1" || foldable[1].Text != "steer-2" {
		t.Fatalf("foldable = %+v, want the two steers in order", foldable)
	}
	left := srv.drainSteer(sid)
	if len(left) != 1 || left[0].item.Text != "queued-1" || !left[0].standalone {
		t.Fatalf("queue = %+v, want only the standalone queued message", left)
	}
}

// requeueFront must preserve both order and the standalone flag, so a caller
// that consumed only the first batch leaves the rest intact for the turn loop.
func TestRequeueFront_PreservesOrderAndFlags(t *testing.T) {
	srv := &Server{steerQueues: make(map[string][]queuedTurn)}
	const sid = "requeue"

	srv.enqueueSteer(sid, agent.InboxItem{Text: "later"})
	srv.requeueFront(sid, [][]queuedTurn{
		{{item: agent.InboxItem{Text: "q1"}, standalone: true}},
		{{item: agent.InboxItem{Text: "s1"}}, {item: agent.InboxItem{Text: "s2"}}},
	})

	got := srv.drainSteer(sid)
	want := []struct {
		text       string
		standalone bool
	}{{"q1", true}, {"s1", false}, {"s2", false}, {"later", false}}
	if len(got) != len(want) {
		t.Fatalf("queue = %+v, want %d items", got, len(want))
	}
	for i, w := range want {
		if got[i].item.Text != w.text || got[i].standalone != w.standalone {
			t.Errorf("item %d = (%q, %v), want (%q, %v)", i, got[i].item.Text, got[i].standalone, w.text, w.standalone)
		}
	}
}

// A mid-turn message with queue=true must not steer the turn in flight: it runs
// afterwards, as its own turn. Two queued messages therefore produce two
// separate user messages in the transcript — not one folded "q1\n\nq2".
func TestMidTurnQueue_RunsAsSeparateTurns(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]queuedTurn)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	// See steer_probe_test.go: a real title keeps the background title-generation
	// goroutine from racing midTurnSender's first-call detection.
	sess.Title = "queue probe"
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	srv.sender = &midTurnSender{inject: func() {
		for _, text := range []string{`"q1"`, `"q2"`} {
			srv.handleWSUserMessage(nil, &wsMsgUserMessage{
				SessionID: sess.ID,
				Content:   json.RawMessage(text),
				Queue:     true,
			})
		}
	}}

	srv.turnRunning[sess.ID] = true
	srv.runAgentTurnLoop(sess, "first-msg", nil, nil)

	loaded, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	var userTexts []string
	for _, m := range loaded.Messages {
		if m.Role != agent.RoleUser {
			continue
		}
		if m.Content != "" {
			userTexts = append(userTexts, m.Content)
		}
		for _, b := range m.Blocks {
			if b.Type == "text" && b.Text != "" {
				userTexts = append(userTexts, b.Text)
			}
		}
	}
	counts := map[string]int{}
	for _, txt := range userTexts {
		counts[txt]++
	}
	for _, want := range []string{"first-msg", "q1", "q2"} {
		if counts[want] != 1 {
			t.Errorf("user message %q appears %d times, want 1 (all: %q)", want, counts[want], userTexts)
		}
	}
	if counts["q1\n\nq2"] != 0 {
		t.Errorf("queued messages were folded into one turn (all: %q)", userTexts)
	}
	// Order is the whole point: they must run in the order the user sent them.
	if got := indexOf(userTexts, "q1"); got > indexOf(userTexts, "q2") {
		t.Errorf("q1 ran after q2 (transcript: %q)", userTexts)
	}
	if leftover := srv.drainSteer(sess.ID); len(leftover) != 0 {
		t.Errorf("steer queue = %+v, want drained", leftover)
	}
}

func indexOf(hay []string, needle string) int {
	for i, s := range hay {
		if s == needle {
			return i
		}
	}
	return -1
}

// A steer left undrained when its turn ends belongs to that turn — it must run
// BEFORE a message the user queued behind it. The teardown used to append it to
// the queue tail, which reversed the two as soon as queueing gave the queue's
// order any meaning: the user saw the later message answered first.
//
// The single-round stub is what makes this reachable — runLoop drains the inbox
// at the START of an iteration, so a steer arriving during the only round is
// still there at teardown.
func TestTurnTeardown_UndrainedSteerRunsBeforeQueued(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]queuedTurn)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	sess.Title = "teardown order probe"
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	srv.sender = &midTurnSender{inject: func() {
		// Steer first, then queue — the order the user typed them.
		srv.handleWSUserMessage(nil, &wsMsgUserMessage{
			SessionID: sess.ID,
			Content:   json.RawMessage(`"steered"`),
		})
		srv.handleWSUserMessage(nil, &wsMsgUserMessage{
			SessionID: sess.ID,
			Content:   json.RawMessage(`"queued"`),
			Queue:     true,
		})
	}}

	srv.turnRunning[sess.ID] = true
	srv.runAgentTurnLoop(sess, "first-msg", nil, nil)

	loaded, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	var userTexts []string
	for _, m := range loaded.Messages {
		if m.Role != agent.RoleUser {
			continue
		}
		if m.Content != "" {
			userTexts = append(userTexts, m.Content)
		}
		for _, b := range m.Blocks {
			if b.Type == "text" && b.Text != "" {
				userTexts = append(userTexts, b.Text)
			}
		}
	}
	si, qi := indexOf(userTexts, "steered"), indexOf(userTexts, "queued")
	if si < 0 || qi < 0 {
		t.Fatalf("both messages must run, got transcript %q", userTexts)
	}
	if si > qi {
		t.Errorf("the undrained steer ran after the queued message (transcript: %q)", userTexts)
	}
}

// While a queued message waits for its turn it must stay in the queue, so the
// user can still retract it. Holding not-yet-run batches in a local made the
// retract report "already sent" for a message that had not run yet.
func TestQueuedMessage_RetractableWhileWaiting(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	srv.initWS()

	const sid = "retract-queued"
	conn := subscribedConn(t, srv, sid)

	srv.enqueueQueued(sid, agent.InboxItem{Text: "q1"}, agent.InboxItem{Text: "q2"})
	// The turn loop takes the first batch and puts the rest back.
	batches := batchQueuedTurns(srv.drainSteer(sid))
	srv.requeueFront(sid, batches[1:])

	srv.handleWSRetractSteer(sid, "pending-q2", "q2")

	ev := nextEvent(t, conn)
	if ev["type"] != "steer_retracted" {
		t.Fatalf("type = %v, want steer_retracted (a waiting queued message is retractable)", ev["type"])
	}
	if leftover := srv.drainSteer(sid); len(leftover) != 0 {
		t.Errorf("queue = %+v, want empty after retracting the only waiting item", leftover)
	}
}

// The idle kick is the safety net for a message queued in the window between
// the turn loop's last drain and turnRunning clearing — nothing else would pick
// it up. It must also honour the batching: a steer ahead of a queued message
// runs first, as its own turn.
func TestKickIdleSteerTurn_RunsQueuedAsSeparateTurns(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]queuedTurn)
	srv.sessionAgents = make(map[string]*agent.Agent)
	srv.sender = &midTurnSender{}

	sess := agent.NewSession("stub-model", "")
	sess.Title = "idle kick probe"
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	srv.enqueueSteer(sess.ID, agent.InboxItem{Text: "late-steer"})
	srv.enqueueQueued(sess.ID, agent.InboxItem{Text: "late-queued"})

	if !srv.kickIdleSteerTurn(sess.ID) {
		t.Fatal("kickIdleSteerTurn returned false, want a turn started")
	}

	mu := srv.sessionTurnLock(sess.ID)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		running := srv.turnRunning[sess.ID]
		mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("turn never wound down")
		}
		time.Sleep(10 * time.Millisecond)
	}

	loaded, err := agent.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	var userTexts []string
	for _, m := range loaded.Messages {
		if m.Role == agent.RoleUser && m.Content != "" {
			userTexts = append(userTexts, m.Content)
		}
	}
	for _, want := range []string{"late-steer", "late-queued"} {
		if indexOf(userTexts, want) < 0 {
			t.Errorf("%q never ran (transcript: %q)", want, userTexts)
		}
	}
	if indexOf(userTexts, "late-steer") > indexOf(userTexts, "late-queued") {
		t.Errorf("steer ran after the queued message (transcript: %q)", userTexts)
	}
	for _, txt := range userTexts {
		if txt == "late-steer\n\nlate-queued" {
			t.Errorf("the queued message was folded with the steer (transcript: %q)", userTexts)
		}
	}
}

// Wire contract: the exact JSON web ws.ts sends for a queued message must route
// through dispatch onto wsMsgUserMessage.Queue and park the message instead of
// feeding the running Agent's Inbox. Guards against a json-tag drift.
func TestUserMessage_QueueWireContract(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	srv.initWS()
	srv.turnRunning = make(map[string]bool)
	srv.steerQueues = make(map[string][]queuedTurn)
	srv.sessionAgents = make(map[string]*agent.Agent)

	sess := agent.NewSession("stub-model", "")
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	a := agent.New(&recordingSender{}, "stub-model")
	srv.sessionAgentsMu.Lock()
	srv.sessionAgents[sess.ID] = a
	srv.sessionAgentsMu.Unlock()
	srv.turnRunning[sess.ID] = true

	conn := subscribedConn(t, srv, sess.ID)
	raw, err := json.Marshal(map[string]any{
		"type":       "user_message",
		"session_id": sess.ID,
		"content":    "run this next",
		"queue":      true,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	conn.dispatch("user_message", raw)

	if a.Inbox.HasPending() {
		t.Error("a queued message must not reach the running turn's Inbox")
	}
	parked := srv.drainSteer(sess.ID)
	if len(parked) != 1 || parked[0].item.Text != "run this next" || !parked[0].standalone {
		t.Fatalf("queue = %+v, want one standalone entry", parked)
	}
}
