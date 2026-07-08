package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/open-octo/octo-agent/internal/channel"
)

// typingCountAdapter implements channel.Adapter with no-ops everywhere except
// SendTyping/StopTyping, which it counts.
type typingCountAdapter struct {
	mu         sync.Mutex
	sendTyping int
	stopTyping int
}

func (a *typingCountAdapter) Platform() string { return "fake" }
func (a *typingCountAdapter) Start(ctx context.Context, _ func(channel.InboundEvent)) error {
	<-ctx.Done()
	return nil
}
func (a *typingCountAdapter) Stop() error { return nil }
func (a *typingCountAdapter) SendText(chatID, text, replyTo string) channel.SendResult {
	return channel.SendResult{OK: true}
}
func (a *typingCountAdapter) SendFile(chatID, path, name, replyTo string) channel.SendResult {
	return channel.SendResult{OK: true}
}
func (a *typingCountAdapter) UpdateMessage(chatID, messageID, text string) bool { return true }
func (a *typingCountAdapter) SupportsMessageUpdates() bool                      { return false }
func (a *typingCountAdapter) SendTyping(chatID, contextToken string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sendTyping++
	return nil
}
func (a *typingCountAdapter) StopTyping(chatID, contextToken string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stopTyping++
	return nil
}
func (a *typingCountAdapter) Flush(chatID string)   {}
func (a *typingCountAdapter) SupportsButtons() bool { return false }
func (a *typingCountAdapter) SendButtons(chatID, text string, buttons []channel.Button, replyTo string) channel.SendResult {
	return channel.SendResult{OK: true}
}
func (a *typingCountAdapter) ValidateConfig(channel.PlatformConfig) []string { return nil }

func (a *typingCountAdapter) counts() (sendTyping, stopTyping int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sendTyping, a.stopTyping
}

func TestStartTypingKeepalive_RepeatsUntilStopped(t *testing.T) {
	ad := &typingCountAdapter{}
	stop := startTypingKeepaliveInterval(ad, "chat1", "", 20*time.Millisecond)

	if send, _ := ad.counts(); send != 1 {
		t.Fatalf("SendTyping should fire immediately, count = %d", send)
	}

	// Wait for at least two more ticks. Polled rather than a fixed sleep so a
	// slow CI scheduler (esp. Windows) can't turn this into a flaky failure.
	waitFor(t, func() bool {
		send, _ := ad.counts()
		return send >= 3
	})
	if _, stopBefore := ad.counts(); stopBefore != 0 {
		t.Fatalf("StopTyping should not fire before stop() is called, count = %d", stopBefore)
	}

	stop() // blocks until the keepalive goroutine has actually called StopTyping
	sendAfterStop, stopAfter := ad.counts()
	if stopAfter != 1 {
		t.Fatalf("StopTyping should fire exactly once after stop(), count = %d", stopAfter)
	}

	// No further SendTyping calls once stopped.
	time.Sleep(50 * time.Millisecond)
	sendFinal, stopFinal := ad.counts()
	if sendFinal != sendAfterStop {
		t.Fatalf("SendTyping fired after stop(): before=%d after=%d", sendAfterStop, sendFinal)
	}
	if stopFinal != 1 {
		t.Fatalf("StopTyping fired more than once: %d", stopFinal)
	}
}

func TestStartTypingKeepalive_StopIsIdempotent(t *testing.T) {
	ad := &typingCountAdapter{}
	stop := startTypingKeepaliveInterval(ad, "chat1", "", 10*time.Millisecond)

	stop()
	stop()
	stop()

	if _, stopCount := ad.counts(); stopCount != 1 {
		t.Fatalf("StopTyping should fire exactly once no matter how many times stop() is called, count = %d", stopCount)
	}
}
