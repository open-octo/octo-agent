package skills

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Source identifies a skill directory inside a GitHub repository.
type Source struct {
	Owner   string
	Repo    string
	Ref     string // empty = the repository's default branch
	Subpath string // slash-separated path inside the repo; empty = repo root
}

// String renders the source in the canonical shorthand form for messages.
func (s Source) String() string {
	out := s.Owner + "/" + s.Repo
	if s.Ref != "" {
		out += "@" + s.Ref
	}
	if s.Subpath != "" {
		out += "/" + s.Subpath
	}
	return out
}

// ParseSource accepts either a GitHub tree URL
// (https://github.com/owner/repo/tree/ref/sub/path) or the shorthand
// owner/repo[/sub/path]. The shorthand carries no ref and resolves to the
// repository's default branch.
func ParseSource(s string) (Source, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Source{}, fmt.Errorf("empty source")
	}
	if strings.Contains(s, "://") {
		return parseSourceURL(s)
	}
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) < 2 {
		return Source{}, fmt.Errorf("source %q: want owner/repo[/sub/path] or a github.com/.../tree/... URL", s)
	}
	return Source{Owner: parts[0], Repo: parts[1], Subpath: strings.Join(parts[2:], "/")}, nil
}

func parseSourceURL(s string) (Source, error) {
	u, err := url.Parse(s)
	if err != nil {
		return Source{}, fmt.Errorf("source %q: %w", s, err)
	}
	if u.Host != "github.com" && u.Host != "www.github.com" {
		return Source{}, fmt.Errorf("source %q: only github.com URLs are supported", s)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return Source{}, fmt.Errorf("source %q: want github.com/owner/repo[/tree/ref/sub/path]", s)
	}
	src := Source{Owner: parts[0], Repo: parts[1]}
	rest := parts[2:]
	if len(rest) == 0 {
		return src, nil
	}
	// Only the /tree/<ref>/<subpath> form is meaningful for a directory; a ref
	// containing "/" can't be told apart from the subpath, so the first segment
	// after tree/ is taken as the whole ref.
	if rest[0] != "tree" || len(rest) < 2 {
		return Source{}, fmt.Errorf("source %q: want github.com/owner/repo/tree/ref/sub/path", s)
	}
	src.Ref = rest[1]
	src.Subpath = strings.Join(rest[2:], "/")
	return src, nil
}

// tarballURL builds the GitHub tarball endpoint for a source. The API serves
// public repos unauthenticated and resolves an empty ref to the default
// branch. A var so tests can point it at an httptest server.
var tarballURL = func(s Source) string {
	u := "https://api.github.com/repos/" + s.Owner + "/" + s.Repo + "/tarball"
	if s.Ref != "" {
		u += "/" + s.Ref
	}
	return u
}

// installMaxBytes caps the total bytes extracted from a tarball so a
// hostile or bloated repository can't fill the disk. Real skills are a few
// megabytes at most (the OOXML document skills are ~1.2 MB each).
const installMaxBytes = 256 << 20

var installClient = &http.Client{Timeout: 5 * time.Minute}

