package agent

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// overflowReclaimMargin is the extra headroom (tokens) the cheap reclamation
// tier must free beyond the parsed deficit before we retry the send without a
// summarize — the deficit is approximate, so leave slack.
const overflowReclaimMargin = 4_000

var (
	// Anthropic: "prompt is too long: 218849 tokens > 200000 maximum".
	reTokensOverMax = regexp.MustCompile(`(\d+)\s+tokens\s*>\s*(\d+)`)
	// OpenAI: "maximum context length is 128000 tokens ... you requested 130000".
	reMaxContextLen = regexp.MustCompile(`maximum context length is (\d+)`)
	reRequested     = regexp.MustCompile(`requested (?:about |~)?(\d+)`)
	// Moonshot/Kimi: "Your request exceeded model token limit: 262144 (requested: 269030)".
	reTokenLimitRequested = regexp.MustCompile(`token limit:\s*(\d+)\s*\(requested:\s*(\d+)\)`)
)

// parseOverflowTokens extracts (have, max) token counts from a context-overflow
// error when the provider reports them, so recovery can free at least the
// deficit. Best-effort: returns ok=false when the numbers aren't present.
func parseOverflowTokens(err error) (have, max int, ok bool) {
	if err == nil {
		return 0, 0, false
	}
	msg := strings.ToLower(err.Error())
	if m := reTokensOverMax.FindStringSubmatch(msg); m != nil {
		h, _ := strconv.Atoi(m[1])
		mx, _ := strconv.Atoi(m[2])
		if h > mx && mx > 0 {
			return h, mx, true
		}
	}
	if mm := reMaxContextLen.FindStringSubmatch(msg); mm != nil {
		mx, _ := strconv.Atoi(mm[1])
		if rm := reRequested.FindStringSubmatch(msg); rm != nil {
			h, _ := strconv.Atoi(rm[1])
			if h > mx && mx > 0 {
				return h, mx, true
			}
		}
	}
	if m := reTokenLimitRequested.FindStringSubmatch(msg); m != nil {
		mx, _ := strconv.Atoi(m[1])
		h, _ := strconv.Atoi(m[2])
		if h > mx && mx > 0 {
			return h, mx, true
		}
	}
	return 0, 0, false
}

// overflowRecovery handles "context too long" errors by compressing history
// and retrying. Aligned with Ruby's perform_context_overflow_compression.
type overflowRecovery struct {
	inProgress bool // true during recovery compression (prevents recursion)
	attempted  bool // true after one attempt per turn (max one retry)
}

// contextTooLongError detects whether an error is about exceeding the model's
// context window. Aligned with Ruby's context_too_long_error?.
//
// Coverage (verified against real production error strings, except where a
// phrase's own comment marks it as a defensive generic):
//
//	OpenAI:    "This model's maximum context length is 128000 tokens..."
//	Anthropic: "prompt is too long: 218849 tokens > 200000 maximum"
//	Qwen:      "You passed 117345 input tokens... context length is only 125536"
//	DeepSeek:  Variants of "context length" / "tokens exceeds"
//	Kimi:      "Invalid request: Your request exceeded model token limit: 262144 (requested: 269030)"
//	Zhipu:     "Prompt 超长" (bigmodel.cn error code 1261)
//	DashScope: "InternalError.Algo.InvalidParameter: Range of input length should be [1, 202752]"
//	Generic:   "The total number of tokens exceeds the model's maximum context length"
func contextTooLongError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())

	// Strong phrases — any one is conclusive on its own.
	strongPhrases := []string{
		"context length",
		"context_length_exceeded",
		"maximum context",
		"maximum input length",
		"prompt is too long",
		"input is too long",
		"exceeds the maximum context",
		"exceeds the model's context",
		"exceeds the model's maximum",
		"reduce the length of the input",
		"reduce the length of the messages",
		"reduce the length of your",
		"reduce the length of the prompt",
		"range of input length",
		// Moonshot/Kimi (OpenAI protocol): "Your request exceeded model
		// token limit: 262144 (requested: 269030)". Deliberately the full
		// phrase — a bare "token limit" would also match rate-limit (TPM)
		// errors, and "max_tokens exceeds the maximum" parameter rejections
		// must not be classified as context overflow.
		"exceeded model token limit",
		// Zhipu bigmodel.cn error code 1261. The message is Chinese-only
		// ("Prompt 超长" per the official error-code table); matched against
		// the lowercased error string, with a no-space variant in case the
		// endpoint omits it.
		"prompt 超长",
		"prompt超长",
		// Defensive generics for CN-hosted endpoints (not verified against a
		// specific production string). Deliberately INPUT-scoped: a bare
		// "长度超限" would also match "输出长度超限" — the Chinese wording of
		// an output-side max_tokens rejection, which must not be classified
		// as context overflow (same trap as the English guard above).
		"输入长度超限",
		"上下文长度超过",
	}
	for _, p := range strongPhrases {
		if strings.Contains(msg, p) {
			return true
		}
	}

	// Pattern 1: Anthropic-style "<N> tokens > <N> maximum"
	if strings.Contains(msg, "tokens") && strings.Contains(msg, "maximum") {
		return true
	}

	// Pattern 2: Qwen-style structured field "parameter=input_tokens"
	if strings.Contains(msg, "parameter=input_tokens") {
		return true
	}

	return false
}

