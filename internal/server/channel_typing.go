package server

import (
	"log/slog"
	"sync"
	"time"

	"github.com/open-octo/octo-agent/internal/channel"
)

// typingKeepaliveInterval is how often SendTyping is re-invoked while a turn
// is in flight, for adapters that need it. Telegram/Discord's typing
// indicator expires client-side in 5-10s; Feishu/DingTalk/WeCom's SendTyping
// is a no-op so repeating it costs nothing. Weixin sustains its own typing
// indicator after a single call and opts out of the repeat entirely — see
// channel.SelfSustainingTyper below.
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
			slog.Debug("channel sendTyping", "platform", ad.Platform(), "err", err)
		}
	}
	sendTyping()

	// An adapter that already sustains its own typing indicator after one
	// call (currently only Weixin) doesn't need — and shouldn't get —
	// repeated SendTyping calls: each one re-fetches Weixin's typing ticket
	// and restarts its internal keepalive goroutine, so ticking every 5s here
	// would just rack up redundant backend round-trips for the whole turn
	// with no additional user-visible effect.
	if ss, ok := ad.(channel.SelfSustainingTyper); ok && ss.SelfSustainingTyping() {
		go func() {
			<-done
			if err := ad.StopTyping(chatID, contextToken); err != nil {
				slog.Debug("channel stopTyping", "platform", ad.Platform(), "err", err)
			}
		}()
		return stop
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				if err := ad.StopTyping(chatID, contextToken); err != nil {
					slog.Debug("channel stopTyping", "platform", ad.Platform(), "err", err)
				}
				return
			case <-ticker.C:
				sendTyping()
			}
		}
	}()
	return stop
}
