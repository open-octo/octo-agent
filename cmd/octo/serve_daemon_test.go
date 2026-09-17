package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDaemonDialAddr(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"default", nil, "127.0.0.1:8088"},
		{"separate flag", []string{"-addr", "127.0.0.1:3000"}, "127.0.0.1:3000"},
		{"long separate flag", []string{"--addr", "127.0.0.1:3000"}, "127.0.0.1:3000"},
		{"equals form", []string{"-addr=127.0.0.1:9000"}, "127.0.0.1:9000"},
		{"long equals form", []string{"--addr=127.0.0.1:9000"}, "127.0.0.1:9000"},
		{"wildcard host dialed on loopback", []string{"-addr", ":8080"}, "127.0.0.1:8080"},
		{"0.0.0.0 dialed on loopback", []string{"-addr", "0.0.0.0:8081"}, "127.0.0.1:8081"},
		{"ipv6 wildcard dialed on loopback", []string{"-addr", "[::]:8082"}, "127.0.0.1:8082"},
		{"other flags ignored", []string{"--no-tools", "-addr", "127.0.0.1:7000", "--cors", "*"}, "127.0.0.1:7000"},
		{"last addr wins", []string{"-addr", "127.0.0.1:1111", "-addr", "127.0.0.1:2222"}, "127.0.0.1:2222"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := daemonDialAddr(tc.args); got != tc.want {
				t.Errorf("daemonDialAddr(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

// A named profile's port is chosen for the user, so status is the one place
// they can look it up — it reports the pinned address alongside the pid.
func TestStatusDaemonReportsThePinnedAddress(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("OCTO_PROFILE", "work")
	dir := filepath.Join(home, ".octo-work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "serve.pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "serve.addr"), []byte("127.0.0.1:8089\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if code := statusDaemon(&out, &out); code != 0 {
		t.Fatalf("statusDaemon = %d, want 0", code)
	}
	want := fmt.Sprintf("running (pid %d) at http://127.0.0.1:8089", os.Getpid())
	if !strings.Contains(out.String(), want) {
		t.Errorf("status output should contain %q; got %q", want, out.String())
	}

	// Without a pin (the default profile never writes one) the output stays
	// in its pre-pin shape.
	if err := os.Remove(filepath.Join(dir, "serve.addr")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	statusDaemon(&out, &out)
	if strings.Contains(out.String(), " at ") {
		t.Errorf("status without a pin should not report an address; got %q", out.String())
	}
	if !strings.Contains(out.String(), fmt.Sprintf("running (pid %d)", os.Getpid())) {
		t.Errorf("status should still report the pid; got %q", out.String())
	}
}
