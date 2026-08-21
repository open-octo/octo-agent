package server

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/channel"
	"github.com/open-octo/octo-agent/internal/tools"
)

func TestIsAffirmative(t *testing.T) {
	yes := []string{"yes", "y", "OK", " ok ", "Allow", "是", "可以", "同意", "允许", "YES"}
	for _, s := range yes {
		if !isAffirmative(s) {
			t.Errorf("isAffirmative(%q) = false, want true", s)
		}
	}
	no := []string{"no", "n", "deny", "不", "稍等", "yes please", "", "  ", "okay?"}
	for _, s := range no {
		if isAffirmative(s) {
			t.Errorf("isAffirmative(%q) = true, want false", s)
		}
	}
}

func askEnv(t *testing.T) (*Server, *channel.Session, *drainTestAdapter, channel.InboundEvent) {
	t.Helper()
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	sess := &channel.Session{}
	ad := &drainTestAdapter{}
	ev := channel.InboundEvent{ChatID: "c1", MessageID: "m1", Text: "original"}
	return srv, sess, ad, ev
}

func TestChannelPermissionAsk_AffirmativeAllows(t *testing.T) {
	srv, sess, ad, ev := askEnv(t)
	ask := srv.channelPermissionAsk(sess, ad, ev)

	done := make(chan struct{})
	var allow, remember bool
	var err error
	go func() {
		allow, remember, err = ask(context.Background(), "terminal", map[string]any{"command": "sudo ls"})
		close(done)
	}()

	// Wait until the prompt was sent and the ask slot is armed.
	waitFor(t, func() bool { return len(ad.texts()) == 1 })
	if !strings.Contains(ad.texts()[0], "terminal") {
		t.Errorf("prompt %q should name the tool", ad.texts()[0])
	}
	if !strings.Contains(ad.texts()[0], "sudo ls") {
		t.Errorf("prompt %q must show the input being approved", ad.texts()[0])
	}
	if !sess.DeliverAskReply("c1", "", "允许") {
		t.Fatal("ask slot not armed when the prompt was already sent")
	}

	<-done
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if !allow {
		t.Error("affirmative reply should allow")
	}
	if remember {
		t.Error("IM approvals must be one-shot (remember=false)")
	}
}

func TestChannelPermissionAsk_NonAffirmativeDenies(t *testing.T) {
	srv, sess, ad, ev := askEnv(t)
	ask := srv.channelPermissionAsk(sess, ad, ev)

	done := make(chan struct{})
	var allow bool
	go func() {
		allow, _, _ = ask(context.Background(), "terminal", nil)
		close(done)
	}()
	waitFor(t, func() bool { return len(ad.texts()) == 1 })
	sess.DeliverAskReply("c1", "", "先等等，我看看")
	<-done

	if allow {
		t.Error("non-affirmative reply must deny")
	}
}

// TestChannelPermissionAsk_AffirmativeWithAttachmentNote: the dispatcher
// folds "[Attached file: …]" notes into replies that carry files; an
// approving word accompanied by a screenshot must still approve.
func TestChannelPermissionAsk_AffirmativeWithAttachmentNote(t *testing.T) {
	srv, sess, ad, ev := askEnv(t)
	ask := srv.channelPermissionAsk(sess, ad, ev)

	done := make(chan struct{})
	var allow bool
	go func() {
		allow, _, _ = ask(context.Background(), "terminal", nil)
		close(done)
	}()
	waitFor(t, func() bool { return len(ad.texts()) == 1 })
	sess.DeliverAskReply("c1", "", "允许\n\n[Attached file: /tmp/shot.png]")
	<-done

	if !allow {
		t.Error("affirmative reply with an attachment note must allow")
	}
}

func TestChannelPermissionAsk_ContextCancelDenies(t *testing.T) {
	srv, sess, ad, ev := askEnv(t)
	ask := srv.channelPermissionAsk(sess, ad, ev)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var allow bool
	var err error
	go func() {
		allow, _, err = ask(ctx, "terminal", nil)
		close(done)
	}()
	waitFor(t, func() bool { return len(ad.texts()) == 1 })
	cancel() // the /stop path cancels the turn ctx

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ask did not return on context cancellation")
	}
	if allow || err == nil {
		t.Errorf("cancelled ask: allow=%v err=%v, want deny with error", allow, err)
	}
	if sess.DeliverAskReply("c1", "", "yes") {
		t.Error("ask slot must be released after cancellation")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}

