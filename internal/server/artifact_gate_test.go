package server

import (
	"bytes"
	"strings"
	"testing"
)

func gate(t *testing.T, html string) string {
	t.Helper()
	return string(gateArtifactHTML([]byte(html), false))
}

func TestGateArtifactHTML_SelfContainedPageIsUntouched(t *testing.T) {
	src := []byte(`<html><head><style>h1{color:red}</style><script>window.ok=1</script></head><body><h1>hi</h1></body></html>`)
	if out := gateArtifactHTML(src, false); !bytes.Equal(out, src) {
		t.Fatalf("self-contained page was reshaped:\n%s", out)
	}
}

func TestGateArtifactHTML_StripsAndBanners(t *testing.T) {
	out := gate(t, `<html><head><script src="https://cdn.example.com/lib.js"></script></head><body><h1>hi</h1></body></html>`)
	if strings.Contains(out, "lib.js") {
		t.Errorf("external script survived: %s", out)
	}
	if !strings.Contains(out, "1 external script/stylesheet was removed") {
		t.Errorf("banner missing: %s", out)
	}
	if !strings.Contains(out, "<h1>hi</h1>") {
		t.Errorf("content lost: %s", out)
	}
}

func TestGateArtifactHTML_DoctypeStaysFirstWithoutBodyTag(t *testing.T) {
	// A <div> ahead of the DOCTYPE would drop the page into quirks mode.
	out := gate(t, `<!DOCTYPE html><html><head><link rel="stylesheet" href="https://cdn/x.css"></head><main>hi</main></html>`)
	if !strings.HasPrefix(out, "<!DOCTYPE html>") {
		t.Errorf("doctype not first: %s", out)
	}
	if strings.Index(out, "removed") > strings.Index(out, "<main>") {
		t.Errorf("banner must precede the content: %s", out)
	}
}

func TestGateArtifactHTML_BannerLandsInsideAttributedBody(t *testing.T) {
	out := gate(t, `<html><head><link rel="stylesheet" href="https://cdn/x.css"></head><body class="dark" data-x="1"><p>hi</p></body></html>`)
	body := strings.Index(out, `<body class="dark" data-x="1">`)
	banner := strings.Index(out, "removed")
	p := strings.Index(out, "<p>hi</p>")
	if body < 0 || !(body < banner && banner < p) {
		t.Errorf("order body(%d) < banner(%d) < p(%d) violated: %s", body, banner, p, out)
	}
}

func TestGateArtifactHTML_UnquotedAndUppercase(t *testing.T) {
	out := gate(t, `<HTML><HEAD><SCRIPT SRC=https://cdn/x.js></SCRIPT><LINK REL=stylesheet HREF=https://cdn/x.css></HEAD><BODY></BODY></HTML>`)
	if strings.Contains(out, "cdn/x.js") || strings.Contains(out, "cdn/x.css") {
		t.Errorf("uppercase/unquoted references survived: %s", out)
	}
	if !strings.Contains(out, "2 external scripts/stylesheets were removed") {
		t.Errorf("count wrong: %s", out)
	}
}

func TestGateArtifactHTML_DecoyInsideAnotherAttributeIsNotAReference(t *testing.T) {
	src := `<html><head><script data-cfg="src='https://c/x'">inline()</script><link rel="stylesheet" data-href="https://c/x.css"></head><body></body></html>`
	if out := gate(t, src); out != src {
		t.Errorf("decoy attributes changed the document:\n%s", out)
	}
}

func TestGateArtifactHTML_AllowlistedCDNsStay(t *testing.T) {
	src := `<html><head>` +
		`<script src="https://cdn.jsdelivr.net/npm/chart.js@4.4.0/dist/chart.umd.js"></script>` +
		`<script src="https://cdnjs.cloudflare.com/ajax/libs/react/18.3.1/umd/react.production.min.js"></script>` +
		`<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter">` +
		`<link rel="stylesheet" href="https://cdn.bootcdn.net/ajax/libs/antd/5.0.0/reset.css">` +
		`</head><body><h1>hi</h1></body></html>`
	if out := gate(t, src); out != src {
		t.Errorf("allowlisted references were touched:\n%s", out)
	}
}

func TestGateArtifactHTML_StripsDisallowedKeepsAllowedBesideIt(t *testing.T) {
	out := gate(t, `<html><head><script src="https://cdn.jsdelivr.net/npm/vue@3/dist/vue.global.js"></script><script src="https://evil.example.com/x.js"></script></head><body></body></html>`)
	if !strings.Contains(out, "cdn.jsdelivr.net") || strings.Contains(out, "evil.example.com") {
		t.Errorf("wrong reference judged: %s", out)
	}
	if !strings.Contains(out, "1 external script/stylesheet was removed") {
		t.Errorf("count wrong: %s", out)
	}
}

func TestGateArtifactHTML_RequiresHTTPS(t *testing.T) {
	out := gate(t, `<html><head><script src="http://cdn.jsdelivr.net/npm/x.js"></script></head><body></body></html>`)
	if strings.Contains(out, "cdn.jsdelivr.net") || !strings.Contains(out, "removed") {
		t.Errorf("plain-http reference to an allowlisted host survived: %s", out)
	}
}

