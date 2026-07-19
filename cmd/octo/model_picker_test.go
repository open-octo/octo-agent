package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/open-octo/octo-agent/internal/agent"
	"github.com/open-octo/octo-agent/internal/config"
)

// writeModelsConfig writes a config with the given models to a temp HOME dir
// so config.Load() finds them.
func writeModelsConfig(t *testing.T, cfg config.Config) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	dir := filepath.Join(tmp, ".octo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir .octo: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

// newPickerTestModel builds a bare tuiModel with an agent on modelName.
// Its subAgentFocus is -1 (no sub-agent nav) and stderr is io.Discard so a
// sender rebuild in the Enter test path doesn't panic on a nil writer.
func newPickerTestModel(modelName string) *tuiModel {
	a := agent.New(&stubSender{reply: "ok"}, modelName)
	return &tuiModel{cfg: replConfig{a: a, stderr: io.Discard}, a: a, subAgentFocus: -1}
}

// pickUpdate runs m.Update and returns the resulting *tuiModel (cmd discarded).
func pickUpdate(m *tuiModel, msg tea.Msg) *tuiModel {
	updated, _ := m.Update(msg)
	return updated.(*tuiModel)
}

// TestDispatchModel_NoArgOpensPicker verifies that "/model" without an argument
// opens the picker overlay (instead of printing a usage error). PR4b: the
// picker is two-level — two distinct (provider, base_url) groups become two
// endpoints, and the cursor starts on the active model's endpoint.
func TestDispatchModel_NoArgOpensPicker(t *testing.T) {
	writeModelsConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "gpt-4o", Provider: "openai"},
			{Model: "claude-sonnet-4-6", Provider: "anthropic"},
		},
		DefaultModel: "gpt-4o",
	})

	m := newPickerTestModel("gpt-4o")
	model, _ := m.dispatchModel("")

	p := model.(*tuiModel).modelPicker
	if p == nil {
		t.Fatal("expected picker to be open after dispatchModel(\"\")")
	}
	if len(p.endpoints) != 2 {
		t.Fatalf("picker endpoints = %d, want 2 (one per distinct provider): %+v", len(p.endpoints), p.endpoints)
	}
	// Current endpoint should have exactly its models exposed as items.
	if len(p.items) != len(p.endpoints[p.epIdx].items) {
		t.Errorf("picker items = %d, want %d (current endpoint's models)", len(p.items), len(p.endpoints[p.epIdx].items))
	}
	// Cursor should start on the active model (gpt-4o). Its endpoint is the
	// openai one; within that endpoint gpt-4o is the only (→ index 0) model.
	if p.idx != 0 {
		t.Errorf("picker cursor = %d, want 0 (active model)", p.idx)
	}
}

// TestDispatchModel_NoArgCursorOnActive verifies the picker opens with the
// cursor on the active model — not always the first one.
func TestDispatchModel_NoArgCursorOnActive(t *testing.T) {
	writeModelsConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "gpt-4o", Provider: "openai"},
			{Model: "claude-sonnet-4-6", Provider: "anthropic"},
			{Model: "deepseek-v4-flash", Provider: "deepseek", BaseURL: "https://api.deepseek.com"},
		},
		DefaultModel: "claude-sonnet-4-6",
	})

	m := newPickerTestModel("claude-sonnet-4-6")
	m.dispatchModel("")

	p := m.modelPicker
	if p == nil {
		t.Fatal("picker not open")
	}
	// The active model claude-sonnet-4-6 is the only model under the anthropic
	// endpoint, so idx within that endpoint is 0; the endpoint cursor should be
	// on the anthropic endpoint (index 1, assuming openai→0, anthropic→1,
	// deepseek→2 ordering).
	if p.idx != 0 {
		t.Errorf("picker model cursor = %d, want 0 (claude-sonnet-4-6 is the only model in its endpoint)", p.idx)
	}
	if p.endpoints[p.epIdx].items[0].name != "claude-sonnet-4-6" {
		t.Errorf("active endpoint = %q, want the one holding claude-sonnet-4-6", p.endpoints[p.epIdx].items[0].name)
	}
}