// askQ builds one question with the given labels.
func askQ(multi bool, labels ...string) tools.AskQuestion {
	opts := make([]tools.AskOption, 0, len(labels))
	for _, l := range labels {
		opts = append(opts, tools.AskOption{Label: l})
	}
	return tools.AskQuestion{Question: "q", Header: "h", MultiSelect: multi, Options: opts}
}

func askOne(q tools.AskQuestion) tools.AskRequest {
	return tools.AskRequest{Questions: []tools.AskQuestion{q}}
}

func TestParseAskReply(t *testing.T) {
	q := askQ(false, "Alpha", "Beta", "Gamma")
	multi := askQ(true, "Alpha", "Beta", "Gamma")

	cases := []struct {
		q       tools.AskQuestion
		text    string
		choices []string
		custom  string
		outcome tools.AskOutcome
	}{
		{q, "2", []string{"Beta"}, "", tools.AskSubmitted},
		{q, " 1 ", []string{"Alpha"}, "", tools.AskSubmitted},
		{q, "beta", []string{"Beta"}, "", tools.AskSubmitted},        // label match, case-insensitive
		{q, "do it my way", nil, "do it my way", tools.AskSubmitted}, // free text
		{q, "9", nil, "9", tools.AskSubmitted},                       // out of range → custom text
		{q, "", nil, "", tools.AskRejected},                          // empty reply cancels the set
		{q, "4", nil, "", tools.AskClarify},                          // the tail row: "Chat about this"
		{multi, "1,3", []string{"Alpha", "Gamma"}, "", tools.AskSubmitted},
		{multi, "1、3", []string{"Alpha", "Gamma"}, "", tools.AskSubmitted},
	}
	for _, c := range cases {
		got, outcome := parseAskReply(c.text, c.q)
		if outcome != c.outcome {
			t.Errorf("parse(%q) outcome = %v, want %v", c.text, outcome, c.outcome)
			continue
		}
		if strings.Join(got.Choices, "|") != strings.Join(c.choices, "|") || got.Custom != c.custom {
			t.Errorf("parse(%q) = choices %v custom %q, want %v %q", c.text, got.Choices, got.Custom, c.choices, c.custom)
		}
	}
}

// A set is asked one message per question, and each reply fills its own slot.
func TestChannelAsker_AsksEachQuestionInTurn(t *testing.T) {
	srv, sess, ad, ev := askEnv(t)
	asker := srv.channelAsker(sess, ad, ev)

	req := tools.AskRequest{Questions: []tools.AskQuestion{
		askQ(false, "staging", "production"),
		askQ(false, "now", "later"),
	}}
	req.Questions[0].Question = "Deploy where?"
	req.Questions[1].Question = "Deploy when?"

	done := make(chan tools.AskResponse, 1)
	go func() {
		res, _ := asker.Ask(context.Background(), req)
		done <- res
	}()

	waitFor(t, func() bool { return len(ad.texts()) == 1 })
	if first := ad.texts()[0]; !strings.Contains(first, "(1/2)") || !strings.Contains(first, "Deploy where?") {
		t.Errorf("first prompt %q must show progress and the first question", first)
	}
	if !sess.DeliverAskReply("c1", "", "2") {
		t.Fatal("ask slot not armed for the first question")
	}
	waitFor(t, func() bool { return len(ad.texts()) == 2 })
	if second := ad.texts()[1]; !strings.Contains(second, "Deploy when?") {
		t.Errorf("second prompt %q must show the second question", second)
	}
	waitFor(t, func() bool { return sess.DeliverAskReply("c1", "", "1") })

	res := <-done
	if res.Outcome != tools.AskSubmitted || len(res.Answers) != 2 {
		t.Fatalf("res = %+v, want two submitted answers", res)
	}
	if got := res.Answers[0].Choices; len(got) != 1 || got[0] != "production" {
		t.Errorf("answer 0 = %v, want [production]", got)
	}
	if got := res.Answers[1].Choices; len(got) != 1 || got[0] != "now" {
		t.Errorf("answer 1 = %v, want [now]", got)
	}
}

// "Chat about this" ends the set immediately, carrying what was answered so
// far — no extra round-trip, because clarify resolves the call.
func TestChannelAsker_ClarifyEndsSetWithPartialAnswers(t *testing.T) {
	srv, sess, ad, ev := askEnv(t)
	asker := srv.channelAsker(sess, ad, ev)

	req := tools.AskRequest{Questions: []tools.AskQuestion{
		askQ(false, "staging", "production"),
		askQ(false, "now", "later"),
	}}
	done := make(chan tools.AskResponse, 1)
	go func() {
		res, _ := asker.Ask(context.Background(), req)
		done <- res
	}()

	waitFor(t, func() bool { return len(ad.texts()) == 1 })
	if !sess.DeliverAskReply("c1", "", "1") {
		t.Fatal("ask slot not armed")
	}
	waitFor(t, func() bool { return len(ad.texts()) == 2 })
	// 3 = the "Chat about this" row for a two-option question.
	waitFor(t, func() bool { return sess.DeliverAskReply("c1", "", "3") })

	res := <-done
	if res.Outcome != tools.AskClarify {
		t.Fatalf("outcome = %v, want clarify", res.Outcome)
	}
	if got := res.Answers[0].Choices; len(got) != 1 || got[0] != "staging" {
		t.Errorf("answer 0 = %v, want the answer given before clarifying", got)
	}
}

