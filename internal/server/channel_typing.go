package server

import (
	"log/slog"
	"sync"
	"time"

	"github.com/open-octo/octo-agent/internal/channel"
)

// typingKeepaliveInterval is how often SendTyping is re-invoked while a turn
// is in flight. Telegram/Discord's typing indicator expires client-side in
// 5-10s; Feishu/DingTalk/WeCom's SendTyping is a no-op so repeating it costs
// nothing; Weixin's own SendTyping already runs a persistent per-chat
// keepalive, so repeating it here just re-arms an already-idempotent ticker.
const typingKeepaliveInterval = 5 * time.Second

// startTypingKeepalive sends an initial typing indicator and re-sends it
// every typingKeepaliveInterval until the returned stop func is called (safe
// to call more than once — only the first call has an effect). Without this,
// a long agentic turn goes dark to the user the moment the platform's own
// typing indicator expires (or never showed one at all, e.g. Feishu),
// reading as the bot having died (#1117).
func startTypingKeepalive(ad channel.Adapter, chatID, contextToken string) (stop func()) {
	return startTypingKeepaliveInterval(ad, chatID, contextToken, typingKeepaliveInterval)
}

// startTypingKeepaliveInterval is startTypingKeepalive with an injectable
// interval, so tests don't have to wait out the real 5s cadence.
func startTypingKeepaliveInterval(ad channel.Adapter, chatID, contextToken string, interval time.Duration) (stop func()) {
	done := make(chan struct{})
	var once sync.Once
	stop = func() { once.Do(func() { close(done) }) }

	sendTyping := func() {
		if err := ad.SendTyping(chatID, contextToken); err != nil {
			slog.Debug("channel sendTyping", "err", err)
		}
	}
	sendTyping()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				if err := ad.StopTyping(chatID, contextToken); err != nil {
					slog.Debug("channel stopTyping", "err", err)
				}
				return
			case <-ticker.C:
				sendTyping()
			}
		}
	}()
	return stop
}
