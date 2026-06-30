package browser

import (
	"strings"
	"testing"
)

func TestExtractPseudos(t *testing.T) {
	cases := []struct {
		in      string
		base    string
		needle  string
		exact   bool
		visible bool
	}{
		{`a`, `a`, ``, false, false},
		{`a:has-text("Buy now")`, `a`, `Buy now`, false, false},
		{`div > a:contains('提交')`, `div > a`, `提交`, false, false},
		{`button:text-is("OK")`, `button`, `OK`, true, false},
		{`a:has-text("x"):visible`, `a`, `x`, false, true},
		{`button:visible`, `button`, ``, false, true},
		{`:has-text("only text")`, ``, `only text`, false, false},
		{`a:has-text("with (paren)")`, `a`, `with (paren)`, false, false},
	}
	for _, c := range cases {
		base, needle, exact, visible := extractPseudos(c.in)
		if base != c.base || needle != c.needle || exact != c.exact || visible != c.visible {
			t.Errorf("extractPseudos(%q) = (%q,%q,%v,%v), want (%q,%q,%v,%v)",
				c.in, base, needle, exact, visible, c.base, c.needle, c.exact, c.visible)
		}
	}
}

func TestResolveSelectorJS_Routing(t *testing.T) {
	cases := []struct {
		in   string
		want string // a fragment the generated JS must contain
	}{
		{`#id`, `document.querySelector(`},                 // plain CSS untouched
		{`a:has-text("hi")`, `querySelectorAll(`},          // text → scan
		{`text=Login`, `querySelectorAll("*")`},            // text= engine
		{`xpath=//a[@id]`, `document.evaluate(`},           // xpath engine
		{`css=div.card`, `document.querySelector("div.card")`}, // css= prefix stripped
		{`button:visible`, `getClientRects`},               // :visible → scan w/ visibility
	}
	for _, c := range cases {
		got := resolveSelectorJS("document", c.in)
		if !strings.Contains(got, c.want) {
			t.Errorf("resolveSelectorJS(%q) = %q, want substring %q", c.in, got, c.want)
		}
	}
}

func TestUnquote(t *testing.T) {
	cases := []struct {
		in     string
		out    string
		quoted bool
	}{
		{`"x"`, `x`, true},
		{`'y'`, `y`, true},
		{`bare`, `bare`, false},
		{`"a\"b"`, `a"b`, true},
	}
	for _, c := range cases {
		out, q := unquote(c.in)
		if out != c.out || q != c.quoted {
			t.Errorf("unquote(%q) = (%q,%v), want (%q,%v)", c.in, out, q, c.out, c.quoted)
		}
	}
}
