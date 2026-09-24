package skills

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// A junction is how Windows users without symlink privileges link a shared
// skill in; it must be discovered like a symlink.
func TestDiscover_FollowsJunctionedSkillDir(t *testing.T) {
	useDefaultRoot(t, t.TempDir())
	userRoot := t.TempDir()
	useUserRoot(t, userRoot)
	shared := t.TempDir()
	writeSkill(t, shared, "shared", "---\ndescription: via junction\n---\nbody")
	link := filepath.Join(userRoot, "shared")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, filepath.Join(shared, "shared")).CombinedOutput(); err != nil {
		t.Skipf("mklink /J unavailable: %v: %s", err, out)
	}

	if _, ok := Discover().Get("shared"); !ok {
		t.Fatal("junctioned skill not discovered")
	}
}
