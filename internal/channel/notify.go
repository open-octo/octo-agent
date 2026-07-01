package channel

import "fmt"

// SendOnce delivers a single outbound message to an IM chat by constructing
// the platform adapter from ~/.octo/channels.yml on the fly. It exists for
// proactive pushes (e.g. scheduled-task results) from processes that don't run
// inbound adapters, such as octo serve. Every registered platform can be
// pushed to, each with its own credential/target rules (documented in the
// cron-task-creator skill): Feishu and DingTalk use app credentials over
// REST, Telegram and Discord use their bot tokens, WeCom needs a group-robot
// webhook_key, and Weixin needs the on-disk login plus a context_token from
// the write-through store — so the target user must have messaged the bot at
// least once.
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

// SendFileOnce is the file counterpart to SendOnce: it delivers a single local
// file to an IM chat by constructing the platform adapter from config on the
// fly. Used as the fallback when no live adapter is running for the platform.
// The adapter picks the wire type (image / video / file) from the extension.
// The same per-platform credential/target rules as SendOnce apply.
func SendFileOnce(platform, chatID, path, name string) error {
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
	if res := a.SendFile(chatID, path, name, ""); !res.OK {
		return fmt.Errorf("send file to %s chat %s: %s", platform, chatID, res.Error)
	}
	return nil
}
