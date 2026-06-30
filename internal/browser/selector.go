package browser

import (
	"fmt"
	"regexp"
	"strings"
)

// Playwright-flavored selector support. Models trained on Playwright frequently
// emit its selector dialect, so rather than reject it we translate the common
// subset to a CDP-evaluable JS query. Covered: the text engines
// (:has-text / :text / :contains / :text-is and the `text=` prefix), the
// :visible pseudo, and the `xpath=` / `css=` engine prefixes. :has() is native
// CSS (Chrome supports it) so it needs no handling. Layout selectors (:near,
// :right-of …), role=, `>>` engine chaining, and :nth-match are intentionally
// out of scope — rare in model output and disproportionate to implement.

var (
	// matches :has-text("x") / :text('x') / :contains(x) / :text-is("x"); the
	// arg is a single/double-quoted string (with escapes) or bare up to ')'.
	textPseudoRe    = regexp.MustCompile(`:(has-text|text-is|text|contains)\(\s*("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|[^)]*?)\s*\)`)
	visiblePseudoRe = regexp.MustCompile(`:visible\b`)
)

// resolveSelectorJS returns a JS expression — evaluated against doc, e.g.
// "document" or a frame's "f.contentDocument" — that yields the first matching
// element, or null. Plain CSS falls through to querySelector unchanged so the
// invalid-selector path (a real CSS typo throwing) still works.
func resolveSelectorJS(doc, sel string) string {
	sel = strings.TrimSpace(sel)
	if rest, ok := strings.CutPrefix(sel, "xpath="); ok {
		// 9 = XPathResult.FIRST_ORDERED_NODE_TYPE.
		return fmt.Sprintf(`(()=>{try{return document.evaluate(%s,%s,null,9,null).singleNodeValue;}catch(e){return null;}})()`, jsString(rest), doc)
	}
	if rest, ok := strings.CutPrefix(sel, "css="); ok {
		sel = strings.TrimSpace(rest)
	} else if rest, ok := strings.CutPrefix(sel, "text="); ok {
		// `text="x"` is exact; `text=x` is a substring match.
		needle, quoted := unquote(strings.TrimSpace(rest))
		return scanJS(doc, "*", needle, quoted, false)
	}

	base, needle, exact, visible := extractPseudos(sel)
	if needle == "" && !visible {
		return fmt.Sprintf("%s.querySelector(%s)", doc, jsString(base))
	}
	if strings.TrimSpace(base) == "" {
		base = "*"
	}
	return scanJS(doc, base, needle, exact, visible)
}

// extractPseudos pulls the text and :visible pseudos out of a selector,
// returning the remaining base CSS plus what was found. The text needle is the
// (unquoted) argument; exact is true only for :text-is.
func extractPseudos(sel string) (base, needle string, exact, visible bool) {
	base = sel
	if m := textPseudoRe.FindStringSubmatch(base); m != nil {
		needle, _ = unquote(m[2])
		exact = m[1] == "text-is"
		base = textPseudoRe.ReplaceAllString(base, "")
	}
	if visiblePseudoRe.MatchString(base) {
		visible = true
		base = visiblePseudoRe.ReplaceAllString(base, "")
	}
	return strings.TrimSpace(base), needle, exact, visible
}

// unquote strips one layer of matching quotes (unescaping the delimiter) and
// reports whether the value was quoted.
func unquote(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		q := s[0]
		if (q == '"' || q == '\'') && s[len(s)-1] == q {
			return strings.ReplaceAll(s[1:len(s)-1], `\`+string(q), string(q)), true
		}
	}
	return s, false
}

// scanJS builds a JS query that walks base-CSS matches and returns the first
// (or, when filtering by text, the tightest — shortest textContent) element
// passing the visibility and text filters.
func scanJS(doc, base, needle string, exact, visible bool) string {
	hasNeedle := needle != ""
	return fmt.Sprintf(`(()=>{var els=%s.querySelectorAll(%s);var best=null,bl=Infinity;for(var i=0;i<els.length;i++){var el=els[i];if(%t&&!(el.offsetParent!==null||el.getClientRects().length))continue;var t=el.textContent||"";if(%t){if(!(%t?t.trim()===%s:t.includes(%s)))continue;}if(!%t)return el;if(t.length<bl){best=el;bl=t.length;}}return best;})()`,
		doc, jsString(base),
		visible,
		hasNeedle, exact, jsString(needle), jsString(needle),
		hasNeedle)
}
