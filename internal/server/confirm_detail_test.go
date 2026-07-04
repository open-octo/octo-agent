package server

import "testing"

// TestBuildConfirmDetail_Terminal pins #1105: the permission modal used to
// show only "Allow terminal?" with the command discarded, so the user
// authorized blind. The confirmation event must now carry the literal
// command.
func TestBuildConfirmDetail_Terminal(t *testing.T) {
	d := buildConfirmDetail("terminal", map[string]any{"command": "rm -rf /tmp/foo"})
	if d.Command != "rm -rf /tmp/foo" {
		t.Errorf("Command = %q, want the literal command", d.Command)
	}
	if d.Diff != "" || d.Input != "" {
		t.Errorf("terminal detail should only set Command, got Diff=%q Input=%q", d.Diff, d.Input)
	}
}

// TestBuildConfirmDetail_EditFile pins #1105 for edit_file: the modal must
// render the pending change, not just the bare tool name.
func TestBuildConfirmDetail_EditFile(t *testing.T) {
	d := buildConfirmDetail("edit_file", map[string]any{
		"path":       "/tmp/foo.go",
		"old_string": "foo",
		"new_string": "bar",
	})
	want := "- foo\n+ bar"
	if d.Diff != want {
		t.Errorf("Diff = %q, want %q", d.Diff, want)
	}
	if d.Command != "" || d.Input != "" {
		t.Errorf("edit_file detail should only set Diff, got Command=%q Input=%q", d.Command, d.Input)
	}
}

// TestBuildConfirmDetail_OtherTool pins #1105 for tools with no dedicated
// preview: input must be surfaced (sorted, so the modal is deterministic),
// not silently dropped.
func TestBuildConfirmDetail_OtherTool(t *testing.T) {
	d := buildConfirmDetail("some_mcp_tool", map[string]any{"b": "2", "a": "1"})
	want := "a: 1\nb: 2"
	if d.Input != want {
		t.Errorf("Input = %q, want %q", d.Input, want)
	}
}

func TestFormatToolInputForConfirm(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		if got := formatToolInputForConfirm(nil); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("multi-line value folds after 4 lines", func(t *testing.T) {
		lines := "l1\nl2\nl3\nl4\nl5\nl6"
		got := formatToolInputForConfirm(map[string]any{"body": lines})
		want := "body:\n  l1\n  l2\n  l3\n  l4\n  … +2 more lines"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("long single line is capped, not left to blow up the modal", func(t *testing.T) {
		long := ""
		for i := 0; i < 300; i++ {
			long += "x"
		}
		got := formatToolInputForConfirm(map[string]any{"k": long})
		if len(got) >= len(long) {
			t.Errorf("expected the 300-rune value to be capped, got length %d", len(got))
		}
	})
}
