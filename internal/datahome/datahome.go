// Package datahome resolves the profile-scoped root for Octo's persistent user data.
package datahome

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ProfileEnv carries the selected profile into re-executed serve workers and
// child processes. The selected profile can come from the global --profile
// command-line flag or an inherited environment.
const ProfileEnv = "OCTO_PROFILE"

var profileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// Configure validates profile and records it for the current process. An empty
// profile selects the historical ~/.octo data root.
func Configure(profile string) error {
	profile = strings.TrimSpace(profile)
	if profile != "" && !profileName.MatchString(profile) {
		return fmt.Errorf("profile must contain only letters, digits, '-' or '_', and start with a letter or digit")
	}
	return os.Setenv(ProfileEnv, profile)
}

// ConfigureFromArgs extracts the global --profile value from any command-line
// position. The returned argument slice contains no profile flags, so command-
// specific flag parsers do not need to know about this global option.
func ConfigureFromArgs(args []string) ([]string, error) {
	filtered := make([]string, 0, len(args))
	profile := ""
	seen := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--profile":
			if seen {
				return nil, fmt.Errorf("--profile may be specified only once")
			}
			if i+1 == len(args) || strings.TrimSpace(args[i+1]) == "" {
				return nil, fmt.Errorf("--profile requires a name")
			}
			seen = true
			profile = args[i+1]
			i++
		case strings.HasPrefix(arg, "--profile="):
			if seen {
				return nil, fmt.Errorf("--profile may be specified only once")
			}
			profile = strings.TrimPrefix(arg, "--profile=")
			if strings.TrimSpace(profile) == "" {
				return nil, fmt.Errorf("--profile requires a name")
			}
			seen = true
		default:
			filtered = append(filtered, arg)
		}
	}
	if seen {
		if err := Configure(profile); err != nil {
			return nil, err
		}
	}
	return filtered, nil
}

// Dir returns the profile-scoped root for Octo's persistent user data. The
// default profile uses ~/.octo; a profile named "work" uses ~/.octo-work.
func Dir() (string, error) {
	profile := strings.TrimSpace(os.Getenv(ProfileEnv))
	if profile != "" && !profileName.MatchString(profile) {
		return "", fmt.Errorf("invalid %s value %q", ProfileEnv, profile)
	}
	return DirFor(profile)
}

// Current returns the selected profile name: "" for the default ~/.octo root.
func Current() string {
	return strings.TrimSpace(os.Getenv(ProfileEnv))
}

// ValidName reports whether profile is an acceptable profile name. The empty
// string is valid and names the default root.
func ValidName(profile string) bool {
	return profile == "" || profileName.MatchString(profile)
}

// DirFor returns the data root a given profile would use, whether or not it
// exists on disk, without consulting the selected profile. Management commands
// use it to look at roots other than the one they run under.
func DirFor(profile string) (string, error) {
	if !ValidName(profile) {
		return "", fmt.Errorf("invalid profile name %q", profile)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if home == "" {
		return "", fmt.Errorf("resolve home directory: empty path")
	}
	name := ".octo"
	if profile != "" {
		name += "-" + profile
	}
	return filepath.Join(home, name), nil
}

// Path joins elements below the profile-scoped data root.
func Path(elem ...string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{dir}, elem...)...), nil
}

// SharedPath joins elements below ~/.octo regardless of the selected profile —
// the home for things that belong to the machine rather than to one profile's
// user data. Helper binaries the installers stage live here, and so does the
// desktop shell's record of which profile to open, which by definition cannot
// live inside a profile.
func SharedPath(elem ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if home == "" {
		return "", fmt.Errorf("resolve home directory: empty path")
	}
	return filepath.Join(append([]string{home, ".octo"}, elem...)...), nil
}

// BinDir returns the machine-scoped directory for Octo-managed helper binaries.
func BinDir() (string, error) { return SharedPath("bin") }

// List returns the profiles that exist on disk: "" for the default ~/.octo when
// it is there, then each named ~/.octo-<name>, sorted. It reports what has been
// used, not what is allowed — any valid name works whether or not it is listed.
func List() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil, fmt.Errorf("read home directory: %w", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		switch name := e.Name(); {
		case name == ".octo":
			out = append(out, "")
		case strings.HasPrefix(name, ".octo-"):
			profile := strings.TrimPrefix(name, ".octo-")
			if profileName.MatchString(profile) {
				out = append(out, profile)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}