// TestDispatchModel_PickerKeyDown verifies DOWN advances the cursor (with
// wrap) and UP moves it back, while the picker stays open. Uses a single
// endpoint with two models so ↑↓ stays within one endpoint.
func TestDispatchModel_PickerKeyDown(t *testing.T) {
	writeModelsConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "gpt-4o", Provider: "openai", BaseURL: "https://api.openai.com"},
			{Model: "gpt-4.1", Provider: "openai", BaseURL: "https://api.openai.com"},
		},
		DefaultModel: "gpt-4o",
	})

	m := newPickerTestModel("gpt-4o")
	m.dispatchModel("")

	if len(m.modelPicker.endpoints) != 1 {
		t.Fatalf("expected 1 endpoint (same provider+base_url), got %d", len(m.modelPicker.endpoints))
	}
	// Down → index 1
	m = pickUpdate(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.modelPicker.idx != 1 {
		t.Errorf("after DOWN: idx = %d, want 1", m.modelPicker.idx)
	}
	// Down again → wraps to 0
	m = pickUpdate(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.modelPicker.idx != 0 {
		t.Errorf("after DOWN (wrap): idx = %d, want 0", m.modelPicker.idx)
	}
	// Up → wraps to 1
	m = pickUpdate(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.modelPicker.idx != 1 {
		t.Errorf("after UP (wrap): idx = %d, want 1", m.modelPicker.idx)
	}
}

// TestDispatchModel_PickerKeyEsc verifies Esc closes the picker.
func TestDispatchModel_PickerKeyEsc(t *testing.T) {
	writeModelsConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "gpt-4o", Provider: "openai"},
		},
		DefaultModel: "gpt-4o",
	})

	m := newPickerTestModel("gpt-4o")
	m.dispatchModel("")

	m = pickUpdate(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.modelPicker != nil {
		t.Error("expected picker to be cleared after Esc")
	}
}

// TestDispatchModel_PickerKeyEnter verifies Enter switches to the highlighted
// (endpoint, model) and clears the picker. The accept path now produces a
// composite id "<endpoint>::<model>" so the receiver resolves the right
// endpoint's connection params (PR4b). Two models share a provider + base URL
// so ensureSender returns early (no live rebuild needed).
func TestDispatchModel_PickerKeyEnter(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-deepseek-key")
	writeModelsConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "deepseek-v4-flash", Provider: "deepseek", BaseURL: "https://api.deepseek.com"},
			{Model: "deepseek-v4-pro", Provider: "deepseek", BaseURL: "https://api.deepseek.com"},
		},
		DefaultModel: "deepseek-v4-flash",
	})

	m := newPickerTestModel("deepseek-v4-flash")
	m.dispatchModel("")

	// Move to index 1 (deepseek-v4-pro) within the single endpoint.
	m = pickUpdate(m, tea.KeyMsg{Type: tea.KeyDown})
	// Enter accepts.
	m = pickUpdate(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.modelPicker != nil {
		t.Error("picker should be cleared after Enter")
	}
	// m.a.Model is now the composite id; EntryByModel resolves it back to the
	// same model id, so we assert the suffix rather than the bare name.
	if !strings.HasSuffix(m.a.Model, "::deepseek-v4-pro") {
		t.Errorf("active model = %q, want suffix ::deepseek-v4-pro", m.a.Model)
	}
}

// TestModelPickerView_Renders verifies the overlay shows the focused endpoint's
// models (collapsed for others — ←→ switches focus to reveal theirs).
func TestModelPickerView_Renders(t *testing.T) {
	writeModelsConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "gpt-4o", Provider: "openai", BaseURL: "https://api.openai.com"},
			{Model: "deepseek-v4-flash", Provider: "deepseek", BaseURL: "https://api.deepseek.com"},
		},
		DefaultModel: "gpt-4o",
	})

	m := newPickerTestModel("gpt-4o")
	m.dispatchModel("")

	// Initial focus is on the openai endpoint (where gpt-4o lives).
	out := m.modelPickerView()
	if !strings.Contains(out, "gpt-4o") {
		t.Errorf("picker view missing focused model gpt-4o:\n%s", out)
	}
	if !strings.Contains(out, "https://api.openai.com") {
		t.Errorf("picker view missing focused endpoint's base URL:\n%s", out)
	}
	if !strings.Contains(out, "https://api.deepseek.com") {
		t.Errorf("picker view missing other endpoint's header:\n%s", out)
	}
	if !strings.Contains(out, "Switch model") {
		t.Errorf("picker view missing prompt:\n%s", out)
	}
	if !strings.Contains(out, "←/→ endpoint") {
		t.Errorf("picker view missing ←→ hint:\n%s", out)
	}

	// ←→ moves focus to the deepseek endpoint; its models now appear.
	m = pickUpdate(m, tea.KeyMsg{Type: tea.KeyRight})
	out = m.modelPickerView()
	if !strings.Contains(out, "deepseek-v4-flash") {
		t.Errorf("picker view missing deepseek-v4-flash after →:\n%s", out)
	}
}