// Install downloads the repository tarball, extracts src.Subpath, validates
// its SKILL.md, and moves it into destRoot (normally UserRoot()). The
// installed directory is named after the SKILL.md frontmatter name, falling
// back to the subpath's basename. Refuses to replace an existing skill unless
// force is set. Returns the installed skill's name and description.
func Install(src Source, destRoot string, force bool) (name, desc string, err error) {
	resp, err := installClient.Get(tarballURL(src))
	if err != nil {
		return "", "", fmt.Errorf("download %s: %w", src, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download %s: HTTP %s (private repos and bad refs both surface here)", src, resp.Status)
	}

	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return "", "", err
	}
	// Stage into a temp dir next to the final location so the move is a
	// same-volume rename and a failed install never leaves a half-written skill.
	tmp, err := os.MkdirTemp(destRoot, ".install-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)

	if err := extractSubdir(resp.Body, src.Subpath, tmp); err != nil {
		return "", "", fmt.Errorf("extract %s: %w", src, err)
	}

	fallback := path.Base(src.Subpath)
	if fallback == "" || fallback == "." || fallback == "/" {
		fallback = src.Repo
	}
	return placeStaged(tmp, destRoot, fallback, force)
}

// ErrExists reports an install refused because a same-named skill is already
// present and force was not set. Callers branch on it: the CLI appends a
// --force hint, the web API maps it to HTTP 409.
var ErrExists = fmt.Errorf("skill already exists")

// placeStaged validates a fully-staged skill directory (tmp must contain
// SKILL.md) and renames it to destRoot/<name>. The name comes from the
// SKILL.md frontmatter, falling back to fallbackName. Shared by the GitHub,
// zip, and directory installers.
func placeStaged(tmp, destRoot, fallbackName string, force bool) (name, desc string, err error) {
	b, err := os.ReadFile(filepath.Join(tmp, SkillFile))
	if err != nil {
		return "", "", fmt.Errorf("no readable %s — not a skill directory", SkillFile)
	}
	fmName, fmDesc, ok := parseNamed(b)
	if !ok || fmDesc == "" {
		return "", "", fmt.Errorf("%s is missing frontmatter name/description", SkillFile)
	}
	name = fmName
	if name == "" {
		name = fallbackName
	}
	if name == "" || name == "." || name == "/" {
		return "", "", fmt.Errorf("cannot determine a skill name")
	}

	dest := filepath.Join(destRoot, name)
	if _, err := os.Stat(dest); err == nil {
		if !force {
			return "", "", fmt.Errorf("%w: %q at %s", ErrExists, name, dest)
		}
		if err := os.RemoveAll(dest); err != nil {
			return "", "", err
		}
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", "", err
	}
	return name, fmDesc, nil
}

// parseNamed is parse plus the frontmatter name, which Install uses to pick
// the destination directory (the registry keys skills by directory name).
func parseNamed(b []byte) (name, desc string, ok bool) {
	front, _, ok := splitFrontmatter(string(b))
	if !ok {
		return "", "", false
	}
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(front), &fm); err != nil {
		return "", "", false
	}
	return strings.TrimSpace(fm.Name), strings.TrimSpace(fm.Description), true
}

// extractSubdir streams a GitHub tarball, keeping only regular files under
// subpath (repo-relative, after stripping the owner-repo-sha/ prefix GitHub
// adds) and writing them under dest. Symlinks and other special entries are
// skipped — a skill is plain files, and honoring links from an untrusted
// archive is a path-escape risk.
func extractSubdir(r io.Reader, subpath, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()

	var total int64
	found := false
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		rel := stripTopDir(hdr.Name)
		if rel == "" {
			continue
		}
		if subpath != "" {
			if !strings.HasPrefix(rel, subpath+"/") {
				continue
			}
			rel = strings.TrimPrefix(rel, subpath+"/")
		}
		// Reject entries that would land outside dest. filepath.IsLocal
		// rejects absolute paths, ".." chains, and (on Windows) reserved
		// names — anything that would escape the destination directory.
		clean := filepath.FromSlash(path.Clean(rel))
		if !filepath.IsLocal(clean) {
			return fmt.Errorf("tar entry %q escapes the skill directory", hdr.Name)
		}
		total += hdr.Size
		if total > installMaxBytes {
			return fmt.Errorf("tarball exceeds %d MB extraction cap", installMaxBytes>>20)
		}
		target := filepath.Join(dest, clean)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, io.LimitReader(tr, hdr.Size)); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		found = true
	}
	if !found {
		return fmt.Errorf("no files found under %q in the repository tarball", subpath)
	}
	return nil
}

// stripTopDir drops the single top-level directory GitHub prepends to every
// tarball entry (owner-repo-sha/...), returning the repo-relative path.
func stripTopDir(name string) string {
	if i := strings.IndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return ""
}
