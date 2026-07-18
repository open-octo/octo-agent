package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/browser"
	"github.com/open-octo/octo-agent/internal/skills"
)

func TestSplitSkillKind(t *testing.T) {
	cases := []struct{ in, kind, bare string }{
		{"foo", "", "foo"},
		{"recording:foo", "recording", "foo"},
		{"browser:foo", "browser", "foo"},
		{"md:foo", "md", "foo"},
		{"md:", "md", ""},
	}
	for _, c := range cases {
		k, b := splitSkillKind(c.in)
		if k != c.kind || b != c.bare {
			t.Errorf("splitSkillKind(%q) = (%q,%q), want (%q,%q)", c.in, k, b, c.kind, c.bare)
		}
	}
}

func TestParseSkillParams(t *testing.T) {
	m, err := parseSkillParams(`{"month":"2026-06","n":3,"files":["a","b"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if m["month"] != "2026-06" {
		t.Errorf("month = %#v", m["month"])
	}
	if n, _ := m["n"].(float64); n != 3 {
		t.Errorf("n = %#v, want number 3", m["n"])
	}
	// An array value stays structured (not flattened to a string).
	if f, ok := m["files"].([]any); !ok || len(f) != 2 {
		t.Errorf("files = %#v, want a 2-element array", m["files"])
	}
	for _, empty := range []string{"", "null", "{}"} {
		m, err := parseSkillParams(empty)
		if err != nil || len(m) != 0 {
			t.Fatalf("parseSkillParams(%q) = %#v, %v", empty, m, err)
		}
	}
	if _, err := parseSkillParams(`[1,2]`); err == nil {
		t.Fatal("want error for a non-object params value")
	}
}

func TestStringifyParam(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"2026-06", "2026-06"},
		{float64(3), "3"},
		{true, "true"},
		{nil, ""},
		{[]any{"a", "b"}, `["a","b"]`},          // array → JSON, not "[a b]"
		{map[string]any{"k": "v"}, `{"k":"v"}`}, // object → JSON
	}
	for _, c := range cases {
		if got := stringifyParam(c.in); got != c.want {
			t.Errorf("stringifyParam(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// captureSpawner records the last request and returns a canned reply.
type captureSpawner struct {
	last  SpawnRequest
	reply string
}

func (f *captureSpawner) Spawn(_ context.Context, req SpawnRequest) (SpawnResult, error) {
	f.last = req
	return SpawnResult{Reply: f.reply, OutputTokens: 2}, nil
}

func (f *captureSpawner) Continue(_ context.Context, _, _ string) (SpawnResult, error) {
	return SpawnResult{}, nil
}

func writeSkillMD(t *testing.T, cwd, name, desc, body string) {
	t.Helper()
	dir := filepath.Join(cwd, ".octo", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\ndescription: " + desc + "\n---\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchWorkflowSkill_Routing(t *testing.T) {
	bdir := t.TempDir()
	t.Setenv("OCTO_BROWSER_SKILLS_DIR", bdir)
	saveRec := func(name string) {
		if err := browser.SaveRecording(filepath.Join(bdir, name+".yaml"),
			browser.Recording{Name: name, Steps: []browser.Step{{Action: "navigate", URL: "x"}}}); err != nil {
			t.Fatal(err)
		}
	}
	saveRec("wf_c_dl")   // browser only
	saveRec("wf_c_proc") // also an md skill below → ambiguous

	cwd := t.TempDir()
	writeSkillMD(t, cwd, "wf_c_proc", "process a table", "do the thing")
	prev := activeSkills
	SetSkills(skills.Discover(cwd))
	defer SetSkills(prev)

	fs := &captureSpawner{reply: "done"}
	ctx := context.Background()
	cases := []struct{ name, want string }{
		{"nope", "not found"},
		{"browser:nope", "no browser recording"},
		{"recording:nope", "no browser recording"},
		{"md:wf_c_dl", "no SKILL.md"},
	}
	for _, c := range cases {
		r := dispatchWorkflowSkill(ctx, fs, c.name, "{}", "")
		if r.Err == nil || !strings.Contains(r.Err.Error(), c.want) {
			t.Errorf("dispatch(%q).Err = %v, want it to contain %q", c.name, r.Err, c.want)
		}
	}

	// A name existing as BOTH a recording and a SKILL.md skill now resolves to
	// the skill deterministically (it used to be an ambiguity error).
	fs.last = SpawnRequest{}
	r := dispatchWorkflowSkill(ctx, fs, "wf_c_proc", "{}", "")
	if r.Err != nil {
		t.Fatalf("both-kinds name should resolve to the SKILL.md skill, got %v", r.Err)
	}
	if !strings.Contains(fs.last.Prompt, "do the thing") {
		t.Errorf("expected the md skill body to be spawned, got %q", fs.last.Prompt)
	}

	// Legacy unprefixed-to-recording resolution still routes to a replay. Inject a
	// headless session when Chrome is available (so the replay fails fast on the
	// fixture's bogus navigate URL — and we never attach to the user's real
	// browser); without Chrome the connect error wraps the same way. Either path
	// proves the name routed to a replay rather than falling through to not-found.
	if browser.ChromeAvailable("") {
		b, err := browser.Launch(ctx, browser.LaunchOptions{Headless: true})
		if err == nil {
			defer b.Close()
			if page, err := b.NewPage(ctx, "about:blank"); err == nil {
				SetBrowserSession(b, page)
				defer ResetBrowserSession()
			}
		}
	}
	r = dispatchWorkflowSkill(ctx, fs, "wf_c_dl", "{}", "")
	if r.Err == nil || !strings.Contains(r.Err.Error(), "wf_c_dl") || strings.Contains(r.Err.Error(), "not found") {
		t.Errorf("unprefixed recording name should route to the replay path, got %v", r.Err)
	}
}

func TestDispatchWorkflowRecording_NotFound(t *testing.T) {
	t.Setenv("OCTO_BROWSER_SKILLS_DIR", t.TempDir())
	r := dispatchWorkflowRecording(context.Background(), "nope", nil)
	if r.Err == nil || !strings.Contains(r.Err.Error(), "no browser recording by that name") {
		t.Fatalf("dispatchWorkflowRecording(nope).Err = %v", r.Err)
	}
}

func TestDispatchWorkflowSkill_MD(t *testing.T) {
	t.Setenv("OCTO_BROWSER_SKILLS_DIR", t.TempDir()) // no browser recordings
	cwd := t.TempDir()
	writeSkillMD(t, cwd, "wf_c_only", "merge excels", "MERGE THE FILES")
	prev := activeSkills
	SetSkills(skills.Discover(cwd))
	defer SetSkills(prev)

	// No schema → free-text reply JSON-encoded to a string; body + inputs reach
	// the sub-agent prompt.
	fs := &captureSpawner{reply: "merged.xlsx"}
	r := dispatchWorkflowSkill(context.Background(), fs, "wf_c_only", `{"dir":"/in"}`, "")
	if r.Err != nil {
		t.Fatalf("dispatch: %v", r.Err)
	}
	if r.Reply != `"merged.xlsx"` {
		t.Errorf("no-schema reply should be a JSON string, got %q", r.Reply)
	}
	if !strings.Contains(fs.last.Prompt, "MERGE THE FILES") || !strings.Contains(fs.last.Prompt, `"dir":"/in"`) {
		t.Errorf("prompt missing body or inputs: %q", fs.last.Prompt)
	}

	// An array param (a file[] handed from an upstream skill) must reach the
	// sub-agent as structured JSON, not a flattened "[a b]" blob.
	fsArr := &captureSpawner{reply: "ok"}
	if r := dispatchWorkflowSkill(context.Background(), fsArr, "wf_c_only", `{"inputs":["/a.xlsx","/b.xlsx"]}`, ""); r.Err != nil {
		t.Fatalf("dispatch array: %v", r.Err)
	}
	if !strings.Contains(fsArr.last.Prompt, `"inputs":["/a.xlsx","/b.xlsx"]`) {
		t.Errorf("array param not preserved as JSON in prompt: %q", fsArr.last.Prompt)
	}

	// With schema → reply passes through raw and the schema is forwarded.
	fs2 := &captureSpawner{reply: `{"path":"/out/m.xlsx"}`}
	r2 := dispatchWorkflowSkill(context.Background(), fs2, "wf_c_only", "{}", `{"type":"object"}`)
	if r2.Err != nil {
		t.Fatalf("dispatch2: %v", r2.Err)
	}
	if r2.Reply != `{"path":"/out/m.xlsx"}` {
		t.Errorf("schema reply should pass through unchanged, got %q", r2.Reply)
	}
	if fs2.last.Schema != `{"type":"object"}` {
		t.Errorf("schema not forwarded to spawner: %q", fs2.last.Schema)
	}
}
