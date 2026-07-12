package config

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSave_WritesRollingBackup(t *testing.T) {
	setHome(t)

	cfg := Config{
		Models:       []ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "gpt-4o",
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	bak, err := BackupPath()
	if err != nil {
		t.Fatalf("BackupPath: %v", err)
	}
	got, err := os.ReadFile(bak)
	if err != nil {
		t.Fatalf("reading %s: %v", bak, err)
	}
	// The .bak is a marshaled Config, so it must itself parse back cleanly.
	var f fileConfig
	if uerr := yaml.Unmarshal(got, &f); uerr != nil {
		t.Fatalf("backup does not parse: %v", uerr)
	}
	if len(f.Models) != 1 || f.Models[0].Model != "gpt-4o" {
		t.Errorf("backup content = %+v, want the saved config", f)
	}
}

func TestRestoreFromBackup(t *testing.T) {
	home := setHome(t)

	good := Config{
		Models:       []ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "gpt-4o",
	}
	if err := good.Save(); err != nil { // writes config.yml + config.yml.bak
		t.Fatalf("Save: %v", err)
	}

	// Now corrupt config.yml the way a hand edit would.
	path, _ := Path()
	if err := os.WriteFile(path, []byte("models: [oops\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("Load() should fail on the corrupted file")
	}

	broken, err := RestoreFromBackup()
	if err != nil {
		t.Fatalf("RestoreFromBackup: %v", err)
	}
	if broken == "" || !strings.HasSuffix(broken, ".broken") {
		t.Errorf("broken-file path = %q, want a *.broken path", broken)
	}
	if _, statErr := os.Stat(broken); statErr != nil {
		t.Errorf("broken file not kept at %s: %v", broken, statErr)
	}

	// config.yml now parses again and matches the backup.
	got, err := Load()
	if err != nil {
		t.Fatalf("Load after restore: %v", err)
	}
	if got.DefaultModel != "gpt-4o" || len(got.Models) != 1 {
		t.Errorf("restored config = %+v, want the good one", got)
	}
	_ = home
}

func TestRestoreFromBackup_NoBackup(t *testing.T) {
	setHome(t)
	writeOcto(t, os.Getenv("HOME"), "config.yml", "models: [oops\n")
	if _, err := RestoreFromBackup(); err == nil {
		t.Fatal("RestoreFromBackup with no .bak should error")
	}
}

func TestRestoreFromBackup_InvalidBackup(t *testing.T) {
	home := setHome(t)
	writeOcto(t, home, "config.yml", "models: [oops\n")
	writeOcto(t, home, "config.yml.bak", "also: [broken\n")
	if _, err := RestoreFromBackup(); err == nil {
		t.Fatal("RestoreFromBackup with an invalid .bak should error")
	}
}

func TestRepair_ResetsDanglingDefaultModel(t *testing.T) {
	c := Config{
		Models:       []ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "gone",
	}
	repaired, fixed, unfixable := c.Repair()
	if len(unfixable) != 0 {
		t.Errorf("unfixable = %v, want none", unfixable)
	}
	if len(fixed) != 1 {
		t.Fatalf("fixed = %v, want 1 entry", fixed)
	}
	if repaired.DefaultModel != "gpt-4o" {
		t.Errorf("default_model = %q, want reset to gpt-4o", repaired.DefaultModel)
	}
	// Repaired config is clean.
	if p := repaired.Validate(); len(p) != 0 {
		t.Errorf("repaired config still has problems: %v", p)
	}
}

func TestRepair_ClearsDanglingLiteModel(t *testing.T) {
	c := Config{
		Models:    []ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		LiteModel: "gone",
	}
	repaired, fixed, unfixable := c.Repair()
	if len(unfixable) != 0 || len(fixed) != 1 {
		t.Fatalf("fixed=%v unfixable=%v, want 1 fixed 0 unfixable", fixed, unfixable)
	}
	if repaired.LiteModel != "" {
		t.Errorf("lite_model = %q, want cleared", repaired.LiteModel)
	}
}

func TestRepair_ReportsUnfixable(t *testing.T) {
	c := Config{
		Models: []ModelEntry{
			{Provider: "openai", Model: "gpt-4o"},
			{Provider: "openai", Model: "gpt-4o"}, // duplicate
			{Provider: "", Model: "no-provider"},  // missing provider
			{Provider: "openai", Model: ""},       // missing model name
		},
	}
	_, fixed, unfixable := c.Repair()
	if len(fixed) != 0 {
		t.Errorf("fixed = %v, want none (all problems are unfixable)", fixed)
	}
	if len(unfixable) != 3 {
		t.Errorf("unfixable = %v, want 3 (duplicate, missing provider, missing model)", unfixable)
	}
}

func TestRepair_HealthyIsNoOp(t *testing.T) {
	c := Config{
		Models:       []ModelEntry{{Provider: "openai", Model: "gpt-4o"}},
		DefaultModel: "gpt-4o",
	}
	repaired, fixed, unfixable := c.Repair()
	if len(fixed) != 0 || len(unfixable) != 0 {
		t.Errorf("healthy config: fixed=%v unfixable=%v, want none", fixed, unfixable)
	}
	if repaired.DefaultModel != "gpt-4o" {
		t.Errorf("healthy config mutated: %+v", repaired)
	}
}

func TestRepair_EmptyConfigIsNoOp(t *testing.T) {
	var c Config
	repaired, fixed, unfixable := c.Repair()
	if len(fixed) != 0 || len(unfixable) != 0 {
		t.Errorf("empty config: fixed=%v unfixable=%v, want none", fixed, unfixable)
	}
	if len(repaired.Models) != 0 {
		t.Errorf("empty config gained models: %+v", repaired)
	}
}