// Previews and notes never reach a chat timeline: they exist for a
// side-by-side comparison a timeline can't render.
func TestRenderChatQuestion_OmitsPreview(t *testing.T) {
	q := askQ(false, "A", "B")
	q.Options[0].Description = "the first one"
	q.Options[0].Preview = "SECRET-PREVIEW-BODY"
	out := renderChatQuestion(q, 0, 1)

	if strings.Contains(out, "SECRET-PREVIEW-BODY") {
		t.Errorf("prompt %q must not carry a preview", out)
	}
	if !strings.Contains(out, "1. A — the first one") {
		t.Errorf("prompt %q must carry the description inline", out)
	}
	if !strings.Contains(out, "3. Chat about this") {
		t.Errorf("prompt %q must offer the clarify row", out)
	}
	if strings.Contains(out, "Other") {
		t.Errorf("prompt %q must not list Other — free text already is the reply", out)
	}
}

func TestChannelAsker_NumberPicksOption(t *testing.T) {
	srv, sess, ad, ev := askEnv(t)
	asker := srv.channelAsker(sess, ad, ev)

	q := askQ(false, "staging", "production")
	q.Question = "Deploy where?"

	done := make(chan tools.AskResponse, 1)
	go func() {
		res, _ := asker.Ask(context.Background(), askOne(q))
		done <- res
	}()
	waitFor(t, func() bool { return len(ad.texts()) == 1 })
	prompt := ad.texts()[0]
	if !strings.Contains(prompt, "Deploy where?") || !strings.Contains(prompt, "1. staging") {
		t.Errorf("prompt %q must show the question and numbered options", prompt)
	}
	if !sess.DeliverAskReply("c1", "", "2") {
		t.Fatal("ask slot not armed")
	}
	res := <-done
	if len(res.Answers) != 1 || len(res.Answers[0].Choices) != 1 || res.Answers[0].Choices[0] != "production" {
		t.Errorf("answers = %+v, want one answer of [production]", res.Answers)
	}
}

// ask_user_question waits forever for an attended reply — unlike
// channelPermissionAsk (fail-closed safety default), a clarifying question
// must not silently give up on a user who stepped away. Only an explicit
// context cancellation (e.g. the turn was interrupted) ends the wait.
func TestChannelAsker_ContextCancelReturnsCancelled(t *testing.T) {
	srv, sess, ad, ev := askEnv(t)
	_ = sess
	_ = ad
	asker := srv.channelAsker(sess, ad, ev)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan tools.AskResponse, 1)
	go func() {
		res, _ := asker.Ask(ctx, askOne(askQ(false, "a", "b")))
		done <- res
	}()
	waitFor(t, func() bool { return len(ad.texts()) == 1 })
	cancel()

	select {
	case res := <-done:
		if res.Outcome != tools.AskRejected {
			t.Errorf("outcome = %v, want rejected on context cancellation", res.Outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask did not return on context cancellation")
	}
}

// ─── Button tests ────────────────────────────────────────────────────────────

// fakeButtonAdapter supports buttons and records SendButtons calls.
type fakeButtonAdapter struct {
	channel.Adapter
	mu         sync.Mutex
	sent       []string
	buttonSent bool
}

func (a *fakeButtonAdapter) text() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sent) == 0 {
		return ""
	}
	return a.sent[0]
}

