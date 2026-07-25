package agentprofile

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	base := func() *Profile {
		return &Profile{ID: "code-review", Description: "reviews code"}
	}
	tests := []struct {
		name    string
		mutate  func(*Profile)
		wantErr string
	}{
		{"ok", func(p *Profile) {}, ""},
		{"id empty", func(p *Profile) { p.ID = "" }, "invalid id"},
		{"id uppercase", func(p *Profile) { p.ID = "Code" }, "invalid id"},
		{"id underscore", func(p *Profile) { p.ID = "code_review" }, "invalid id"},
		{"id leading dash", func(p *Profile) { p.ID = "-code" }, "invalid id"},
		{"id too long", func(p *Profile) { p.ID = strings.Repeat("a", 33) }, "invalid id"},
		{"id 32 chars ok", func(p *Profile) { p.ID = strings.Repeat("a", 32) }, ""},
		{"description required", func(p *Profile) { p.Description = "  " }, "description is required"},
		{"name too long", func(p *Profile) { p.Name = strings.Repeat("n", 33) }, "name too long"},
		{"name 32 CJK chars ok", func(p *Profile) { p.Name = strings.Repeat("审", 32) }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := base()
			tt.mutate(p)
			err := p.Validate()
			if tt.wantErr == "" && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Validate() = %v, want error containing %q", err, tt.wantErr)
				}
			}
		})
	}
}

func TestDefaultProfile(t *testing.T) {
	p := DefaultProfile()
	if !p.IsDefault() || p.Source != SourceBuiltin {
		t.Fatalf("DefaultProfile() = %+v", p)
	}
	if (&Profile{ID: "x"}).IsDefault() {
		t.Fatal("non-default profile claims IsDefault")
	}
}
