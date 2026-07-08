package memorybackend

import "testing"

func TestNewDispatchesByType(t *testing.T) {
	cases := []struct {
		typ  string
		want string
	}{
		{"hindsight", "hindsight"},
		{"mem0", "mem0"},
		{"memos", "memos"},
	}
	for _, c := range cases {
		b, err := New(Config{Type: c.typ, BaseURL: "http://localhost:8888"})
		if err != nil {
			t.Fatalf("New(%q): unexpected error: %v", c.typ, err)
		}
		if got := b.Name(); got != c.want {
			t.Errorf("New(%q).Name() = %q, want %q", c.typ, got, c.want)
		}
	}
}

func TestNewRejectsUnknownType(t *testing.T) {
	if _, err := New(Config{Type: "unknown", BaseURL: "http://localhost:8888"}); err == nil {
		t.Fatal("New(unknown type): want error, got nil")
	}
}

func TestNewRequiresBaseURL(t *testing.T) {
	if _, err := New(Config{Type: "hindsight"}); err == nil {
		t.Fatal("New(no base_url): want error, got nil")
	}
}

func TestNewRequiresBaseURLForSelfHostedMem0(t *testing.T) {
	if _, err := New(Config{Type: "mem0"}); err == nil {
		t.Fatal("New(mem0, no mode, no base_url): want error, got nil")
	}
}

func TestNewDefaultsBaseURLForMem0Cloud(t *testing.T) {
	b, err := New(Config{Type: "mem0", Mode: "cloud"})
	if err != nil {
		t.Fatalf("New(mem0 cloud, no base_url): unexpected error: %v", err)
	}
	m, ok := b.(*mem0Backend)
	if !ok {
		t.Fatalf("New(mem0 cloud) returned %T, want *mem0Backend", b)
	}
	if m.cfg.BaseURL != mem0CloudBaseURL {
		t.Errorf("BaseURL = %q, want the mem0 Cloud default %q", m.cfg.BaseURL, mem0CloudBaseURL)
	}
}

func TestNewDoesNotOverrideExplicitBaseURLForMem0Cloud(t *testing.T) {
	b, err := New(Config{Type: "mem0", Mode: "cloud", BaseURL: "https://staging.mem0.example"})
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	m := b.(*mem0Backend)
	if m.cfg.BaseURL != "https://staging.mem0.example" {
		t.Errorf("BaseURL = %q, want the explicitly configured value preserved", m.cfg.BaseURL)
	}
}

func TestConfigNamespaceDefault(t *testing.T) {
	if got := (Config{}).namespace(); got != "default" {
		t.Errorf("empty Namespace: got %q, want %q", got, "default")
	}
	if got := (Config{Namespace: "octo-agent"}).namespace(); got != "octo-agent" {
		t.Errorf("explicit Namespace: got %q, want %q", got, "octo-agent")
	}
}
