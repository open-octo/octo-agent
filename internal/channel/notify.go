package channel

import "fmt"

// SendOnce delivers a single outbound message to an IM chat by constructing
// the platform adapter from ~/.octo/channels.yml on the fly. It exists for
// proactive pushes (e.g. scheduled-task results) from processes that don't run
// inbound adapters, such as octo serve. Feishu sends with app credentials
// alone (tenant token → REST). DingTalk sends through the proactive robot
// APIs (app credentials; needs the robot-message-send permission, and the
// chat id must be a staff id or a "cid…" openConversationId). Weixin sends
// with the on-disk login credentials plus the chat's last context_token from
// the write-through store the receive loop maintains — so the target user
// must have messaged the bot at least once.
func SendOnce(platform, chatID, text string) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	pc, ok := cfg.Channels[platform]
	if !ok {
		return fmt.Errorf("channel %q not configured", platform)
	}
	ctor, err := Find(platform)
	if err != nil {
		return err
	}
	a, err := ctor(pc)
	if err != nil {
		return err
	}
	if res := a.SendText(chatID, text, ""); !res.OK {
		return fmt.Errorf("send to %s chat %s: %s", platform, chatID, res.Error)
	}
	return nil
}
