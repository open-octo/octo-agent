package shellpath

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMergePaths(t *testing.T) {
	sep := string(os.PathListSeparator)
	cases := []struct {
		name string
		a    string
		b    string
		want string
	}{
		{
			name: "b appends missing entries",
			a:    join(sep, "/usr/bin", "/bin"),
			b:    join(sep, "/opt/homebrew/bin", "/usr/bin", "/bin"),
			want: join(sep, "/usr/bin", "/bin", "/opt/homebrew/bin"),
		},
		{
			name: "deduplicates and trims",
			a:    join(sep, "/a", "/b", "/a"),
			b:    join(sep, "  /b  ", "/c", ""),
			want: join(sep, "/a", "/b", "/c"),
		},
		{
			name: "empty b returns a",
			a:    join(sep, "/a", "/b"),
			b:    "",
			want: join(sep, "/a", "/b"),
		},
		{
			name: "empty a returns b",
			a:    "",
			b:    join(sep, "/a", "/b"),
			want: join(sep, "/a", "/b"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mergePaths(c.a, c.b)
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestLooksLikeFullPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	cases := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "empty is not full",
			path: "",
			want: false,
		},
		{
			name: "gui default is not full",
			path: "/usr/bin:/bin:/usr/sbin:/sbin",
			want: false,
		},
		{
			name: "homebrew is full",
			path: "/opt/homebrew/bin:/usr/bin:/bin",
			want: true,
		},
		{
			name: "local bin is full",
			path: join(string(os.PathListSeparator), filepath.Join(home, ".local", "bin"), "/usr/bin"),
			want: true,
		},
		{
			// path_helper can inject /usr/local/bin into an otherwise-minimal GUI
			// PATH; that alone is not evidence the login shell ran, so it must NOT
			// count as full (this is the false positive the tightening fixes).
			name: "system dirs plus path_helper /usr/local/bin is not full",
			path: "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
			want: false,
		},
		{
			name: "any non-system dir makes it full",
			path: "/usr/local/go/bin:/usr/bin:/bin",
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := looksLikeFullPath(c.path)
			if got != c.want {
				t.Fatalf("looksLikeFullPath(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

func TestSyncToLoginShell(t *testing.T) {
	original := os.Getenv("PATH")
	defer os.Setenv("PATH", original)

	os.Setenv("PATH", "/usr/bin:/bin")
	syncToLoginShell(func() (string, error) {
		return "/opt/homebrew/bin:/Users/x/.local/bin:/usr/bin:/bin", nil
	})

	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		if os.Getenv("PATH") != "/usr/bin:/bin" {
			t.Fatalf("unsupported OS should not touch PATH")
		}
		return
	}

	got := os.Getenv("PATH")
	if !strings.Contains(got, "/opt/homebrew/bin") {
		t.Fatalf("PATH should contain /opt/homebrew/bin, got %q", got)
	}
	if !strings.Contains(got, "/Users/x/.local/bin") {
		t.Fatalf("PATH should contain /Users/x/.local/bin, got %q", got)
	}
}

func TestSyncToLoginShell_DoesNothingWhenFull(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("darwin/linux only")
	}
	original := os.Getenv("PATH")
	defer os.Setenv("PATH", original)

	os.Setenv("PATH", "/opt/homebrew/bin:/usr/bin:/bin")
	called := false
	syncToLoginShell(func() (string, error) {
		called = true
		return "/some/extra/bin:/usr/bin:/bin", nil
	})
	if called {
		t.Fatalf("resolver should not be called when PATH is already full")
	}
	if os.Getenv("PATH") != "/opt/homebrew/bin:/usr/bin:/bin" {
		t.Fatalf("PATH should not change when already full, got %q", os.Getenv("PATH"))
	}
}

func join(sep string, parts ...string) string {
	return strings.Join(parts, sep)
}
