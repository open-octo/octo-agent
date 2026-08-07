package agent

import (
	"context"
	"fmt"
	"path/filepath"
)

// visionHelperMaxFailures is how many consecutive failures one image block
// tolerates before it stops calling the vision helper for the rest of the
// session. Two attempts distinguish a blip from a broken endpoint; beyond that
// a dead helper would cost a timeout per image per turn for nothing.
// LoadSession clears the count, so fixing the endpoint and resuming works.
const visionHelperMaxFailures = 2

// ImageDescriber renders images as text so a model that can't accept image
// input still learns what an image contains. app.NewVisionDescriber builds the
// real one; the agent only knows this interface.
type ImageDescriber interface {
	// Active reports whether descriptions are needed right now — false when
	// the primary model can see images for itself, in which case image blocks
	// travel to the provider untouched. Consulted once per turn rather than
	// captured at construction, so a /model switch takes effect immediately.
	Active() bool

	// Describe returns a text rendering of one image. The returned string goes
	// into the conversation verbatim (wrapped in a short attribution line), so
	// it should read as prose or structured text, not as a raw API envelope.
	Describe(ctx context.Context, img ImageData) (string, error)
}

// describeImages rewrites the outgoing snapshot so image blocks a text-only
// model can't read become text it can. History keeps the image blocks: the UI
// renders them, a later /model switch to a vision model re-sends them as
// images, and a successful description is cached onto the original block so
// the helper runs once per image, not once per turn.
//
// Failure never breaks the turn — the model gets a fallback line telling it an
// image exists and not to guess at its contents.
func (a *Agent) describeImages(ctx context.Context, handler EventHandler, msgs []Message) {
	d := a.getImageDescriber()
	if d == nil || !d.Active() {
		return
	}

	// Count first so the events can say "1/3". Only images that actually reach
	// the helper are counted: a cache hit costs nothing and has no wait worth
	// narrating.
	total := 0
	for mi := range msgs {
		for bi := range msgs[mi].Blocks {
			if needsVisionHelper(msgs[mi].Blocks[bi]) {
				total++
			}
		}
	}

	// cloned tracks which snapshot messages already have a private Blocks
	// array. Snapshot copies the []Message but not each Message's Blocks
	// slice, so the two share a backing array — writing a text block straight
	// into the snapshot would delete the image from history itself.
	cloned := make(map[int]bool)
	index := 0

	for mi := range msgs {
		for bi := range msgs[mi].Blocks {
			b := msgs[mi].Blocks[bi]
			if b.Type != "image" {
				continue
			}

			switch {
			case b.ImageDescription != "":
				replaceBlock(msgs, cloned, mi, bi, describedImageText(b, b.ImageDescription))

			case b.Image == nil:
				// No bytes to send (a session whose on-disk copy vanished).
				// LoadSession already turned these into text; anything left
				// here is not describable, so leave it for the provider layer.
				continue

			case b.ImageDescFailures >= visionHelperMaxFailures:
				replaceBlock(msgs, cloned, mi, bi, imageDescFallback(b, "the vision helper failed repeatedly earlier in this session"))

			default:
				index++
				name := imageBlockName(b)
				emitImageDescribing(handler, name, index, total, "started", "")

				desc, err := d.Describe(ctx, *b.Image)
				if err != nil {
					emitImageDescribing(handler, name, index, total, "failed", err.Error())
					bumpImageDescFailures(a.History, mi, bi)
					replaceBlock(msgs, cloned, mi, bi, imageDescFallback(b, err.Error()))
					continue
				}

				emitImageDescribing(handler, name, index, total, "done", "")
				cacheImageDescription(a.History, mi, bi, desc)
				replaceBlock(msgs, cloned, mi, bi, describedImageText(b, desc))
			}
		}
	}
}

// needsVisionHelper reports whether this block would trigger a helper call.
func needsVisionHelper(b ContentBlock) bool {
	return b.Type == "image" &&
		b.Image != nil &&
		b.ImageDescription == "" &&
		b.ImageDescFailures < visionHelperMaxFailures
}

// replaceBlock swaps the snapshot's block for a text block, giving the message
// a private Blocks array first so history keeps its image block.
func replaceBlock(msgs []Message, cloned map[int]bool, mi, bi int, text string) {
	if !cloned[mi] {
		private := make([]ContentBlock, len(msgs[mi].Blocks))
		copy(private, msgs[mi].Blocks)
		msgs[mi].Blocks = private
		cloned[mi] = true
	}
	msgs[mi].Blocks[bi] = NewTextBlock(text)
}

// cacheImageDescription stores a successful description on the original block
// so later turns reuse it instead of paying for the helper again.
func cacheImageDescription(h *History, mi, bi int, desc string) {
	h.UpdateMessage(mi, func(m *Message) {
		if bi >= len(m.Blocks) || m.Blocks[bi].Type != "image" {
			return
		}
		m.Blocks[bi].ImageDescription = desc
		m.Blocks[bi].ImageDescFailures = 0
	})
}

// bumpImageDescFailures charges one failure against this block's retry budget.
func bumpImageDescFailures(h *History, mi, bi int) {
	h.UpdateMessage(mi, func(m *Message) {
		if bi >= len(m.Blocks) || m.Blocks[bi].Type != "image" {
			return
		}
		m.Blocks[bi].ImageDescFailures++
	})
}

// imageBlockName is the human label for an image block: the file's basename
// when there's an on-disk copy, else a generic word. Tool-produced images
// (read_file, browser screenshots, MCP results) carry no path.
func imageBlockName(b ContentBlock) string {
	if b.ImagePath != "" {
		return filepath.Base(b.ImagePath)
	}
	return "image"
}

// describedImageText wraps a description with attribution, so the model treats
// it as a rendering of an image rather than as something the user typed.
func describedImageText(b ContentBlock, desc string) string {
	return fmt.Sprintf("[Image: %s — the active model cannot view images, so a vision helper described it:]\n%s", imageBlockName(b), desc)
}

// imageDescFallback is what the model sees when an image could not be
// described. It names the failure so the model can relay a fix to the user,
// and forbids guessing — the whole reason images were refused before this
// feature existed.
func imageDescFallback(b ContentBlock, reason string) string {
	return fmt.Sprintf("[image description unavailable — %s; the active model cannot view images and the vision helper failed (%s). Do not guess what the image shows.]", imageBlockName(b), reason)
}

// emitImageDescribing sends one EventImageDescribing, if anyone is listening.
func emitImageDescribing(handler EventHandler, name string, index, total int, status, errMsg string) {
	if handler == nil {
		return
	}
	handler(AgentEvent{
		Kind:        EventImageDescribing,
		ImageName:   name,
		ImageIndex:  index,
		ImageTotal:  total,
		ImageStatus: status,
		Err:         errMsg,
	})
}
