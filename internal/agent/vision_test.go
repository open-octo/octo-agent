package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeDescriber records calls and replays scripted answers.
type fakeDescriber struct {
	active bool
	calls  int
	err    error
	reply  string
}

func (f *fakeDescriber) Active() bool { return f.active }

func (f *fakeDescriber) Describe(context.Context, ImageData) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	if f.reply != "" {
		return f.reply, nil
	}
	return "a cat sitting on a keyboard", nil
}

// imageAgent builds an agent whose history holds one user message with a text
// block and an image block.
func imageAgent(t *testing.T, d ImageDescriber) *Agent {
	t.Helper()
	a := New(nil, "text-only-model")
	a.History.Append(Message{Role: RoleUser, Blocks: []ContentBlock{
		NewTextBlock("what is this?"),
		{Type: "image", Image: &ImageData{MIMEType: "image/png", Data: []byte{1, 2, 3}}, ImagePath: "/tmp/shot.png"},
	}})
	a.SetImageDescriber(d)
	return a
}

func imageBlocksOf(msgs []Message) int {
	n := 0
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == "image" {
				n++
			}
		}
	}
	return n
}

func TestDescribeImagesReplacesSnapshotButKeepsHistory(t *testing.T) {
	d := &fakeDescriber{active: true}
	a := imageAgent(t, d)

	msgs := a.History.Snapshot()
	a.describeImages(context.Background(), nil, msgs)

	if d.calls != 1 {
		t.Fatalf("describer calls = %d, want 1", d.calls)
	}
	if got := imageBlocksOf(msgs); got != 0 {
		t.Errorf("snapshot still has %d image block(s); the transform should have replaced them", got)
	}
	text := msgs[0].Blocks[1].Text
	if !strings.Contains(text, "a cat sitting on a keyboard") {
		t.Errorf("snapshot text %q does not carry the description", text)
	}
	if !strings.Contains(text, "shot.png") {
		t.Errorf("snapshot text %q should name the image", text)
	}

	// The critical invariant: Snapshot copies the []Message but not each
	// message's Blocks slice, so a careless in-place write would delete the
	// image from history — breaking the UI, session replay, and any later
	// switch back to a vision model.
	hist := a.History.Snapshot()
	if got := imageBlocksOf(hist); got != 1 {
		t.Fatalf("history has %d image block(s), want 1 — the transform leaked into history", got)
	}
	if hist[0].Blocks[1].ImageDescription != "a cat sitting on a keyboard" {
		t.Errorf("description was not cached onto the history block, got %q", hist[0].Blocks[1].ImageDescription)
	}
	if hist[0].Blocks[1].Image == nil {
		t.Error("history block lost its image bytes")
	}
}

func TestDescribeImagesCachesAcrossTurns(t *testing.T) {
	d := &fakeDescriber{active: true}
	a := imageAgent(t, d)

	for i := 0; i < 3; i++ {
		msgs := a.History.Snapshot()
		a.describeImages(context.Background(), nil, msgs)
		if got := imageBlocksOf(msgs); got != 0 {
			t.Fatalf("turn %d: image block survived into the snapshot", i)
		}
	}
	if d.calls != 1 {
		t.Errorf("describer called %d times across 3 turns, want 1 (the block is the cache)", d.calls)
	}
}

func TestDescribeImagesSkippedWhenModelHasVision(t *testing.T) {
	d := &fakeDescriber{active: false}
	a := imageAgent(t, d)

	msgs := a.History.Snapshot()
	a.describeImages(context.Background(), nil, msgs)

	if d.calls != 0 {
		t.Errorf("describer called %d times, want 0 — the model can see images itself", d.calls)
	}
	if got := imageBlocksOf(msgs); got != 1 {
		t.Errorf("snapshot has %d image block(s), want the original 1 untouched", got)
	}
}

