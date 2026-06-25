package skills

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// InstallZip installs a skill from a local zip archive into destRoot. The
// archive may carry SKILL.md at its root, or inside a single top-level folder
// (the layout produced by zipping a skill directory); that folder prefix is
// stripped. Same hardening as the GitHub installer: regular files only,
// path-traversal entries rejected, total size capped.
func InstallZip(zipPath, destRoot string, force bool) (name, desc string, err error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", "", fmt.Errorf("open %s: %w", zipPath, err)
	}
	defer zr.Close()

	prefix, err := zipSkillPrefix(&zr.Reader)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", zipPath, err)
	}

	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return "", "", err
	}
	tmp, err := os.MkdirTemp(destRoot, ".install-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)

	var total int64
	for _, f := range zr.File {
		if !f.Mode().IsRegular() {
			continue
		}
		rel := strings.TrimPrefix(f.Name, prefix)
		if rel == "" {
			continue
		}
		clean := filepath.FromSlash(path.Clean(rel))
		if !filepath.IsLocal(clean) {
			return "", "", fmt.Errorf("zip entry %q escapes the skill directory", f.Name)
		}
		total += int64(f.UncompressedSize64)
		if total > installMaxBytes {
			return "", "", fmt.Errorf("archive exceeds %d MB extraction cap", installMaxBytes>>20)
		}
		if err := writeZipEntry(f, filepath.Join(tmp, clean)); err != nil {
			return "", "", err
		}
	}

	fallback := strings.TrimSuffix(filepath.Base(zipPath), filepath.Ext(zipPath))
	return placeStaged(tmp, destRoot, fallback, force)
}

// zipSkillPrefix locates SKILL.md in the archive and returns the directory
// prefix (with trailing slash) to strip from every entry: "" when SKILL.md is
// at the root, "<dir>/" when the archive wraps the skill in one folder.
func zipSkillPrefix(zr *zip.Reader) (string, error) {
	for _, f := range zr.File {
		if f.Name == SkillFile {
			return "", nil
		}
	}
	for _, f := range zr.File {
		dir, base := path.Split(f.Name)
		if base == SkillFile && strings.Count(dir, "/") == 1 {
			return dir, nil
		}
	}
	return "", fmt.Errorf("no %s at the archive root or in a single top-level folder", SkillFile)
}

func writeZipEntry(f *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	// LimitReader guards against an entry whose actual stream exceeds its
	// declared UncompressedSize64 (the number the size cap accounted).
	if _, err := io.Copy(dst, io.LimitReader(src, int64(f.UncompressedSize64))); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}

// InstallDir installs a skill from a local directory (containing SKILL.md)
// into destRoot by copying regular files. Symlinks are skipped, matching the
// archive installers.
func InstallDir(srcDir, destRoot string, force bool) (name, desc string, err error) {
	info, err := os.Stat(srcDir)
	if err != nil {
		return "", "", err
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("%s is not a directory", srcDir)
	}

	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return "", "", err
	}
	tmp, err := os.MkdirTemp(destRoot, ".install-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)

	var total int64
	err = filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		total += fi.Size()
		if total > installMaxBytes {
			return fmt.Errorf("directory exceeds %d MB copy cap", installMaxBytes>>20)
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		return copyFile(p, filepath.Join(tmp, rel))
	})
	if err != nil {
		return "", "", err
	}

	return placeStaged(tmp, destRoot, filepath.Base(srcDir), force)
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
