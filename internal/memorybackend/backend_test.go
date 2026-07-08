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

func TestConfigNamespaceDefault(t *testing.T) {
	if got := (Config{}).namespace(); got != "default" {
		t.Errorf("empty Namespace: got %q, want %q", got, "default")
	}
	if got := (Config{Namespace: "octo-agent"}).namespace(); got != "octo-agent" {
		t.Errorf("explicit Namespace: got %q, want %q", got, "octo-agent")
	}
}
