package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Leihb/octo-agent/internal/app"
	"github.com/Leihb/octo-agent/internal/channel"
)

// channelAskTimeout bounds how long an IM permission prompt waits for the
// user's reply before denying. Matches the web confirmation modal's posture
// of "no answer = no". Variable, not const, so tests can shorten it.
var channelAskTimeout = 5 * time.Minute

// imAffirmatives are the only replies that approve an IM permission prompt.
// Anything else denies — over chat, silence and ambiguity must fail closed.
var imAffirmatives = map[string]bool{
	"yes": true, "y": true, "ok": true, "allow": true,
	"是": true, "可以": true, "同意": true, "允许": true,
}

func isAffirmative(text string) bool {
	return imAffirmatives[strings.ToLower(strings.TrimSpace(text))]
}

// askInputSummary renders the part of toolInput the user is actually
// approving. The IM transport shows no tool cards before the gate fires, so
// without this the user would approve blind — "Allow terminal?" tells them
// nothing about WHICH command. Known primary fields come first; anything
// else falls back to a JSON head.
func askInputSummary(toolInput map[string]any) string {
	const maxLen = 160
	for _, key := range []string{"command", "path", "url", "reason", "pattern"} {
		if v, ok := toolInput[key].(string); ok && strings.TrimSpace(v) != "" {
			return truncateForAsk(v, maxLen)
		}
	}
	if len(toolInput) == 0 {
		return ""
	}
	b, err := json.Marshal(toolInput)
	if err != nil {
		return ""
	}
	return truncateForAsk(string(b), maxLen)
}

func truncateForAsk(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// channelPermissionAsk builds the app.PermissionAsk for one IM turn: it sends
// a confirmation prompt into the chat and consumes the requesting user's next
// plain message in that chat as the answer (routed by the inbound dispatcher
// via DeliverAskReply, ahead of the turn path — see routeChannelEvent).
// Approval requires an explicit affirmative; any other reply, the turn being
// cancelled (/stop), or the timeout denies. remember is always false: chat
// approvals are one-shot, a lingering allow in a group chat would outlive
// the person who granted it.
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
		prompt := fmt.Sprintf(
			"⚠️ Allow %s? Reply yes / 允许 to approve — any other reply denies; only the requester's reply counts. (Auto-deny in %s; /stop cancels the task.)",
			what, channelAskTimeout)
		ad.SendText(ev.ChatID, prompt, ev.MessageID)

		timer := time.NewTimer(channelAskTimeout)
		defer timer.Stop()
		select {
		case text := <-replyCh:
			return isAffirmative(text), false, nil
		case <-ctx.Done():
			return false, false, ctx.Err()
		case <-timer.C:
			return false, false, nil
		}
	}
}
