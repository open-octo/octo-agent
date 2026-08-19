package genui

import "strings"

// PlaceholderText replaces a fully-closed ```octo-ui fenced block when
// assistant text is forwarded to a transport with no live GenUI renderer
// (an IM channel adapter, the TUI's glamour renderer). Exported as a named
// constant, rather than inlined at each call site, so a future caller can
// find and — if it ever needs to — localize or restyle it without having to
// grep for a magic string.
const PlaceholderText = "[interactive panel — open in the Web UI to view]"

// fenceOpenMarker and fenceCloseMarker are the exact (post-TrimSpace) line
// contents that open and close an inline octo-ui fence, per
// dev-docs/genui-design.md's "IM/TUI degrade" subsection.
const (
	fenceOpenMarker  = "```octo-ui"
	fenceCloseMarker = "```"
)

// StripOctoUIFences replaces every well-formed ```octo-ui ... ``` fenced
// block in text with a single PlaceholderText line. It does not parse or
// validate the JSON inside the fence — it only needs to find the fence
// boundaries.
//
// Detection is a straightforward line-based scan, not a JSON-aware one: a
// line is an opening marker when its content, trimmed of surrounding
// whitespace, is exactly "```octo-ui"; a line closes the block when trimmed
// content is exactly "```". This is deliberately simple rather than a full
// JSON-aware scanner — see the design doc's guard (this package's Sanitize),
// which already caps string field lengths, and note there is no realistic
// path for a model to naturally emit a literal "```octo-ui" or "```" inside
// a JSON string value it's using for a UI spec. A spec body that did contain
// one of those literal strings as string content would be misdetected as a
// fence boundary — an accepted limitation, not solved here.
//
// An unclosed trailing fence — the model is still streaming it, or a channel
// adapter has only buffered part of a reply — is left completely untouched:
// there is no safe boundary yet to replace up to. This is intentional rather
// than a gap: guessing at a boundary risks corrupting a partial JSON body
// that the caller may still want to inspect or re-buffer, and a later call
// against a buffer that also contains the closing fence will replace the
// whole block correctly once it arrives. Callers that flush text to a
// transport before a fence closes will show raw JSON transiently in that
// window — see internal/channel/ui_controller.go's onTextDelta and
// cmd/octo/tuirepl.go's appendText/flushTextString for how that's accepted
// as a bounded, documented limitation rather than something this function
// tries to prevent.
func StripOctoUIFences(text string) string {
	// Fast path: the overwhelmingly common case is text with no fence at
	// all, and it must be byte-identical to the input — return the original
	// string value directly rather than reconstructing it line by line.
	if !strings.Contains(text, fenceOpenMarker) {
		return text
	}

	var out strings.Builder
	remaining := text
	changed := false

	for remaining != "" {
		untouched := remaining
		line, rest, hasNL := nextLine(remaining)
		if strings.TrimSpace(line) != fenceOpenMarker {
			out.WriteString(line)
			if hasNL {
				out.WriteByte('\n')
			}
			remaining = rest
			continue
		}

		if after, hadNL, found := cutAtFenceClose(rest); found {
			out.WriteString(PlaceholderText)
			if hadNL {
				out.WriteByte('\n')
			}
			remaining = after
			changed = true
			continue
		}

		// Unclosed fence: no safe boundary yet. Leave everything from this
		// opening line through the end of the buffered text exactly as-is.
		out.WriteString(untouched)
		remaining = ""
	}

	if !changed {
		return text
	}
	return out.String()
}

// nextLine splits s at the first '\n', returning the line's content without
// the terminator, the remainder of s after the terminator, and whether a
// terminator was actually present (false only for a final, unterminated
// line at the end of s).
func nextLine(s string) (line, remainder string, hasNL bool) {
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		return s[:nl], s[nl+1:], true
	}
	return s, "", false
}

// cutAtFenceClose scans s line by line for the closing fence marker. On a
// match it returns everything after that line (the remainder to keep
// scanning from) and whether the closing line itself was newline-terminated
// — needed so StripOctoUIFences doesn't invent a trailing newline that
// wasn't in the original text. found is false when no closing line exists
// anywhere in s (an unclosed fence).
func cutAtFenceClose(s string) (after string, hadNL bool, found bool) {
	remaining := s
	for remaining != "" {
		line, rest, hasNL := nextLine(remaining)
		if strings.TrimSpace(line) == fenceCloseMarker {
			return rest, hasNL, true
		}
		remaining = rest
	}
	return "", false, false
}