// tryRecover attempts to recover from a context-overflow error by compressing
// history and returning true if the caller should retry.
//
// Layer 1 (standard): pull back 1 message from tail (preserves prompt cache),
// compress, retry. Handles 99% of cases.
//
// Layer 2 (aggressive): pull back ~half the history. Sacrifices cache but
// guarantees the compression call fits.
func (r *overflowRecovery) tryRecover(ctx context.Context, a *Agent, sendErr error, handler EventHandler) bool {
	if r.attempted || r.inProgress {
		return false
	}
	if !contextTooLongError(sendErr) {
		return false
	}

	r.attempted = true
	r.inProgress = true
	defer func() { r.inProgress = false }()

	// Layer 0: cheap reclamation of stale tool results (no LLM call). When the
	// provider reported the deficit and reclamation freed at least that much
	// (plus margin), retry the send directly — no summarize needed.
	if reclaimed := a.reclaimStaleToolResults(); reclaimed > 0 {
		a.resetContextTrigger()
		if have, max, ok := parseOverflowTokens(sendErr); ok && reclaimed >= have-max+overflowReclaimMargin {
			return true
		}
		// Not provably enough — fall through to summarize, now over the
		// already-reduced history.
	}

	// Layer 1: standard cache-preserving compression
	if a.tryOverflowCompact(ctx, pullBackStandard, handler) {
		return true
	}

	// Layer 2: aggressive fallback
	if a.tryOverflowCompact(ctx, pullBackAggressive, handler) {
		return true
	}

	return false
}

// reset clears the attempted flag so the next turn can attempt recovery again.
func (r *overflowRecovery) reset() {
	r.attempted = false
}

const (
	pullBackStandard   = 1  // preserve cache#A
	pullBackAggressive = -1 // ~half history, computed dynamically
)

// tryOverflowCompact is the Layer 1/2 entry point for 400-recovery compression.
// It pops K messages from tail, runs compression, then reattaches them.
// Returns true if compression succeeded and history was rebuilt.
func (a *Agent) tryOverflowCompact(ctx context.Context, pullBackMode int, handler EventHandler) bool {
	trigger := a.compactTriggerTokens()
	if trigger <= 0 {
		return false // compaction disabled
	}

	msgs := a.History.Snapshot()
	if len(msgs) < 4 {
		return false // not enough to safely compact
	}

	// Compute pull-back count
	k := computePullBack(len(msgs), pullBackMode)
	if k <= 0 || k >= len(msgs)-1 {
		return false // would pop too much (keep at least system + 1)
	}

	// Save pulled-back messages
	pulledBack := make([]Message, k)
	copy(pulledBack, msgs[len(msgs)-k:])

	// Build truncated history for compression
	truncated := NewHistory()
	for _, m := range msgs[:len(msgs)-k] {
		truncated.Append(m)
	}

	// Find safe split point on a stable snapshot.
	truncatedSnap := truncated.Snapshot()
	split := safeSplitIndexByBudget(truncatedSnap, a.compactKeepBudget())
	if split <= 0 {
		return false
	}

	before := a.historyTokens(msgs)
	if handler != nil {
		handler(AgentEvent{Kind: EventCompactStarted, Compact: &CompactStats{
			BeforeTokens: before,
			FoldedMsgs:   split,
			KeptTurns:    countKeptUserTurns(truncatedSnap, split),
			MaxTokens:    summarizeMaxTokens,
		}})
	}

	// Run compression side-call on the stable snapshot of the truncated prefix.
	summary, err := a.summarize(ctx, truncatedSnap[:split], handler)
	if err != nil || summary == "" {
		emitCompactDone(handler, before, before, split) // no-op: clear the indicator
		return false
	}

	// Rebuild: summary + kept recent + pulled_back. Use the same snapshot so
	// recent and prefix are consistent.
	recent := truncatedSnap[split:]

	a.History.Reset()
	a.History.Append(NewUserMessage("[Earlier conversation summary]\n\n" + summary))
	for _, m := range recent {
		a.History.Append(m)
	}
	for _, m := range pulledBack {
		a.History.Append(m)
	}

	a.resetContextTrigger() // reset trigger so we don't immediately re-compact
	emitCompactDone(handler, before, a.historyTokens(a.History.Snapshot()), split)
	return true
}

func computePullBack(historyLen, mode int) int {
	if mode == pullBackStandard {
		return 1
	}
	// Aggressive: pop ~half, bounded
	half := historyLen / 2
	return clamp(half, 4, historyLen-2) // keep system + at least 1 message
}

// clamp bounds v to [min, max]. When the range is infeasible (max < min —
// e.g. a small history where "keep at least 1 message" caps below the
// aggressive-mode floor), max wins: it's the hard safety bound against
// popping too much history, so returning it degrades gracefully instead of
// handing back an out-of-range value the caller would reject outright.
func clamp(v, min, max int) int {
	if max < min {
		return max
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
