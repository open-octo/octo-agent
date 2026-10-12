package feishu

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/open-octo/octo-agent/internal/channel"
)

// Golden bytes produced by the official SDK's generated Frame.Marshal
// (github.com/larksuite/oapi-sdk-go/v3/ws), so the hand-written codec is
// checked against the real wire format rather than against itself.
const (
	sdkPingFrameHex  = "08001000180720002a0c0a0474797065120470696e6732003a004a00"
	sdkEventFrameHex = "080b1016180720012a0d0a047479706512056576656e742a100a0a6d6573736167655f696412026d312a080a0373756d12013132003a0042077b2261223a317d4a014c"
)

func sdkEventFrame() *frame {
	return &frame{
		SeqID: 11, LogID: 22, Service: 7, Method: frameMethodData,
		Headers: []frameHeader{{"type", "event"}, {"message_id", "m1"}, {"sum", "1"}},
		Payload: []byte(`{"a":1}`), LogIDNew: "L",
	}
}

func TestFrameMarshal_MatchesSDK(t *testing.T) {
	ping := (&wsConn{serviceID: 7}).pingFrame()
	if got := hex.EncodeToString(ping.marshal()); got != sdkPingFrameHex {
		t.Errorf("ping frame\n got %s\nwant %s", got, sdkPingFrameHex)
	}
	if got := hex.EncodeToString(sdkEventFrame().marshal()); got != sdkEventFrameHex {
		t.Errorf("event frame\n got %s\nwant %s", got, sdkEventFrameHex)
	}
}

func TestUnmarshalFrame_SDKBytes(t *testing.T) {
	raw, _ := hex.DecodeString(sdkEventFrameHex)
	f, err := unmarshalFrame(raw)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := sdkEventFrame()
	want.Payload = []byte(`{"a":1}`)
	if f.SeqID != want.SeqID || f.LogID != want.LogID || f.Service != want.Service ||
		f.Method != want.Method || f.LogIDNew != want.LogIDNew || !bytes.Equal(f.Payload, want.Payload) {
		t.Fatalf("got %+v, want %+v", f, want)
	}
	if len(f.Headers) != 3 || f.header("message_id") != "m1" || f.header("type") != "event" {
		t.Fatalf("headers = %+v", f.Headers)
	}
}

func TestUnmarshalFrame_Truncated(t *testing.T) {
	raw, _ := hex.DecodeString(sdkEventFrameHex)
	// Cut into the middle of the payload field (the last 3 bytes are a whole
	// LogIDNew field, so dropping only those would still be valid).
	if _, err := unmarshalFrame(raw[:len(raw)-6]); err == nil {
		t.Fatal("expected an error for a truncated frame")
	}
}

func TestCollectParts(t *testing.T) {
	pending := map[string]*pendingParts{}
	part := func(seq, payload string) *frame {
		return &frame{Headers: []frameHeader{{hdrMessageID, "m"}, {hdrSum, "2"}, {hdrSeq, seq}}, Payload: []byte(payload)}
	}
	if got := collectParts(pending, part("1", "world")); got != nil {
		t.Fatalf("incomplete event returned %q", got)
	}
	if got := string(collectParts(pending, part("0", "hello "))); got != "hello world" {
		t.Fatalf("got %q", got)
	}
	if len(pending) != 0 {
		t.Fatalf("pending not cleared: %v", pending)
	}
	if got := collectParts(pending, part("5", "x")); got != nil || len(pending) != 0 {
		t.Fatalf("out-of-range seq should be dropped, got %q pending=%v", got, pending)
	}
}

// #2611: the bootstrap route answered 404 with a plain-text body, which
// surfaced as "json: cannot unmarshal number …".
func TestGetWSEndpoint_ReportsHTTPStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "404 page not found", http.StatusNotFound)
	}))
	defer srv.Close()
	a := &Adapter{domain: srv.URL, http: srv.Client()}
	_, err := a.getWSEndpoint(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("err = %v, want it to name HTTP 404", err)
	}
}

// End to end against a fake gateway: credentials go in the bootstrap body,
// the first frame is a protobuf ping carrying the URL's service_id, and a
// split event is reassembled, acked, and dispatched.
func TestStart_LongConnection(t *testing.T) {
	ackCh := make(chan *frame, 1)
	pingCh := make(chan *frame, 1)
	errCh := make(chan string, 4)

	event := `{"schema":"2.0","header":{"event_type":"im.message.receive_v1"},"event":{"message":{"message_id":"om-1","chat_id":"oc-1","chat_type":"p2p","message_type":"text","content":"{\"text\":\"hi there\"}"},"sender":{"sender_id":{"open_id":"ou-user"}}}}`
	half := len(event) / 2

	upgrader := websocket.Upgrader{}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "tok", "expire": 7200})
		case "/open-apis/bot/v3/info":
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "bot": map[string]any{"open_id": "ou_bot"}})
		case "/callback/ws/endpoint":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["AppID"] != "app" || body["AppSecret"] != "secret" {
				errCh <- "bootstrap body missing credentials"
			}
			if r.Header.Get("Authorization") != "" {
				errCh <- "bootstrap should not send a bearer token"
			}
			wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?device_id=d&service_id=7"
			json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "msg": "",
				"data": map[string]any{"URL": wsURL, "ClientConfig": map[string]any{"PingInterval": 60}},
			})
		case "/ws":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			readFrame := func() *frame {
				conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				mt, raw, err := conn.ReadMessage()
				if err != nil || mt != websocket.BinaryMessage {
					return nil
				}
				f, _ := unmarshalFrame(raw)
				return f
			}
			pingCh <- readFrame()
			for seq, p := range []string{event[:half], event[half:]} {
				f := &frame{SeqID: 1, Service: 7, Method: frameMethodData, Headers: []frameHeader{
					{hdrType, "event"}, {hdrMessageID, "msg-1"}, {hdrSum, "2"}, {hdrSeq, string(rune('0' + seq))},
				}, Payload: []byte(p)}
				conn.WriteMessage(websocket.BinaryMessage, f.marshal())
			}
			ackCh <- readFrame()
			// Hold the connection until the client closes it.
			conn.SetReadDeadline(time.Time{})
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	a, err := New(channel.PlatformConfig{cfgAppID: "app", cfgAppSecret: "secret", cfgDomain: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan channel.InboundEvent, 1)
	done := make(chan struct{})
	go func() {
		a.Start(ctx, func(ev channel.InboundEvent) { got <- ev })
		close(done)
	}()

	select {
	case ping := <-pingCh:
		if ping == nil || ping.Method != frameMethodControl || ping.Service != 7 || ping.header(hdrType) != "ping" {
			t.Fatalf("first frame = %+v, want a protobuf ping for service 7", ping)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no ping received")
	}

	select {
	case ack := <-ackCh:
		if ack == nil || string(ack.Payload) != `{"code":200}` || ack.header(hdrMessageID) != "msg-1" || ack.header(hdrBizRt) == "" {
			t.Fatalf("ack = %+v", ack)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no ack received")
	}

	select {
	case ev := <-got:
		if ev.Text != "hi there" || ev.ChatID != "oc-1" || ev.UserID != "ou-user" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("event not dispatched")
	}

	select {
	case msg := <-errCh:
		t.Fatal(msg)
	default:
	}

	// Cancellation must close the socket rather than wait out the read
	// deadline (2×60s+5s here) with a zombie connection still acking.
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return promptly after cancel")
	}
}