// A vision model must get the real image even when an earlier text-only turn
// already cached a description on the block.
func TestDescribeImagesCachedDescriptionDoesNotShadowVisionModel(t *testing.T) {
	d := &fakeDescriber{active: true}
	a := imageAgent(t, d)
	msgs := a.History.Snapshot()
	a.describeImages(context.Background(), nil, msgs)

	// /model switch to something with vision.
	d.active = false
	msgs = a.History.Snapshot()
	a.describeImages(context.Background(), nil, msgs)

	if got := imageBlocksOf(msgs); got != 1 {
		t.Errorf("snapshot has %d image block(s), want 1 — a vision model should receive the image, not its description", got)
	}
}

func TestDescribeImagesNilDescriberIsNoop(t *testing.T) {
	a := imageAgent(t, nil)
	msgs := a.History.Snapshot()
	a.describeImages(context.Background(), nil, msgs)
	if got := imageBlocksOf(msgs); got != 1 {
		t.Errorf("snapshot has %d image block(s), want 1 untouched", got)
	}
}

func TestDescribeImagesFailureBudget(t *testing.T) {
	d := &fakeDescriber{active: true, err: errors.New("endpoint down")}
	a := imageAgent(t, d)

	for i := 0; i < 4; i++ {
		msgs := a.History.Snapshot()
		a.describeImages(context.Background(), nil, msgs)
		text := msgs[0].Blocks[1].Text
		if !strings.Contains(text, "image description unavailable") {
			t.Fatalf("turn %d: want the fallback line, got %q", i, text)
		}
		if !strings.Contains(text, "Do not guess") {
			t.Errorf("turn %d: fallback must forbid guessing, got %q", i, text)
		}
	}
	if d.calls != visionHelperMaxFailures {
		t.Errorf("describer called %d times, want %d — the budget should stop further calls", d.calls, visionHelperMaxFailures)
	}

	hist := a.History.Snapshot()
	if hist[0].Blocks[1].ImageDescription != "" {
		t.Errorf("a failure must not be cached as a description, got %q", hist[0].Blocks[1].ImageDescription)
	}
	if hist[0].Blocks[1].ImageDescFailures != visionHelperMaxFailures {
		t.Errorf("failure count = %d, want %d", hist[0].Blocks[1].ImageDescFailures, visionHelperMaxFailures)
	}
	if !strings.Contains(msgs4Reason(a), "failed repeatedly") {
		t.Error("once exhausted, the fallback should say the budget ran out")
	}
}

// msgs4Reason renders the fallback text of a budget-exhausted block.
func msgs4Reason(a *Agent) string {
	msgs := a.History.Snapshot()
	a.describeImages(context.Background(), nil, msgs)
	return msgs[0].Blocks[1].Text
}

func TestDescribeImagesFailureThenSuccessClearsCount(t *testing.T) {
	d := &fakeDescriber{active: true, err: errors.New("blip")}
	a := imageAgent(t, d)

	msgs := a.History.Snapshot()
	a.describeImages(context.Background(), nil, msgs)
	if a.History.Snapshot()[0].Blocks[1].ImageDescFailures != 1 {
		t.Fatal("first failure should charge the budget")
	}

	d.err = nil
	msgs = a.History.Snapshot()
	a.describeImages(context.Background(), nil, msgs)

	b := a.History.Snapshot()[0].Blocks[1]
	if b.ImageDescFailures != 0 {
		t.Errorf("failure count = %d, want 0 after a success", b.ImageDescFailures)
	}
	if b.ImageDescription == "" {
		t.Error("success should cache a description")
	}
}

