package browser

import "testing"

// TestSlugParamKeepsCJK: a CJK field hint must produce a meaningful param name
// — slugging 输入标题 to "" fell back to "value", which the model misguessed
// as "title" on every replay of the recording.
func TestSlugParamKeepsCJK(t *testing.T) {
	if got := slugParam("输入标题"); got != "输入标题" {
		t.Fatalf("CJK hint mangled: %q", got)
	}
	if got := slugParam("Order ID"); got != "order_id" {
		t.Fatalf("ascii slug regressed: %q", got)
	}
	if got := slugParam("请输入标题（最多 100 个字）"); got == "" || got == "value" {
		t.Fatalf("long CJK hint must keep substance: %q", got)
	}
}
