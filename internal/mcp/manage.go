package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// This file is the management view of the MCP config — what a settings UI
// needs, as opposed to what a connecting session needs. LoadConfig (config.go)
// answers "which servers should I connect": it drops disabled entries and
// fails hard on invalid ones. LoadManaged answers "which servers exist": it
// keeps disabled entries and annotates invalid ones instead of failing.
//
// All mutations target the user-global file (~/.octo/mcp.json).

// ManagedServer is one config entry in the management view.
type ManagedServer struct {
	Name  string
	Entry ServerEntry
	// Invalid carries the validation error for a malformed entry ("" when
	// valid). Malformed entries are kept visible so the UI can show — and
	// the user can fix — what LoadConfig would reject.
	Invalid string
}

// LoadManaged returns every configured server, including disabled and invalid
// entries, sorted by name.
func LoadManaged() ([]ManagedServer, error) {
	byName := map[string]ManagedServer{}

	if home, err := os.UserHomeDir(); err == nil {
		userPath := filepath.Join(home, ".octo", "mcp.json")
		cfg, err := readConfigFile(userPath)
		if err != nil {
			return nil, err
		}
		if cfg != nil {
			for name, e := range cfg.Servers {
				byName[name] = managedEntry(name, e)
			}
		}
	}

	out := make([]ManagedServer, 0, len(byName))
	for _, m := range byName {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func managedEntry(name string, e ServerEntry) ManagedServer {
	m := ManagedServer{Name: name, Entry: e}
	if err := e.Validate(name); err != nil {
		m.Invalid = err.Error()
	}
	return m
}

// UserConfigPath returns the absolute path of the user-global MCP config
// (~/.octo/mcp.json).
func UserConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".octo", "mcp.json"), nil
}

// ValidateServerName rejects names that would break the mcp__<server>__<tool>
// tool-name encoding or read ambiguously in config files.
func ValidateServerName(name string) error {
	if name == "" {
		return fmt.Errorf("mcp server name must not be empty")
	}
	if strings.ContainsAny(name, " \t\n") {
		return fmt.Errorf("mcp server name %q must not contain whitespace", name)
	}
	if strings.Contains(name, "__") {
		return fmt.Errorf("mcp server name %q must not contain \"__\" (reserved as the tool-name separator)", name)
	}
	return nil
}

// UpsertUserServer validates the entry and writes it into the user-global
// config, creating the file (and ~/.octo) on first use. Existing entries
// under other names are preserved verbatim.
func UpsertUserServer(name string, e ServerEntry) error {
	if err := ValidateServerName(name); err != nil {
		return err
	}
	if err := e.Validate(name); err != nil {
		return err
	}
	return mutateUserConfig(func(cfg *Config) error {
		cfg.Servers[name] = e
		return nil
	})
}

// DeleteUserServer removes the named entry from the user-global config.
// Deleting a name that isn't there is an error rather than a silent no-op.
func DeleteUserServer(name string) error {
	return mutateUserConfig(func(cfg *Config) error {
		if _, ok := cfg.Servers[name]; !ok {
			return fmt.Errorf("mcp server %q not found in user config", name)
		}
		delete(cfg.Servers, name)
		return nil
	})
}

// SetUserServerDisabled flips the disabled flag on a user-global entry.
func SetUserServerDisabled(name string, disabled bool) error {
	return mutateUserConfig(func(cfg *Config) error {
		e, ok := cfg.Servers[name]
		if !ok {
			return fmt.Errorf("mcp server %q not found in user config", name)
		}
		e.Disabled = disabled
		cfg.Servers[name] = e
		return nil
	})
}

// mutateUserConfig reads the raw user-global config, applies fn, and writes
// the result back. The file is written 0600 (entries may carry auth headers)
// and indented so it stays hand-editable.
func mutateUserConfig(fn func(*Config) error) error {
	path, err := UserConfigPath()
	if err != nil {
		return err
	}
	cfg, err := readConfigFile(path)
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.Servers == nil {
		cfg.Servers = map[string]ServerEntry{}
	}
	if err := fn(cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
