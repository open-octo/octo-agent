package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/app"
	"github.com/open-octo/octo-agent/internal/channel"
	"github.com/open-octo/octo-agent/internal/tools"
)

// imAffirmatives are the only replies that approve an IM permission prompt.
// Anything else denies — over chat, silence and ambiguity must fail closed.
// imAlways additionally remembers the decision for the session ("stop
// asking me about this exact call").
var imAffirmatives = map[string]bool{
	"yes": true, "y": true, "ok": true, "allow": true,
	"是": true, "可以": true, "同意": true, "允许": true,
}

var imAlways = map[string]bool{
	"always": true, "always allow": true, "总是允许": true, "一直允许": true,
}

func isAffirmative(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	return imAffirmatives[t] || imAlways[t]
}

func isAlways(text string) bool {
	return imAlways[strings.ToLower(strings.TrimSpace(text))]
}

// askInputSummary renders the part of toolInput the user is actually
// approving. The IM transport shows no tool cards before the gate fires, so
// without this the user would approve blind — "Allow terminal?" tells them
// nothing about WHICH command. Known primary fields come first; anything
// else falls back to a JSON head. Mirror #1101: budget large enough for
// ~10 lines of content (600 runes) so the user can see the actual command
// instead of a truncated snippet that hides the tail (approve-what-you-can't-
// see concern from #1092/#1105).
func askInputSummary(toolInput map[string]any) string {
	const maxRunes = 600
	for _, key := range []string{"command", "path", "url", "reason", "pattern"} {
		if v, ok := toolInput[key].(string); ok && strings.TrimSpace(v) != "" {
			return truncateForAsk(v, maxRunes)
		}
	}
	if len(toolInput) == 0 {
		return ""
	}
	b, err := json.Marshal(toolInput)
	if err != nil {
		return ""
	}
	return truncateForAsk(string(b), maxRunes)
}

// truncateForAsk truncates s to at most maxRunes runes, never mid-rune.
// Uses rune-aware slicing so multi-byte CJK characters are never split
// (byte-slicing a CJK string mid-rune would produce "�" replacement chars).
func truncateForAsk(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}

// Button IDs used in IM permission ask prompts. When the platform supports
// interactive buttons, these are sent as callback_data / custom_id / button
// value; on text-only platforms the user types the corresponding keyword.
const (
	askButtonAllow  = "allow"
	askButtonAlways = "always"
	askButtonDeny   = "deny"
)

// channelPermissionAsk builds the app.PermissionAsk for one IM turn: it sends
// a confirmation prompt into the chat and consumes the requesting user's next
// plain message or button press as the answer (routed by the inbound dispatcher
// via DeliverAskReply or DeliverAskButton, ahead of the turn path — see
// routeChannelEvent). On platforms with native button support (Telegram,
// Discord, Feishu), buttons are used instead of the next-plain-message contract,
// eliminating the swallowed-message trap (#1120). Approval requires an explicit
// affirmative; the "always" variants also remember the decision in the session's
// Remembered store (exact tool+input signature, session lifetime, never persisted
// to permissions.yml). Any other reply, the turn being cancelled (/stop), or the
// timeout denies.
func (s *Server) channelPermissionAsk(sess *channel.Session, ad channel.Adapter, ev channel.InboundEvent) app.PermissionAsk {
	return func(ctx context.Context, toolName string, toolInput map[string]any) (bool, bool, error) {
		replyCh, release, err := sess.BeginAsk(ev.ChatID, ev.UserID)
		if err != nil {
			return false, false, err
		}
		defer release()

		what := toolName
		if detail := askInputSummary(toolInput); detail != "" {
			what = fmt.Sprintf("%s — %q", toolName, detail)
		}

		useButtons := ad.SupportsButtons()
		if useButtons {
			sess.SetAskButtonsOnly()
			ad.SendButtons(ev.ChatID, fmt.Sprintf("⚠️ Allow %s?", what), []channel.Button{
				{ID: askButtonAllow, Label: "✅ Allow once"},
				{ID: askButtonAlways, Label: "🔄 Always allow"},
				{ID: askButtonDeny, Label: "❌ Deny"},
			}, ev.MessageID)
		} else {
			prompt := fmt.Sprintf(
				"⚠️ Allow %s? Reply yes / 允许 to approve once, always / 总是允许 to stop asking for this exact call — any other reply denies; only the requester's reply counts. (/stop cancels the task.)",
				what)
			ad.SendText(ev.ChatID, prompt, ev.MessageID)
		}

		// No timeout: an IM permission ask fires only in an attended chat, so it
		// waits for a real reply and is released by cancelling the turn (/stop →
		// ctx). This mirrors chatAsker.Ask (ask_user_question) below; both
		// surfaces reach the asker only in an interactive session, since strict
		// mode resolves Ask→Deny before the gate ever calls it.
		select {
		case text := <-replyCh:
			// The dispatcher folds "[Attached file: …]" notes into replies
			// that carry files; strip them so an approving word accompanied
			// by a screenshot still parses as the approval it is.
			cleaned, _ := agent.StripAttachmentNotes(text)
			return isAffirmative(cleaned), isAlways(cleaned), nil
		case <-ctx.Done():
			return false, false, ctx.Err()
		}
	}
}

// channelAsker adapts the chat into a tools.Asker for ask_user_question:
// the question goes out as a numbered list, and the requesting user's next
// message answers it through the same session ask slot the permission
// prompt uses. Stamped into the turn ctx by handleChannelMessage
// (tools.WithAsker), where it overrides the process-global wsAsker — which
// would otherwise broadcast IM questions to browser tabs that don't exist.
func (s *Server) channelAsker(sess *channel.Session, ad channel.Adapter, ev channel.InboundEvent) tools.Asker {
	return chatAsker{sess: sess, ad: ad, ev: ev}
}

