package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/gorilla/websocket"
)

// Push wakeups (M1c): when something on this host needs the user's attention
// and their phone isn't connected, ask the relay to send a content-free push.
//
// The host listens to the server's global session_activity broadcasts on its
// own loopback /ws connection (the tunnel is already an ordinary /ws client
// for bridged traffic; this is one more). On a qualifying activity it sends a
// wakeup control frame per registered push token. The RELAY decides whether
// to actually push: it owns connection truth (the host never learns about a
// phone's disconnect — there is no device_left frame), so a wakeup for a
// currently-connected device is dropped there, not here. The host's job is
// only to hold tokens (received end-to-end from the phone, keyed by its
// stable Noise static key) and to rate-limit so a chatty session can't turn
// into push spam.

const wakeupMinInterval = 30 * time.Second

// pushReg is one phone's registered push token. deviceID is the phone's
// relay-assigned id from the connection the registration arrived on — the
// relay uses it to check "is this phone currently connected". The phone
// re-registers on every connect, but there is a window (reconnect until the
// push-token frame lands, and after a host relay-reconnect reissues device
// ids) where deviceID is stale: the relay then misses the connectivity match
// and sends one redundant generic push. Benign, and capped by the rate limit.
type pushReg struct {
	token    string
	platform string
	deviceID string
	lastWake time.Time
}

// registerPushToken records (or refreshes) a phone's push token. peerStatic
// is the phone's Noise static public key — the only identity stable across
// reconnects. Empty tokens unregister (a phone whose permission was revoked).
func (t *Tunnel) registerPushToken(peerStatic []byte, deviceID, token, platform string) {
	if len(peerStatic) == 0 {
		return
	}
	key := base64.StdEncoding.EncodeToString(peerStatic)
	t.pushMu.Lock()
	defer t.pushMu.Unlock()
	if t.pushRegs == nil {
		t.pushRegs = map[string]*pushReg{}
	}
	if token == "" {
		delete(t.pushRegs, key)
		t.logf("[tunnel] device=%s push token cleared", deviceID)
		return
	}
	prev := t.pushRegs[key]
	reg := &pushReg{token: token, platform: platform, deviceID: deviceID}
	if prev != nil {
		reg.lastWake = prev.lastWake // a re-register must not reset the rate limit
	}
	t.pushRegs[key] = reg
	// Log the fact, never the token.
	t.logf("[tunnel] device=%s push token registered platform=%s", deviceID, platform)
}

// wakeDevices sends one wakeup frame per registered token, rate-limited per
// phone. Connectivity gating happens at the relay (see the package comment).
//
// The relay write happens OUTSIDE pushMu: writeRelay blocks on a websocket
// with no write deadline, and the relay read loop's registerPushToken path
// also takes pushMu — holding it across a stalled write would wedge the read
// loop until the socket died.
func (t *Tunnel) wakeDevices() {
	now := time.Now()
	type job struct {
		reg     *pushReg
		payload []byte
		device  string
	}
	var jobs []job
	t.pushMu.Lock()
	for _, reg := range t.pushRegs {
		if now.Sub(reg.lastWake) < wakeupMinInterval {
			continue
		}
		payload, err := json.Marshal(wakeupPayload{PushToken: reg.token, Platform: reg.platform})
		if err != nil {
			continue
		}
		jobs = append(jobs, job{reg: reg, payload: payload, device: reg.deviceID})
	}
	t.pushMu.Unlock()

	for _, j := range jobs {
		if err := t.writeRelay(frame{Type: frameWakeup, Device: j.device, Payload: j.payload}); err != nil {
			// Relay down; its reconnect loop will recover. Don't burn the
			// rate-limit window on a frame that never left.
			continue
		}
		t.pushMu.Lock()
		j.reg.lastWake = now
		t.pushMu.Unlock()
	}
}

// activityEvent is the subset of the server's session_activity broadcast the
// watcher cares about.
type activityEvent struct {
	Type string `json:"type"`
	Kind string `json:"kind"`
}

// wakeKinds are the activity kinds that justify waking a phone: the agent is
// waiting on the user (question/approval) or finished a turn whose result
// they asked for. Mirrors the web UI's notification triggers.
func wakeKind(kind string) bool {
	switch kind {
	case "question_pending", "confirm_pending", "turn_complete":
		return true
	}
	return false
}

// watchActivity keeps a loopback /ws connection open and fires wakeDevices on
// qualifying global session_activity events. Same infinite-retry posture as
// the relay connection; exits when ctx does.
func (t *Tunnel) watchActivity(ctx context.Context) {
	backoff := initialBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		err := t.watchActivityOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		t.logf("[tunnel] activity watcher disconnected (%v); retrying in %s", err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (t *Tunnel) watchActivityOnce(ctx context.Context) error {
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, t.wsBase+"/ws", nil)
	if err != nil {
		return err
	}
	defer ws.Close()
	go func() {
		<-ctx.Done()
		ws.Close()
	}()
	for {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		var ev activityEvent
		if json.Unmarshal(msg, &ev) != nil {
			continue
		}
		if ev.Type == "session_activity" && wakeKind(ev.Kind) {
			t.wakeDevices()
		}
	}
}
