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
// opens the picker overlay (instead of printing a usage error).
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

	if model.(*tuiModel).modelPicker == nil {
		t.Fatal("expected picker to be open after dispatchModel(\"\")")
	}
	if len(model.(*tuiModel).modelPicker.items) != 2 {
		t.Errorf("picker items = %d, want 2", len(model.(*tuiModel).modelPicker.items))
	}
	// Cursor should start on the active model (gpt-4o = index 0).
	if model.(*tuiModel).modelPicker.idx != 0 {
		t.Errorf("picker cursor = %d, want 0 (active model)", model.(*tuiModel).modelPicker.idx)
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

	if m.modelPicker.idx != 1 {
		t.Errorf("picker cursor = %d, want 1 (claude-sonnet-4-6)", m.modelPicker.idx)
	}
}

// TestDispatchModel_PickerKeyDown verifies DOWN advances the cursor (with
// wrap) and UP moves it back, while the picker stays open.
func TestDispatchModel_PickerKeyDown(t *testing.T) {
	writeModelsConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "gpt-4o", Provider: "openai"},
			{Model: "claude-sonnet-4-6", Provider: "anthropic"},
		},
		DefaultModel: "gpt-4o",
	})

	m := newPickerTestModel("gpt-4o")
	m.dispatchModel("")

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
// model and clears the picker. Two models share a provider + base URL so
// ensureSender returns early (no live rebuild needed).
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

	// Move to index 1 (deepseek-v4-pro)
	m = pickUpdate(m, tea.KeyMsg{Type: tea.KeyDown})
	// Enter accepts
	m = pickUpdate(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.modelPicker != nil {
		t.Error("picker should be cleared after Enter")
	}
	if m.a.Model != "deepseek-v4-pro" {
		t.Errorf("active model = %q, want deepseek-v4-pro", m.a.Model)
	}
}

// TestModelPickerView_Renders verifies the overlay lists all models with
// provider/endpoint info.
func TestModelPickerView_Renders(t *testing.T) {
	writeModelsConfig(t, config.Config{
		Models: []config.ModelEntry{
			{Model: "gpt-4o", Provider: "openai"},
			{Model: "deepseek-v4-flash", Provider: "deepseek", BaseURL: "https://api.deepseek.com"},
		},
		DefaultModel: "gpt-4o",
	})

	m := newPickerTestModel("gpt-4o")
	m.dispatchModel("")

	out := m.modelPickerView()
	if !strings.Contains(out, "gpt-4o") {
		t.Errorf("picker view missing gpt-4o:\n%s", out)
	}
	if !strings.Contains(out, "deepseek-v4-flash") {
		t.Errorf("picker view missing deepseek-v4-flash:\n%s", out)
	}
	if !strings.Contains(out, "https://api.deepseek.com") {
		t.Errorf("picker view missing base URL:\n%s", out)
	}
	if !strings.Contains(out, "Switch model") {
		t.Errorf("picker view missing prompt:\n%s", out)
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
