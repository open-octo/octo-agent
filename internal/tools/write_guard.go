package tools

import "regexp"

// secretPatterns are high-confidence credential shapes that almost never
// belong in a file the agent writes programmatically. The set is
// deliberately narrow — we'd rather miss a low-confidence match than block
// legitimate writes (docs, test fixtures) with false positives. Bare AWS
// access-key IDs (AKIA…) are intentionally NOT included: the ID alone isn't
// secret, and the canonical docs example (AKIAIOSFODNN7EXAMPLE) would
// otherwise trip on every tutorial.
var secretPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"private key block", regexp.MustCompile(`-----BEGIN (?:RSA |OPENSSH |EC |DSA |PGP )?PRIVATE KEY-----`)},
	{"AWS secret access key", regexp.MustCompile(`(?i)aws_secret_access_key\s*[=:]\s*['"]?[A-Za-z0-9/+]{40}`)},
	{"GitHub token", regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[0-9A-Za-z]{36}\b`)},
	{"GitHub fine-grained token", regexp.MustCompile(`\bgithub_pat_[0-9A-Za-z_]{60,}\b`)},
	{"Slack token", regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z-]{10,}`)},
	{"OpenAI API key", regexp.MustCompile(`\bsk-[a-zA-Z0-9]{20,}\b`)},
	{"Anthropic API key", regexp.MustCompile(`\bsk-ant-[a-zA-Z0-9_-]{20,}\b`)},
	{"Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}\b`)},
	{"JWT", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]*\.eyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]*\b`)},
	{"generic API key", regexp.MustCompile(`(?i)\b(?:api[_-]?key|apikey|secret[_-]?key)\s*[=:]\s*['"]?[a-zA-Z0-9_\-]{16,}\b`)},
}

// scanForSecrets returns the name of the first secret shape detected in
// content, or "" if none match. It is the "written from scratch" case of
// newSecretsIntroduced — there is no prior content to compare against.
func scanForSecrets(content string) string {
	return newSecretsIntroduced("", content)
}

// newSecretsIntroduced returns the name of the first secret shape that appears
// in newContent but was not already present in oldContent, or "" if every
// secret in newContent already existed verbatim in oldContent. The guard's job
// is to stop the agent from introducing a live credential, not to stop it from
// preserving one it didn't author: a read-modify-write of a credential-bearing
// file (e.g. rewriting ~/.octo/config.yml to add a preference while keeping the
// existing api_key) carries the same secret through both sides and is allowed,
// while a brand-new or changed credential is still refused.
func newSecretsIntroduced(oldContent, newContent string) string {
	for _, p := range secretPatterns {
		newHits := p.re.FindAllString(newContent, -1)
		if len(newHits) == 0 {
			continue
		}
		oldHits := make(map[string]bool, len(newHits))
		for _, h := range p.re.FindAllString(oldContent, -1) {
			oldHits[h] = true
		}
		for _, h := range newHits {
			if !oldHits[h] {
				return p.name
			}
		}
	}
	return ""
}
