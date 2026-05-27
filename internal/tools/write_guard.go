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
}

// scanForSecrets returns the name of the first secret shape detected in
// content, or "" if none match. write_file uses this to refuse writing a
// file that looks like it embeds live credentials.
func scanForSecrets(content string) string {
	for _, p := range secretPatterns {
		if p.re.MatchString(content) {
			return p.name
		}
	}
	return ""
}
