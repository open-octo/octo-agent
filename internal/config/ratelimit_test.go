package config

import (
	"strings"
	"testing"
)

func rateLimitedConfig() Config {
	return Config{
		Endpoints: []Endpoint{
			{ID: "free-tier", Provider: "custom", BaseURL: "https://free.example", Protocol: "openai",
				RPM: 8, MaxConcurrency: 1,
				Models: []EndpointModel{{Model: "glm-4-flash"}}},
		},
	}
}

// TestEntryByModel_ProjectsRateLimits covers both resolution paths, because
// sender construction reads the limits off the projected ModelEntry: a path
// that drops them silently leaves that endpoint ungated.
func TestEntryByModel_ProjectsRateLimits(t *testing.T) {
	cfg := rateLimitedConfig()
	for _, ref := range []string{"free-tier::glm-4-flash", "glm-4-flash"} {
		got, ok := cfg.EntryByModel(ref)
		if !ok {
			t.Fatalf("EntryByModel(%q) = false, want true", ref)
		}
		if got.RPM != 8 || got.MaxConcurrency != 1 {
			t.Errorf("EntryByModel(%q) rpm/max_concurrency = %d/%d, want 8/1", ref, got.RPM, got.MaxConcurrency)
		}
	}
}

func TestDefaultEntry_ProjectsRateLimits(t *testing.T) {
	got := rateLimitedConfig().DefaultEntry()
	if got.RPM != 8 || got.MaxConcurrency != 1 {
		t.Errorf("DefaultEntry() rpm/max_concurrency = %d/%d, want 8/1", got.RPM, got.MaxConcurrency)
	}
}

// TestRateLimitsSurviveSaveReload is the guard for the hand-edit workflow:
// these fields have no UI, so a config rewritten by any other setting change
// must carry them through untouched.
func TestRateLimitsSurviveSaveReload(t *testing.T) {
	setHome(t)
	cfg := rateLimitedConfig()
	cfg.Default = "free-tier::glm-4-flash"
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(reloaded.Endpoints) != 1 {
		t.Fatalf("reloaded %d endpoints, want 1", len(reloaded.Endpoints))
	}
	if got := reloaded.Endpoints[0]; got.RPM != 8 || got.MaxConcurrency != 1 {
		t.Errorf("after save+reload rpm/max_concurrency = %d/%d, want 8/1", got.RPM, got.MaxConcurrency)
	}
}

func TestLoad_ParsesRateLimitKeys(t *testing.T) {
	home := setHome(t)
	writeOcto(t, home, "config.yml", strings.Join([]string{
		"endpoints:",
		"  - id: free-tier",
		"    provider: custom",
		"    base_url: https://free.example",
		"    protocol: openai",
		"    rpm: 8",
		"    max_concurrency: 1",
		"    models:",
		"      - model: glm-4-flash",
		"        vision: false",
		"default: free-tier::glm-4-flash",
		"",
	}, "\n"))
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Endpoints[0]; got.RPM != 8 || got.MaxConcurrency != 1 {
		t.Errorf("rpm/max_concurrency = %d/%d, want 8/1", got.RPM, got.MaxConcurrency)
	}
}

// TestValidate_RejectsOutOfRangeRateLimits: a hand edit is the only way these
// fields get written, so Validate is the only thing standing between a typo
// and either a silently ungated endpoint (negative) or an rpm that sizes a
// multi-gigabyte allocation in the limiter (a stray extra digit).
func TestValidate_RejectsOutOfRangeRateLimits(t *testing.T) {
	for _, tc := range []struct {
		name string
		ep   Endpoint
	}{
		{"negative", Endpoint{ID: "ep", Provider: "anthropic", RPM: -1, MaxConcurrency: -2,
			Models: []EndpointModel{{Model: "claude-sonnet-5"}}}},
		{"absurdly large", Endpoint{ID: "ep", Provider: "anthropic", RPM: 100_000_001, MaxConcurrency: 100_000_001,
			Models: []EndpointModel{{Model: "claude-sonnet-5"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := strings.Join(Config{Endpoints: []Endpoint{tc.ep}}.Validate(), "\n")
			if !strings.Contains(problems, "out-of-range rpm") {
				t.Errorf("Validate() = %q, want an out-of-range rpm problem", problems)
			}
			if !strings.Contains(problems, "out-of-range max_concurrency") {
				t.Errorf("Validate() = %q, want an out-of-range max_concurrency problem", problems)
			}
		})
	}
	if p := rateLimitedConfig().Validate(); len(p) != 0 {
		t.Errorf("Validate() on a valid rate-limited config = %v, want no problems", p)
	}
}
