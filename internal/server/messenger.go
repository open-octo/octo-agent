package server

import (
	"strings"

	"github.com/open-octo/octo-agent/internal/tools"
)

// serverMessenger backs the send_message tool (tools.ChannelMessenger): it
// lets any turn — web, IM, or a sub-agent — push a text message to an IM chat
// the server can reach. Registered at server start via tools.SetMessenger.
type serverMessenger struct{ s *Server }

// SendMessage delivers text to chatID on platform, reusing the same
// live-adapter-then-SendOnce path as scheduled-task notifications.
func (m serverMessenger) SendMessage(platform, chatID, text string) error {
	return m.s.channelSend(platform, chatID, text)
}

// KnownChats lists the chats the bot can currently address, derived from the
// channel manager's live sessions plus the persisted /bind table.
func (m serverMessenger) KnownChats() []tools.KnownRecipient {
	mgr := m.s.channelManager()
	if mgr == nil {
		return nil
	}
	known := mgr.KnownChats()
	out := make([]tools.KnownRecipient, 0, len(known))
	for _, kc := range known {
		var tags []string
		if kc.Active {
			tags = append(tags, "active")
		}
		if kc.Bound {
			tags = append(tags, "bound")
		}
		out = append(out, tools.KnownRecipient{
			Platform: kc.Platform,
			ChatID:   kc.ChatID,
			UserID:   kc.UserID,
			Label:    strings.Join(tags, ","),
		})
	}
	return out
}