func TestGateArtifactHTML_JudgesRealHostname(t *testing.T) {
	out := gate(t, `<html><head>`+
		`<script src="https://cdn.jsdelivr.net.evil.com/x.js"></script>`+
		`<script src="https://cdn.jsdelivr.net@evil.com/x.js"></script>`+
		`<link rel="stylesheet" href="https://evilcdn.jsdelivr.net/x.css">`+
		`</head><body></body></html>`)
	if strings.Contains(out, "evil") {
		t.Errorf("lookalike host survived: %s", out)
	}
	if !strings.Contains(out, "3 external scripts/stylesheets were removed") {
		t.Errorf("count wrong: %s", out)
	}
}

func TestGateArtifactHTML_JudgesTheAttributeTheBrowserUses(t *testing.T) {
	out := gate(t, `<html><head>`+
		`<script foo="x src=https://cdn.jsdelivr.net/ok.js" src="https://evil.com/x.js"></script>`+
		`<link title="href=https://cdn.jsdelivr.net/a.css" rel="stylesheet" href="https://evil.com/x.css">`+
		`</head><body></body></html>`)
	if strings.Contains(out, "evil.com") {
		t.Errorf("decoy let the real reference through: %s", out)
	}
	if !strings.Contains(out, "2 external scripts/stylesheets were removed") {
		t.Errorf("count wrong: %s", out)
	}
}

func TestGateArtifactHTML_QuotedGTAndSolidus(t *testing.T) {
	out := gate(t, `<html><head>`+
		`<script data-x="a>b" src="https://evil.com/x.js"></script>`+
		`<script/src="https://evil.com/y.js"></script>`+
		`</head><body></body></html>`)
	if strings.Contains(out, "evil.com") {
		t.Errorf("tokenizer tricks let a reference through: %s", out)
	}
}

func TestGateArtifactHTML_FirstOfDuplicateAttributesWins(t *testing.T) {
	out := gate(t, `<html><head><script src="https://evil.com/x.js" src="https://cdn.jsdelivr.net/ok.js"></script></head><body></body></html>`)
	if strings.Contains(out, "evil.com") || !strings.Contains(out, "1 external script/stylesheet was removed") {
		t.Errorf("duplicate src judged wrongly: %s", out)
	}
}

func TestGateArtifactHTML_ProtocolRelativeIsStripped(t *testing.T) {
	out := gate(t, `<html><head><script src="//cdn.jsdelivr.net/npm/x.js"></script></head><body></body></html>`)
	if strings.Contains(out, "cdn.jsdelivr.net/npm") || !strings.Contains(out, "removed") {
		t.Errorf("protocol-relative reference survived: %s", out)
	}
}

// The point of the artifact origin: the page's own files are not external.
func TestGateArtifactHTML_RelativeReferencesStay(t *testing.T) {
	src := `<!DOCTYPE html><html><head>` +
		`<link rel="stylesheet" href="./style.css">` +
		`<link rel="stylesheet" href="assets/theme.css">` +
		`<link rel="modulepreload" href="/vendor/three.module.js">` +
		`<script type="module" src="./app.js"></script>` +
		`<script src="vendor/three.min.js"></script>` +
		`</head><body><h1>hi</h1></body></html>`
	if out := gate(t, src); out != src {
		t.Errorf("relative references were stripped or the document reshaped:\n%s", out)
	}
}

func TestGateArtifactHTML_DataAndFragmentReferencesStay(t *testing.T) {
	src := `<html><head><link rel="stylesheet" href="data:text/css,body{color:red}"><script src="blob:abc"></script><script src=""></script></head><body></body></html>`
	if out := gate(t, src); out != src {
		t.Errorf("non-loading references were touched:\n%s", out)
	}
}

func TestGateArtifactHTML_OnlyRenderAffectingRelsCount(t *testing.T) {
	src := `<html><head>` +
		`<link rel="icon" href="https://example.com/favicon.ico">` +
		`<link rel="manifest" href="https://example.com/site.webmanifest">` +
		`<link rel="preconnect" href="https://example.com">` +
		`<link rel="canonical" href="https://example.com/page">` +
		`</head><body></body></html>`
	if out := gate(t, src); out != src {
		t.Errorf("non-render links were stripped:\n%s", out)
	}
	out := gate(t, `<html><head><link rel="preload" as="style" href="https://example.com/x.css"></head><body></body></html>`)
	if strings.Contains(out, "example.com/x.css") || !strings.Contains(out, "removed") {
		t.Errorf("preload was not treated as render-affecting: %s", out)
	}
}

func TestGateArtifactHTML_DarkBanner(t *testing.T) {
	out := string(gateArtifactHTML([]byte(`<html><head><script src="https://evil.com/x.js"></script></head><body></body></html>`), true))
	if !strings.Contains(out, "#2b2111") {
		t.Errorf("dark theme colours missing: %s", out)
	}
}