type chatAsker struct {
	sess *channel.Session
	ad   channel.Adapter
	ev   channel.InboundEvent
}

// channelFileSender adapts the platform adapter into a tools.ChannelFileSender
// for the send_file tool: it pins the inbound chat + reply context so the
// model can push a file to the user it is talking to. Stamped into the turn
// ctx by runChannelTurns (tools.WithChannelSender).
type channelFileSender struct {
	ad      channel.Adapter
	chatID  string
	replyTo string
}

func (s channelFileSender) SendFile(path, name string) error {
	res := s.ad.SendFile(s.chatID, path, name, s.replyTo)
	if !res.OK {
		if res.Error != "" {
			return errors.New(res.Error)
		}
		return errors.New("the channel rejected the file")
	}
	return nil
}

func (c chatAsker) Ask(ctx context.Context, q tools.AskRequest) (tools.AskResponse, error) {
	res := tools.AskResponse{Answers: make([]tools.AskAnswer, len(q.Questions))}
	for i, question := range q.Questions {
		answer, outcome, err := c.askOne(ctx, question, i, len(q.Questions))
		if err != nil {
			return tools.AskResponse{Outcome: tools.AskRejected}, err
		}
		if outcome != tools.AskSubmitted {
			// Clarify and reject both end the set here. Clarify still carries
			// the answers gathered so far, which is what the model reads.
			res.Outcome = outcome
			return res, nil
		}
		res.Answers[i] = answer
	}
	return res, nil
}

// askOne sends one question and consumes one reply. Each question takes its
// own ask slot, so parseAskReply keeps working against a single question and
// the "next plain message" contract is unchanged.
func (c chatAsker) askOne(ctx context.Context, q tools.AskQuestion, idx, total int) (tools.AskAnswer, tools.AskOutcome, error) {
	replyCh, release, err := c.sess.BeginAsk(c.ev.ChatID, c.ev.UserID)
	if err != nil {
		return tools.AskAnswer{}, tools.AskRejected, err
	}
	defer release()

	c.ad.SendText(c.ev.ChatID, renderChatQuestion(q, idx, total), c.ev.MessageID)

	// No timeout: this is a clarifying question in an attended chat, so waiting
	// for an actual reply is correct — released by cancelling the turn, same as
	// the permission ask above.
	select {
	case text := <-replyCh:
		answer, outcome := parseAskReply(text, q)
		return answer, outcome, nil
	case <-ctx.Done():
		return tools.AskAnswer{}, tools.AskRejected, ctx.Err()
	}
}

// renderChatQuestion lays one question out for a chat timeline: numbered
// options with their descriptions inline, then a "Chat about this" row.
//
// No "Other" row: over chat, free text already IS the reply, so listing it
// would be a row telling the user to do what they were going to do anyway.
// Previews and notes are omitted too — a preview exists to be compared side by
// side, which a timeline can't do, and notes annotate that comparison.
func renderChatQuestion(q tools.AskQuestion, idx, total int) string {
	var b strings.Builder
	b.WriteString("❓ ")
	fmt.Fprintf(&b, "[%s] ", q.HeaderOrDefault(idx))
	if total > 1 {
		fmt.Fprintf(&b, "(%d/%d) ", idx+1, total)
	}
	b.WriteString(q.Question + "\n")
	for i, opt := range q.Options {
		fmt.Fprintf(&b, "%d. %s", i+1, opt.Label)
		if opt.Description != "" {
			fmt.Fprintf(&b, " — %s", opt.Description)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "%d. Chat about this\n", len(q.Options)+1)
	if q.MultiSelect {
		b.WriteString("Reply with number(s), e.g. 1,3 — or free text for something else. An empty reply cancels.")
	} else {
		b.WriteString("Reply with a number — or free text for something else. An empty reply cancels.")
	}
	return b.String()
}

// parseAskReply maps the chat reply onto one question's answer: numbers pick
// options (several for multi-select), the tail number is "Chat about this", an
// exact label matches its option, anything else is free text. Out-of-range
// numbers fall through to free text rather than erroring — over chat,
// re-prompting loops are worse than letting the model see the raw reply.
//
// An empty reply rejects the whole set. Over chat there is no other way to say
// "stop asking", and skipping silently would leave the user answering a set
// they have already abandoned.
func parseAskReply(text string, q tools.AskQuestion) (tools.AskAnswer, tools.AskOutcome) {
	t := strings.TrimSpace(text)
	if t == "" {
		return tools.AskAnswer{}, tools.AskRejected
	}

	clarifyIdx := len(q.Options) + 1
	parts := strings.FieldsFunc(t, func(r rune) bool {
		return r == ',' || r == '，' || r == '、' || r == ' '
	})
	var choices []string
	numeric := len(parts) > 0
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 1 || n > clarifyIdx {
			numeric = false
			break
		}
		if n == clarifyIdx {
			return tools.AskAnswer{}, tools.AskClarify
		}
		choices = append(choices, q.Options[n-1].Label)
	}
	if numeric {
		if !q.MultiSelect && len(choices) > 1 {
			choices = choices[:1]
		}
		return tools.AskAnswer{Choices: choices}, tools.AskSubmitted
	}

	for _, opt := range q.Options {
		if strings.EqualFold(t, opt.Label) {
			return tools.AskAnswer{Choices: []string{opt.Label}}, tools.AskSubmitted
		}
	}
	return tools.AskAnswer{Custom: t}, tools.AskSubmitted
}