// TestModelPicker_TwoLevelEndpointSwitch exercises the PR4b ←→ keys: with two
// endpoints, ←/→ cycle the endpoint cursor and reset the model cursor to 0,
// and Enter on the second endpoint's model dispatches a composite id that
// resolves to that endpoint's connection params.
func TestModelPicker_TwoLevelEndpointSwitch(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-deepseek-key")
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	writeModelsConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "gpt-4o", Provider: "openai", BaseURL: "https://api.openai.com", APIKey: "sk-openai"},
			{Model: "deepseek-v4-flash", Provider: "deepseek", BaseURL: "https://api.deepseek.com", APIKey: "sk-deepseek"},
		},
		DefaultModel: "gpt-4o",
	})

	m := newPickerTestModel("gpt-4o")
	m.dispatchModel("")

	if len(m.modelPicker.endpoints) != 2 {
		t.Fatalf("endpoints = %d, want 2: %+v", len(m.modelPicker.endpoints), m.modelPicker.endpoints)
	}
	// Start: focused on the openai endpoint (where gpt-4o lives), model idx 0.
	if m.modelPicker.endpoints[m.modelPicker.epIdx].items[0].name != "gpt-4o" {
		t.Errorf("initial endpoint = %q, want the one with gpt-4o", m.modelPicker.endpoints[m.modelPicker.epIdx].items[0].name)
	}

	// → moves to the deepseek endpoint and resets model idx to 0.
	m = pickUpdate(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.modelPicker.endpoints[m.modelPicker.epIdx].items[0].name != "deepseek-v4-flash" {
		t.Errorf("after →: endpoint = %q, want deepseek endpoint",
			m.modelPicker.endpoints[m.modelPicker.epIdx].items[0].name)
	}
	if m.modelPicker.idx != 0 {
		t.Errorf("after →: model idx = %d, want 0 (reset on endpoint switch)", m.modelPicker.idx)
	}

	// → again wraps back to the openai endpoint.
	m = pickUpdate(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.modelPicker.endpoints[m.modelPicker.epIdx].items[0].name != "gpt-4o" {
		t.Errorf("after →→ (wrap): endpoint = %q, want gpt-4o endpoint",
			m.modelPicker.endpoints[m.modelPicker.epIdx].items[0].name)
	}

	// ← wraps to the deepseek endpoint (the last one).
	m = pickUpdate(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.modelPicker.endpoints[m.modelPicker.epIdx].items[0].name != "deepseek-v4-flash" {
		t.Errorf("after ← (wrap): endpoint = %q, want deepseek endpoint",
			m.modelPicker.endpoints[m.modelPicker.epIdx].items[0].name)
	}

	// Enter on deepseek-v4-flash dispatches the composite id.
	m = pickUpdate(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.modelPicker != nil {
		t.Fatal("picker should be cleared after Enter")
	}
	if m.a.Model != "legacy-api-deepseek-com-0::deepseek-v4-flash" {
		t.Errorf("active model = %q, want legacy-api-deepseek-com-0::deepseek-v4-flash", m.a.Model)
	}
}

// TestDispatchModel_PickerSingleModel verifies the picker opens and behaves
// correctly when only one model is configured (wrap arithmetic is a no-op).
func TestDispatchModel_PickerSingleModel(t *testing.T) {
	writeModelsConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "gpt-4o", Provider: "openai"},
		},
		DefaultModel: "gpt-4o",
	})

	m := newPickerTestModel("gpt-4o")
	m.dispatchModel("")

	if m.modelPicker == nil {
		t.Fatal("picker should open even with a single model")
	}
	if len(m.modelPicker.items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(m.modelPicker.items))
	}
	if m.modelPicker.idx != 0 {
		t.Errorf("picker cursor = %d, want 0", m.modelPicker.idx)
	}
	// DOWN/UP should both stay at index 0 (wrap with len==1).
	m = pickUpdate(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.modelPicker.idx != 0 {
		t.Errorf("after DOWN: idx = %d, want 0", m.modelPicker.idx)
	}
	m = pickUpdate(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.modelPicker.idx != 0 {
		t.Errorf("after UP: idx = %d, want 0", m.modelPicker.idx)
	}
}

// TestModelPickerView_EmptyWhenInactive verifies the picker renders nothing
// when not active.
func TestModelPickerView_EmptyWhenInactive(t *testing.T) {
	m := newPickerTestModel("gpt-4o")
	if out := m.modelPickerView(); out != "" {
		t.Errorf("expected empty picker view when inactive, got:\n%s", out)
	}
}

// TestDispatchModel_DefaultFlag verifies that `/model <name> --default` sets
// the model as default in the persisted config.
func TestDispatchModel_DefaultFlag(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-deepseek-key")
	writeModelsConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "gpt-4o", Provider: "openai"},
			{Model: "deepseek-v4-flash", Provider: "deepseek", BaseURL: "https://api.deepseek.com"},
		},
		DefaultModel: "gpt-4o",
	})

	m := newPickerTestModel("gpt-4o")
	m.dispatchModel("deepseek-v4-flash --default")

	if m.a.Model != "deepseek-v4-flash" {
		t.Errorf("active model = %q, want deepseek-v4-flash", m.a.Model)
	}

	saved, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if saved.DefaultModel != "deepseek-v4-flash" {
		t.Errorf("default_model = %q, want deepseek-v4-flash", saved.DefaultModel)
	}
}

