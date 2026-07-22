package browser

import (
	"encoding/json"
	"testing"
)

// TestEvalExceptionMessage checks that a thrown exception is rendered with its
// real detail rather than a bare "Uncaught". Inputs are the raw CDP
// exceptionDetails shapes Chrome sends for each throw form.
func TestEvalExceptionMessage(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{
			name: "error object uses description first line, not stack",
			json: `{"text":"Uncaught","exception":{"className":"ReferenceError","description":"ReferenceError: LEGS is not defined\n    at <anonymous>:1:1"}}`,
			want: "ReferenceError: LEGS is not defined",
		},
		{
			name: "thrown string primitive uses value",
			json: `{"text":"Uncaught","exception":{"value":"boom"}}`,
			want: "boom",
		},
		{
			name: "class name only",
			json: `{"text":"Uncaught","exception":{"className":"TypeError"}}`,
			want: "TypeError",
		},
		{
			name: "non-Uncaught text keeps prefix",
			json: `{"text":"Syntax error","exception":{"description":"SyntaxError: Unexpected token"}}`,
			want: "Syntax error: SyntaxError: Unexpected token",
		},
		{
			name: "no exception object falls back to text",
			json: `{"text":"Uncaught SyntaxError: missing ) after argument list"}`,
			want: "Uncaught SyntaxError: missing ) after argument list",
		},
		{
			name: "empty exceptionDetails",
			json: `{}`,
			want: "unknown error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var e evalException
			if err := json.Unmarshal([]byte(tc.json), &e); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := e.message(); got != tc.want {
				t.Errorf("message() = %q, want %q", got, tc.want)
			}
		})
	}
}
