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
		Endpoints: []Endpoint{{ID: "ep-a", Provider: "openai", Models: []EndpointModel{{Model: "gpt-4o"}}}},
		Default:   "ep-a::gpt-4o",
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
	if len(f.Endpoints) != 1 || len(f.Endpoints[0].Models) != 1 || f.Endpoints[0].Models[0].Model != "gpt-4o" {
		t.Errorf("backup content = %+v, want the saved config", f)
	}
}

func TestRestoreFromBackup(t *testing.T) {
	home := setHome(t)

	good := Config{
		Endpoints: []Endpoint{{ID: "ep-a", Provider: "openai", Models: []EndpointModel{{Model: "gpt-4o"}}}},
		Default:   "ep-a::gpt-4o",
	}
	if err := good.Save(); err != nil { // writes config.yml + config.yml.bak
		t.Fatalf("Save: %v", err)
	}

	// Now corrupt config.yml the way a hand edit would.
	path, _ := Path()
	if err := os.WriteFile(path, []byte("endpoints: [oops\n"), 0o600); err != nil {
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
	if got.Default != "ep-a::gpt-4o" || len(got.Endpoints) != 1 {
		t.Errorf("restored config = %+v, want the good one", got)
	}
	_ = home
}

func TestRestoreFromBackup_NoBackup(t *testing.T) {
	setHome(t)
	writeOcto(t, os.Getenv("HOME"), "config.yml", "endpoints: [oops\n")
	if _, err := RestoreFromBackup(); err == nil {
		t.Fatal("RestoreFromBackup with no .bak should error")
	}
}

func TestRestoreFromBackup_InvalidBackup(t *testing.T) {
	home := setHome(t)
	writeOcto(t, home, "config.yml", "endpoints: [oops\n")
	writeOcto(t, home, "config.yml.bak", "also: [broken\n")
	if _, err := RestoreFromBackup(); err == nil {
		t.Fatal("RestoreFromBackup with an invalid .bak should error")
	}
}

// TestRepair_EndpointLevel covers the endpoint-level auto-fixes from design
// §14.2: dangling Default resets to the first endpoint's first model;
// dangling Lite clears; an endpoint with no models is deleted (it's unusable
// and nothing references it); Lite == Default clears Lite.
//
// PR5: the "pure new schema" guard from PR1 is lifted — Config.Models is
// deleted, so every config goes through the endpoint-level Repair. Unfixable
// cases (duplicate endpoint id, illegal id chars, no model name, duplicate
// model within an endpoint) are reported for the user rather than guessed.
func TestRepair_EndpointLevel_DanglingDefaultReset(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "ghost::claude-sonnet-4-6", // endpoint doesn't exist
	}
	repaired, fixed, unfixable := cfg.Repair()
	if len(unfixable) != 0 {
		t.Errorf("unexpected unfixable: %v", unfixable)
	}
	wantDefault := "ep-a::claude-sonnet-4-6"
	if repaired.Default != wantDefault {
		t.Errorf("Default = %q, want %q (reset to first endpoint's first model)", repaired.Default, wantDefault)
	}
	if len(fixed) == 0 || !strings.Contains(fixed[0], "reset default") {
		t.Errorf("expected a 'reset default' fixed entry, got %v", fixed)
	}
}

func TestRepair_EndpointLevel_DanglingLiteClears(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "ep-a::claude-sonnet-4-6",
		Lite:    "ghost::claude-haiku-4-5", // endpoint doesn't exist
	}
	repaired, fixed, unfixable := cfg.Repair()
	if len(unfixable) != 0 {
		t.Errorf("unexpected unfixable: %v", unfixable)
	}
	if repaired.Lite != "" {
		t.Errorf("Lite = %q, want empty (cleared, dangling)", repaired.Lite)
	}
	if len(fixed) == 0 || !strings.Contains(fixed[0], "cleared lite") {
		t.Errorf("expected a 'cleared lite' fixed entry, got %v", fixed)
	}
}

