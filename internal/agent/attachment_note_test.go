package agent

import (
	"reflect"
	"testing"
)

func TestAttachmentNoteRoundTrip(t *testing.T) {
	note := AttachmentNote("/home/u/.octo/uploads/123_report.pdf")
	cleaned, names := StripAttachmentNotes(note)
	if cleaned != "" {
		t.Errorf("cleaned = %q, want empty (note-only text)", cleaned)
	}
	if !reflect.DeepEqual(names, []string{"123_report.pdf"}) {
		t.Errorf("names = %v, want [123_report.pdf]", names)
	}
}

func TestStripAttachmentNotes(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantCleaned string
		wantNames   []string
	}{
		{
			name:        "note appended to text",
			in:          "analyze this\n\n[Attached file: /x/y/data.csv]",
			wantCleaned: "analyze this",
			wantNames:   []string{"data.csv"},
		},
		{
			name:        "multiple notes",
			in:          "compare them\n\n[Attached file: /a/q1.xlsx]\n[Attached file: /a/q2.xlsx]",
			wantCleaned: "compare them",
			wantNames:   []string{"q1.xlsx", "q2.xlsx"},
		},
		{
			name:        "note only",
			in:          "[Attached file: /tmp/notes.txt]",
			wantCleaned: "",
			wantNames:   []string{"notes.txt"},
		},
		{
			name:        "upload timestamp prefix stripped for display",
			in:          "[Attached file: /home/u/.octo/uploads/1720000000000000000_report.pdf]",
			wantCleaned: "",
			wantNames:   []string{"report.pdf"},
		},
		{
			name:        "short numeric prefix is kept (not an upload stamp)",
			in:          "[Attached file: /data/2024_budget.xlsx]",
			wantCleaned: "",
			wantNames:   []string{"2024_budget.xlsx"},
		},
		{
			name:        "path containing a bracket is not truncated",
			in:          "[Attached file: /data/wei]rd.pdf]",
			wantCleaned: "",
			wantNames:   []string{"wei]rd.pdf"},
		},
		{
			name:        "no note is byte-identical",
			in:          "just a normal message",
			wantCleaned: "just a normal message",
			wantNames:   nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cleaned, names := StripAttachmentNotes(tc.in)
			if cleaned != tc.wantCleaned {
				t.Errorf("cleaned = %q, want %q", cleaned, tc.wantCleaned)
			}
			if !reflect.DeepEqual(names, tc.wantNames) {
				t.Errorf("names = %v, want %v", names, tc.wantNames)
			}
		})
	}
}