// TestDispatchModel_DefaultFlagUnconfigured verifies `/model <unknown> --default`
// errors on the switch step and does NOT touch the config.
func TestDispatchModel_DefaultFlagUnconfigured(t *testing.T) {
	writeModelsConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "gpt-4o", Provider: "openai"},
		},
		DefaultModel: "gpt-4o",
	})

	m := newPickerTestModel("gpt-4o")
	m.dispatchModel("nonexistent --default")

	// Model should NOT have switched.
	if m.a.Model != "gpt-4o" {
		t.Errorf("active model = %q, want gpt-4o (unchanged)", m.a.Model)
	}
	saved, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if saved.DefaultModel != "gpt-4o" {
		t.Errorf("default_model = %q, want gpt-4o (unchanged)", saved.DefaultModel)
	}
}

// TestDispatchModel_NoDefaultFlag verifies `/model <name>` (no --default) does
// NOT change the persisted default.
func TestDispatchModel_NoDefaultFlag(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-deepseek-key")
	writeModelsConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "deepseek-v4-flash", Provider: "deepseek", BaseURL: "https://api.deepseek.com"},
			{Model: "gpt-4o", Provider: "openai"},
		},
		DefaultModel: "gpt-4o",
	})

	m := newPickerTestModel("gpt-4o")
	m.dispatchModel("deepseek-v4-flash")

	if m.a.Model != "deepseek-v4-flash" {
		t.Errorf("active model = %q, want deepseek-v4-flash", m.a.Model)
	}
	saved, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if saved.DefaultModel != "gpt-4o" {
		t.Errorf("default_model = %q, want gpt-4o (unchanged)", saved.DefaultModel)
	}
}

// TestDispatchModel_DefaultFlagOnly verifies `/model --default` (flag without
// a model name) errors cleanly and does not touch the config.
func TestDispatchModel_DefaultFlagOnly(t *testing.T) {
	writeModelsConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "gpt-4o", Provider: "openai"},
		},
		DefaultModel: "gpt-4o",
	})

	m := newPickerTestModel("gpt-4o")
	m.dispatchModel("--default")

	// Switch should fail (no model), default should remain unchanged.
	if m.a.Model != "gpt-4o" {
		t.Errorf("active model = %q, want gpt-4o (unchanged)", m.a.Model)
	}
	saved, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if saved.DefaultModel != "gpt-4o" {
		t.Errorf("default_model = %q, want gpt-4o (unchanged)", saved.DefaultModel)
	}
}
