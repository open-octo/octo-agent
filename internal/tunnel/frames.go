package tunnel

import "encoding/json"

// shimFrame is the plaintext unit multiplexed inside the Noise data channel
// between the phone's local shim and this host. One channel carries many
// concurrent /api requests and /ws sockets, each tagged with a stream id.
//
// It mirrors mobile/src/frames.ts field-for-field (a service-boundary duplicate,
// like the relay/host wire.Frame split): the two ends agree on this JSON, not on
// shared code. Keep the json tags in lockstep with frames.ts.
type shimFrame struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`

	// http-req / http-resp
	Method  string            `json:"method,omitempty"`
	Path    string            `json:"path,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    *string           `json:"body,omitempty"` // string|null in frames.ts
	Status  int               `json:"status,omitempty"`

	// ws-open / ws-msg / ws-close / ws-error
	Data    string `json:"data,omitempty"`
	Code    int    `json:"code,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

const (
	shimHTTPReq   = "http-req"
	shimHTTPResp  = "http-resp"
	shimWSOpen    = "ws-open"
	shimWSMessage = "ws-msg"
	shimWSClose   = "ws-close"
	shimWSError   = "ws-error"
)

func decodeShimFrame(b []byte) (shimFrame, error) {
	var f shimFrame
	err := json.Unmarshal(b, &f)
	return f, err
}

func (f shimFrame) encode() ([]byte, error) {
	return json.Marshal(f)
}

func strPtr(s string) *string { return &s }