func (a *fakeButtonAdapter) Platform() string { return "button-fake" }
func (a *fakeButtonAdapter) Start(ctx context.Context, _ func(channel.InboundEvent)) error {
	<-ctx.Done()
	return nil
}
func (a *fakeButtonAdapter) Stop() error { return nil }
func (a *fakeButtonAdapter) SendText(chatID, text, replyTo string) channel.SendResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sent = append(a.sent, text)
	return channel.SendResult{OK: true, MessageID: "bt1"}
}
func (a *fakeButtonAdapter) SendFile(chatID, path, name, replyTo string) channel.SendResult {
	return channel.SendResult{OK: true}
}
func (a *fakeButtonAdapter) UpdateMessage(chatID, messageID, text string) bool { return true }
func (a *fakeButtonAdapter) SupportsMessageUpdates() bool                      { return false }
func (a *fakeButtonAdapter) SupportsButtons() bool                             { return true }
func (a *fakeButtonAdapter) SendButtons(chatID, text string, buttons []channel.Button, replyTo string) channel.SendResult {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.buttonSent = true
	a.sent = append(a.sent, text)
	_ = buttons
	return channel.SendResult{OK: true, MessageID: "bb1"}
}
func (a *fakeButtonAdapter) SendTyping(chatID, contextToken string) error   { return nil }
func (a *fakeButtonAdapter) StopTyping(chatID, contextToken string) error   { return nil }
func (a *fakeButtonAdapter) Flush(chatID string)                            {}
func (a *fakeButtonAdapter) ValidateConfig(channel.PlatformConfig) []string { return nil }

func buttonAskEnv(t *testing.T) (*Server, *channel.Session, *fakeButtonAdapter, channel.InboundEvent) {
	t.Helper()
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})
	sess := &channel.Session{}
	ad := &fakeButtonAdapter{}
	ev := channel.InboundEvent{ChatID: "c1", MessageID: "m1", Text: "original"}
	return srv, sess, ad, ev
}

func TestChannelPermissionAsk_ButtonAllow(t *testing.T) {
	srv, sess, ad, ev := buttonAskEnv(t)
	ask := srv.channelPermissionAsk(sess, ad, ev)

	done := make(chan struct{})
	var allow, remember bool
	var err error
	go func() {
		allow, remember, err = ask(context.Background(), "terminal", map[string]any{"command": "ls"})
		close(done)
	}()

	waitFor(t, func() bool { return ad.text() != "" })
	if !strings.Contains(ad.text(), "terminal") {
		t.Errorf("prompt %q should name the tool", ad.text())
	}
	if !ad.buttonSent {
		t.Error("SendButtons was never called")
	}

	if !sess.DeliverAskButton("c1", "", "allow") {
		t.Fatal("ask slot should accept button press")
	}
	<-done
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if !allow {
		t.Error("allow button should allow")
	}
	if remember {
		t.Error("allow button must not set remember")
	}
}

func TestChannelPermissionAsk_ButtonAlways(t *testing.T) {
	srv, sess, ad, ev := buttonAskEnv(t)
	ask := srv.channelPermissionAsk(sess, ad, ev)

	done := make(chan struct{})
	var allow, remember bool
	go func() {
		allow, remember, _ = ask(context.Background(), "terminal", map[string]any{"command": "ls"})
		close(done)
	}()
	waitFor(t, func() bool { return ad.text() != "" })

	if !sess.DeliverAskButton("c1", "", "always") {
		t.Fatal("ask slot should accept button press")
	}
	<-done
	if !allow || !remember {
		t.Error("always button should allow and remember")
	}
}

func TestChannelPermissionAsk_ButtonDeny(t *testing.T) {
	srv, sess, ad, ev := buttonAskEnv(t)
	ask := srv.channelPermissionAsk(sess, ad, ev)

	done := make(chan struct{})
	var allow bool
	go func() {
		allow, _, _ = ask(context.Background(), "terminal", map[string]any{"command": "ls"})
		close(done)
	}()
	waitFor(t, func() bool { return ad.text() != "" })

	if !sess.DeliverAskButton("c1", "", "deny") {
		t.Fatal("ask slot should accept button press")
	}
	<-done
	if allow {
		t.Error("deny button must deny")
	}
}

func TestChannelPermissionAsk_ButtonIgnoresText(t *testing.T) {
	// When buttons are active, a plain text message must NOT be consumed
	// (the ask slot stays armed for a button press).
	srv, sess, ad, ev := buttonAskEnv(t)
	ask := srv.channelPermissionAsk(sess, ad, ev)

	done := make(chan struct{})
	var allow bool
	go func() {
		allow, _, _ = ask(context.Background(), "terminal", map[string]any{"command": "ls"})
		close(done)
	}()
	waitFor(t, func() bool { return ad.text() != "" })

	// DeliverAskReply must return false — text should NOT be consumed.
	if sess.DeliverAskReply("c1", "", "yes") {
		t.Error("text reply must NOT be consumed while buttons are active")
	}
	// The button press must still resolve the ask.
	if !sess.DeliverAskButton("c1", "", "allow") {
		t.Fatal("button press must be consumed after ignored text")
	}
	<-done
	if !allow {
		t.Error("allow button press should allow")
	}
}
