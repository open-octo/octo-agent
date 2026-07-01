package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/open-octo/octo-agent/internal/channel"
	"github.com/open-octo/octo-agent/internal/config"
	"github.com/open-octo/octo-agent/internal/tools"
)

// cliMessenger backs the send_message / send_file tools in the CLI and TUI.
// The CLI process runs no channel adapters, so it prefers a locally running
// `octo serve` (which has live adapters — required for stateful platforms like
// WeChat, whose send needs the running iLink session + a fresh context token)
// and falls back to a one-shot send built from config for stateless platforms
// (Feishu / Telegram / Discord / WeCom) when no serve is reachable.
type cliMessenger struct{}

func (cliMessenger) SendMessage(platform, chatID, text string) error {
	if c := localServe(); c != nil {
		return c.sendText(platform, chatID, text)
	}
	return channel.SendOnce(platform, chatID, text)
}

func (cliMessenger) SendFile(platform, chatID, path, name string) error {
	if c := localServe(); c != nil {
		return c.sendFile(platform, chatID, path, name)
	}
	return channel.SendFileOnce(platform, chatID, path, name)
}

func (cliMessenger) KnownChats() []tools.KnownRecipient {
	// Recipients come from a running serve (it knows live sessions + binds).
	// Offline there is no adapter to reach anyone anyway, so return nothing and
	// let the tool ask for an explicit chat_id.
	if c := localServe(); c != nil {
		if recs, err := c.recipients(); err == nil {
			return recs
		}
	}
	return nil
}

// serveClient talks to a local octo serve over loopback. A CLI request from
// 127.0.0.1 with no Origin passes serve's loopback auth exemption; the access
// key from config is sent too when present (covers a non-default posture).
type serveClient struct {
	base string
	key  string
	hc   *http.Client
}

// localServe returns a client to a reachable local serve, or nil. The result
// is intentionally not cached — the probe is cheap and the daemon may come and
// go across turns. Address override: OCTO_SERVE_ADDR (default 127.0.0.1:8088).
func localServe() *serveClient {
	addr := strings.TrimSpace(os.Getenv("OCTO_SERVE_ADDR"))
	if addr == "" {
		addr = "127.0.0.1:8088"
	}
	conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return nil
	}
	_ = conn.Close()

	key := ""
	if cfg, err := config.Load(); err == nil {
		key = cfg.AccessKey
	}
	return &serveClient{
		base: "http://" + addr,
		key:  key,
		hc:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *serveClient) do(method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.key != "" {
		req.Header.Set("X-Access-Key", c.key)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("serve %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *serveClient) sendText(platform, chatID, text string) error {
	return c.do("POST", "/api/channels/"+url.PathEscape(platform)+"/send",
		map[string]string{"chat_id": chatID, "text": text}, nil)
}

func (c *serveClient) sendFile(platform, chatID, path, name string) error {
	return c.do("POST", "/api/channels/"+url.PathEscape(platform)+"/send-file",
		map[string]string{"chat_id": chatID, "path": path, "name": name}, nil)
}

func (c *serveClient) recipients() ([]tools.KnownRecipient, error) {
	var resp struct {
		Recipients []struct {
			Platform string `json:"platform"`
			ChatID   string `json:"chat_id"`
			UserID   string `json:"user_id"`
			Active   bool   `json:"active"`
			Bound    bool   `json:"bound"`
		} `json:"recipients"`
	}
	if err := c.do("GET", "/api/channels/recipients", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]tools.KnownRecipient, 0, len(resp.Recipients))
	for _, r := range resp.Recipients {
		var tags []string
		if r.Active {
			tags = append(tags, "active")
		}
		if r.Bound {
			tags = append(tags, "bound")
		}
		out = append(out, tools.KnownRecipient{
			Platform: r.Platform,
			ChatID:   r.ChatID,
			UserID:   r.UserID,
			Label:    strings.Join(tags, ","),
		})
	}
	return out, nil
}
