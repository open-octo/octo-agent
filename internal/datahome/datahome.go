// Package datahome resolves the profile-scoped root for Octo's persistent user data.
package datahome

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ProfileEnv carries the selected profile into re-executed serve workers and
// child processes. It is set only by the global --profile command-line flag.
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
			if i+1 == len(args) {
				return nil, fmt.Errorf("--profile requires a name")
			}
			seen = true
			profile = args[i+1]
			i++
		case strings.HasPrefix(arg, "--profile="):
			if seen {
				return nil, fmt.Errorf("--profile may be specified only once")
			}
			seen = true
			profile = strings.TrimPrefix(arg, "--profile=")
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
