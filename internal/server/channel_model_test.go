package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/channel"
	"github.com/open-octo/octo-agent/internal/config"
)

// TestApplyChannelModel_HonorsStoreBinding mirrors
// TestHandleUpdateSessionModel_EntryNameBindsSession for the IM path: an IM
// session whose backing store carries a model binding (set by IM /model or
// the web model picker) must run its turns on that entry's sender, not the
// default the channel factory baked in at session creation.
func TestApplyChannelModel_HonorsStoreBinding(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	seed := config.Config{
		Models: []config.ModelEntry{
			{Provider: "anthropic", Model: "claude-sonnet-4-6"},
			{Provider: "kimi", Model: "kimi-k2.6", APIKey: "sk-kimi"},
		},
		DefaultModel: "claude-sonnet-4-6",
	}
	if err := seed.Save(); err != nil {
		t.Fatal(err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	mgr := channel.NewManager(&channel.Config{}, func() *agent.Agent {
		return agent.New(srv.getSender(), "stub-model")
	}, channel.BindByChatUser)
	sess := mgr.GetOrCreateSession(channel.InboundEvent{Platform: "mock", ChatID: "c1", UserID: "u1"})

	// Unbound: default sender, model from the store.
	srv.applyChannelModel(sess)
	if sess.Agent.GetSender() != srv.getSender() {
		t.Error("unbound session must ride the default sender")
	}
	if sess.Agent.Model != "stub-model" {
		t.Errorf("agent model = %q, want stub-model from the store", sess.Agent.Model)
	}

	// Bound to a configured entry: that entry's sender + model.
	if err := sess.Store.SetModelConfig("kimi-k2.6", "kimi-k2.6"); err != nil {
		t.Fatal(err)
	}
	srv.applyChannelModel(sess)
	if sess.Agent.Model != "kimi-k2.6" {
		t.Errorf("agent model = %q, want kimi-k2.6", sess.Agent.Model)
	}
	if sess.Agent.GetSender() == srv.getSender() {
		t.Error("bound session must not ride the default sender")
	}

	// Binding entry deleted: fall back to the default sender instead of
	// failing the turn.
	if w := doJSON(t, srv, http.MethodDelete, "/api/config/models/kimi-k2.6", ""); w.Code != http.StatusOK {
		t.Fatalf("DELETE = %d: %s", w.Code, w.Body.String())
	}
	srv.applyChannelModel(sess)
	if sess.Agent.GetSender() != srv.getSender() {
		t.Error("stale binding must fall back to the default sender")
	}
}

// TestChannelModelOps_Resolve covers the /model resolver the manager calls:
// "default" unbinds, a configured id binds to its entry's sender, and an
// unknown id is rejected with the available list.
func TestChannelModelOps_Resolve(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	seed := config.Config{
		Models: []config.ModelEntry{
			{Provider: "anthropic", Model: "claude-sonnet-4-6"},
			{Provider: "kimi", Model: "kimi-k2.6", APIKey: "sk-kimi"},
		},
		DefaultModel: "claude-sonnet-4-6",
	}
	if err := seed.Save(); err != nil {
		t.Fatal(err)
	}

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
	ops := srv.channelModelOps()

	def, err := ops.Resolve("default")
	if err != nil {
		t.Fatalf("resolve default: %v", err)
	}
	if def.BoundEntry != "" {
		t.Errorf("default resolution bound to %q, want unbound", def.BoundEntry)
	}
	if def.Sender != srv.getSender() {
		t.Error("default resolution must return the default sender")
	}

	bound, err := ops.Resolve("kimi-k2.6")
	if err != nil {
		t.Fatalf("resolve kimi-k2.6: %v", err)
	}
	// PR4b: bare model resolves through ParseModelFlag; the bound entry is the
	// composite id pointing at kimi's endpoint. The exact endpoint id is the
	// synthesised legacy-<host>-<n> form, so assert by suffix.
	if bound.Model != "kimi-k2.6" {
		t.Errorf("bound model = %q, want kimi-k2.6", bound.Model)
	}
	if !strings.HasSuffix(bound.BoundEntry, "::kimi-k2.6") {
		t.Errorf("bound entry = %q, want suffix ::kimi-k2.6 (composite id)", bound.BoundEntry)
	}
	if bound.Sender == srv.getSender() {
		t.Error("configured entry must not ride the default sender")
	}

	// Composite id path: resolve the same model by its composite id (read
	// back from the bare-model resolution) — must produce the same binding.
	bound2, err := ops.Resolve(bound.BoundEntry)
	if err != nil {
		t.Fatalf("resolve composite id %q: %v", bound.BoundEntry, err)
	}
	if bound2.BoundEntry != bound.BoundEntry {
		t.Errorf("composite-id resolution = %q, want %q", bound2.BoundEntry, bound.BoundEntry)
	}

	if _, err := ops.Resolve("nope"); err == nil {
		t.Error("unknown model id must be rejected")
	}

	infos := ops.List()
	if len(infos) != 2 {
		t.Fatalf("List returned %d entries, want 2", len(infos))
	}
	var sawDefault bool
	for _, info := range infos {
		if info.Model == "claude-sonnet-4-6" && info.Default {
			sawDefault = true
		}
		if info.EndpointID == "" || info.CompositeID == "" {
			t.Errorf("info missing endpoint grouping: %+v", info)
		}
		if info.CompositeID != info.EndpointID+"::"+info.Model {
			t.Errorf("composite id = %q, want %s::%s", info.CompositeID, info.EndpointID, info.Model)
		}
	}
	if !sawDefault {
		t.Error("List must mark claude-sonnet-4-6 as the default entry")
	}
}