func TestDescribeImagesEventsAndOrdering(t *testing.T) {
	d := &fakeDescriber{active: true}
	a := New(nil, "text-only-model")
	a.History.Append(Message{Role: RoleUser, Blocks: []ContentBlock{
		{Type: "image", Image: &ImageData{MIMEType: "image/png", Data: []byte{1}}, ImagePath: "/tmp/a.png"},
		{Type: "image", Image: &ImageData{MIMEType: "image/png", Data: []byte{2}}, ImagePath: "/tmp/b.png"},
	}})
	a.SetImageDescriber(d)

	var events []AgentEvent
	msgs := a.History.Snapshot()
	a.describeImages(context.Background(), func(e AgentEvent) {
		if e.Kind == EventImageDescribing {
			events = append(events, e)
		}
	}, msgs)

	if len(events) != 4 {
		t.Fatalf("got %d events, want 4 (started+done per image): %+v", len(events), events)
	}
	want := []struct {
		name, status string
		index, total int
	}{
		{"a.png", "started", 1, 2},
		{"a.png", "done", 1, 2},
		{"b.png", "started", 2, 2},
		{"b.png", "done", 2, 2},
	}
	for i, w := range want {
		e := events[i]
		if e.ImageName != w.name || e.ImageStatus != w.status || e.ImageIndex != w.index || e.ImageTotal != w.total {
			t.Errorf("event %d = {%s %s %d/%d}, want {%s %s %d/%d}",
				i, e.ImageName, e.ImageStatus, e.ImageIndex, e.ImageTotal, w.name, w.status, w.index, w.total)
		}
	}
}

func TestDescribeImagesFailedEventCarriesReason(t *testing.T) {
	d := &fakeDescriber{active: true, err: errors.New("401 unauthorized")}
	a := imageAgent(t, d)

	var failed *AgentEvent
	msgs := a.History.Snapshot()
	a.describeImages(context.Background(), func(e AgentEvent) {
		if e.Kind == EventImageDescribing && e.ImageStatus == "failed" {
			cp := e
			failed = &cp
		}
	}, msgs)

	if failed == nil {
		t.Fatal("no failed event emitted")
	}
	if !strings.Contains(failed.Err, "401") {
		t.Errorf("failed event Err = %q, want the endpoint reason", failed.Err)
	}
}

// A block with no bytes can't be described; the transform must leave it for
// the provider layer rather than calling the helper with nothing.
func TestDescribeImagesSkipsBlocksWithoutBytes(t *testing.T) {
	d := &fakeDescriber{active: true}
	a := New(nil, "text-only-model")
	a.History.Append(Message{Role: RoleUser, Blocks: []ContentBlock{
		{Type: "image", ImagePath: "/tmp/gone.png"},
	}})
	a.SetImageDescriber(d)

	msgs := a.History.Snapshot()
	a.describeImages(context.Background(), nil, msgs)

	if d.calls != 0 {
		t.Errorf("describer called %d times for a block with no bytes, want 0", d.calls)
	}
}

func TestImageBlockNameFallsBackWhenPathless(t *testing.T) {
	if got := imageBlockName(ContentBlock{Type: "image"}); got != "image" {
		t.Errorf("imageBlockName() = %q, want %q for a tool-produced block", got, "image")
	}
	if got := imageBlockName(ContentBlock{Type: "image", ImagePath: "/a/b/shot.png"}); got != "shot.png" {
		t.Errorf("imageBlockName() = %q, want %q", got, "shot.png")
	}
}

func TestUpdateMessageMarksRewrittenAndBoundsCheck(t *testing.T) {
	h := NewHistory()
	h.Append(NewUserMessage("hi"))
	h.takeRewriteDirty() // clear whatever Append left

	h.UpdateMessage(0, func(m *Message) { m.Content = "edited" })
	if !h.RewriteDirty() {
		t.Error("UpdateMessage should mark history rewritten")
	}
	if h.Snapshot()[0].Content != "edited" {
		t.Error("UpdateMessage did not apply the mutation")
	}

	// Out of range must not panic.
	h.UpdateMessage(99, func(m *Message) { m.Content = "nope" })
	h.UpdateMessage(-1, func(m *Message) { m.Content = "nope" })
	if h.Len() != 1 {
		t.Errorf("history length changed to %d", h.Len())
	}
}