func TestRepair_EndpointLevel_LiteEqualsDefaultClears(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "ep-a::claude-sonnet-4-6",
		Lite:    "ep-a::claude-sonnet-4-6", // == Default
	}
	repaired, fixed, unfixable := cfg.Repair()
	if len(unfixable) != 0 {
		t.Errorf("unexpected unfixable: %v", unfixable)
	}
	if repaired.Lite != "" {
		t.Errorf("Lite = %q, want empty (cleared, == Default)", repaired.Lite)
	}
	if len(fixed) == 0 || !strings.Contains(fixed[0], "cleared lite") {
		t.Errorf("expected a 'cleared lite' fixed entry, got %v", fixed)
	}
}

func TestRepair_EndpointLevel_EmptyEndpointDeleted(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "ep-empty", Provider: "custom"}, // no models
			{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "ep-a::claude-sonnet-4-6",
	}
	repaired, fixed, unfixable := cfg.Repair()
	if len(unfixable) != 0 {
		t.Errorf("unexpected unfixable: %v", unfixable)
	}
	if len(repaired.Endpoints) != 1 {
		t.Errorf("Endpoints = %d entries, want 1 (empty deleted): %+v", len(repaired.Endpoints), repaired.Endpoints)
	}
	if repaired.Endpoints[0].ID != "ep-a" {
		t.Errorf("remaining endpoint = %q, want ep-a", repaired.Endpoints[0].ID)
	}
	if len(fixed) == 0 || !strings.Contains(fixed[0], "deleted") {
		t.Errorf("expected a 'deleted' fixed entry, got %v", fixed)
	}
}

func TestRepair_EndpointLevel_DuplicateIDUnfixable(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
			{ID: "ep-a", Provider: "openai", Models: []EndpointModel{{Model: "gpt-5.4"}}}, // dup
		},
	}
	_, _, unfixable := cfg.Repair()
	if len(unfixable) == 0 || !strings.Contains(unfixable[0], "duplicate endpoint id") {
		t.Errorf("expected 'duplicate endpoint id' unfixable, got %v", unfixable)
	}
}

func TestRepair_EndpointLevel_IllegalIDUnfixable(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "has space", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
	}
	_, _, unfixable := cfg.Repair()
	if len(unfixable) == 0 || !strings.Contains(unfixable[0], "illegal") {
		t.Errorf("expected 'illegal' unfixable, got %v", unfixable)
	}
}

func TestRepair_EndpointLevel_EmptyModelNameUnfixable(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: ""}}},
		},
	}
	_, _, unfixable := cfg.Repair()
	if len(unfixable) == 0 || !strings.Contains(unfixable[0], "no model name") {
		t.Errorf("expected 'no model name' unfixable, got %v", unfixable)
	}
}

func TestRepair_EndpointLevel_DuplicateModelInEndpointUnfixable(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}, {Model: "claude-sonnet-4-6"}}},
		},
	}
	_, _, unfixable := cfg.Repair()
	if len(unfixable) == 0 || !strings.Contains(unfixable[0], "duplicate model") {
		t.Errorf("expected 'duplicate model' unfixable, got %v", unfixable)
	}
}

func TestRepair_EndpointLevel_GoodConfigNoChanges(t *testing.T) {
	cfg := Config{
		Endpoints: []Endpoint{
			{ID: "ep-a", Provider: "anthropic", Models: []EndpointModel{{Model: "claude-sonnet-4-6"}}},
		},
		Default: "ep-a::claude-sonnet-4-6",
	}
	repaired, fixed, unfixable := cfg.Repair()
	if len(fixed) != 0 || len(unfixable) != 0 {
		t.Errorf("good config: fixed=%v unfixable=%v, want both empty", fixed, unfixable)
	}
	if repaired.Default != "ep-a::claude-sonnet-4-6" || len(repaired.Endpoints) != 1 {
		t.Errorf("good config mutated: %+v", repaired)
	}
}
