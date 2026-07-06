package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/app"
	"github.com/open-octo/octo-agent/internal/channel"
	"github.com/open-octo/octo-agent/internal/tools"
)

// channelAskTimeout bounds how long an IM permission prompt waits for the
// user's reply before denying. Matches the web confirmation modal's posture
// of "no answer = no". Variable, not const, so tests can shorten it.
var channelAskTimeout = 5 * time.Minute

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
				"⚠️ Allow %s? Reply yes / 允许 to approve once, always / 总是允许 to stop asking for this exact call — any other reply denies; only the requester's reply counts. (Auto-deny in %s; /stop cancels the task.)",
				what, channelAskTimeout)
			ad.SendText(ev.ChatID, prompt, ev.MessageID)
		}

		timer := time.NewTimer(channelAskTimeout)
		defer timer.Stop()
		select {
		case text := <-replyCh:
			return isAffirmative(text), isAlways(text), nil
		case <-ctx.Done():
			return false, false, ctx.Err()
		case <-timer.C:
			return false, false, nil
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
	replyCh, release, err := c.sess.BeginAsk(c.ev.ChatID, c.ev.UserID)
	if err != nil {
		return tools.AskResponse{}, err
	}
	defer release()

	var b strings.Builder
	b.WriteString("❓ ")
	if q.Header != "" {
		fmt.Fprintf(&b, "[%s] ", q.Header)
	}
	b.WriteString(q.Question + "\n")
	for i, opt := range q.Options {
		fmt.Fprintf(&b, "%d. %s\n", i+1, opt)
	}
	if q.MultiSelect {
		b.WriteString("Reply with number(s), e.g. 1,3 — or free text for something else.")
	} else {
		b.WriteString("Reply with a number — or free text for something else.")
	}
	c.ad.SendText(c.ev.ChatID, b.String(), c.ev.MessageID)

	timer := time.NewTimer(channelAskTimeout)
	defer timer.Stop()
	select {
	case text := <-replyCh:
		return parseAskReply(text, q), nil
	case <-ctx.Done():
		return tools.AskResponse{Cancelled: true}, ctx.Err()
	case <-timer.C:
		return tools.AskResponse{Cancelled: true}, nil
	}
}

// parseAskReply maps the chat reply onto the structured response: numbers
// pick options (several for multi-select), an exact label matches its
// option, anything else is a free-text "Other" answer. Out-of-range numbers
// fall through to free text rather than erroring — over chat, re-prompting
// loops are worse than letting the model see the raw reply.
func parseAskReply(text string, q tools.AskRequest) tools.AskResponse {
	t := strings.TrimSpace(text)
	if t == "" {
		return tools.AskResponse{Cancelled: true}
	}

	parts := strings.FieldsFunc(t, func(r rune) bool {
		return r == ',' || r == '，' || r == '、' || r == ' '
	})
	var choices []string
	numeric := len(parts) > 0
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n < 1 || n > len(q.Options) {
			numeric = false
			break
		}
		choices = append(choices, q.Options[n-1])
	}
	if numeric {
		if !q.MultiSelect && len(choices) > 1 {
			choices = choices[:1]
		}
		return tools.AskResponse{Choices: choices}
	}

	for _, opt := range q.Options {
		if strings.EqualFold(t, opt) {
			return tools.AskResponse{Choices: []string{opt}}
		}
	}
	return tools.AskResponse{Custom: t}
}
