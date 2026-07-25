// Package channel provides IM platform bridging for octo-agent.
package channel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ConfigDir is the user-level config directory.
const ConfigDir = ".octo"

// ConfigFile is the channel credentials file.
const ConfigFile = "channels.yml"

// InstanceConfig is the raw per-bot-instance configuration.
type InstanceConfig struct {
	Name    string         `yaml:"name,omitempty"`
	Config  PlatformConfig `yaml:",inline,omitempty"`
	Enabled bool           `yaml:"enabled"`
}

// IsEnabled reports whether this instance is enabled.
func (ic InstanceConfig) IsEnabled() bool { return ic.Enabled }

// PlatformConfig is the raw per-platform configuration from YAML.
type PlatformConfig map[string]any

// InstanceList is a list of InstanceConfig with custom YAML unmarshalling
// that upgrades legacy single-instance (map) entries to a one-element list.
type InstanceList []InstanceConfig

// UnmarshalYAML implements yaml.Unmarshaler: a map value is upgraded to a
// one-element list so legacy single-instance channels.yml files load without
// migration.
func (il *InstanceList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.SequenceNode:
		var list []InstanceConfig
		if err := value.Decode(&list); err != nil {
			return err
		}
		*il = list
		return nil
	case yaml.MappingNode:
		// Legacy single-instance: decode as PlatformConfig and wrap.
		var pc PlatformConfig
		if err := value.Decode(&pc); err != nil {
			return err
		}
		ic := InstanceConfig{Config: pc, Enabled: isEnabled(pc)}
		// Carry the name from the platform key up into the instance when available.
		*il = []InstanceConfig{ic}
		return nil
	default:
		return fmt.Errorf("channel config: expected sequence or mapping, got %v", value.Kind)
	}
}

// Config manages IM platform credentials (Feishu, WeCom, etc.).
// Stored in ~/.octo/channels.yml.
type Config struct {
	Channels map[string]InstanceList `yaml:"channels,omitempty"`
}

// ConfigPath returns the absolute path to channels.yml.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ConfigDir, ConfigFile), nil
}

// LoadConfig reads ~/.octo/channels.yml. A missing file returns an empty
// Config rather than an error.
func LoadConfig() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{}, nil
		}
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("channel config: %w", err)
	}
	// Backfill instance names for legacy single-instance entries that have none.
	for platform, list := range cfg.Channels {
		for i := range list {
			if list[i].Name == "" {
				list[i].Name = platform
			}
		}
	}
	return &cfg, nil
}

// Save writes the config to ~/.octo/channels.yml with mode 0600.
func (c *Config) Save() error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// EnabledPlatforms returns the list of platforms that have at least one
// enabled instance.
func (c *Config) EnabledPlatforms() []string {
	var out []string
	for name, list := range c.Channels {
		for _, ic := range list {
			if ic.Enabled {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// Platform returns the instance list for a platform, or nil if not present.
func (c *Config) Platform(name string) InstanceList {
	return c.Channels[name]
}

// IsEnabled reports whether the named platform has any enabled instance.
func (c *Config) IsEnabled(name string) bool {
	for _, ic := range c.Channels[name] {
		if ic.Enabled {
			return true
		}
	}
	return false
}

// SetPlatform merges fields into a platform's first instance, creating it
// if needed. For single-instance platforms this is the legacy-compatible path.
func (c *Config) SetPlatform(name string, fields map[string]any) {
	if c.Channels == nil {
		c.Channels = make(map[string]InstanceList)
	}
	list := c.Channels[name]
	if len(list) == 0 {
		list = []InstanceConfig{{Name: name}}
	}
	ic := &list[0]
	if ic.Config == nil {
		ic.Config = make(PlatformConfig)
	}
	for k, v := range fields {
		ic.Config[k] = v
	}
	ic.Enabled = true
	c.Channels[name] = list
}

// RemovePlatform deletes a platform entry entirely.
func (c *Config) RemovePlatform(name string) {
	delete(c.Channels, name)
}

// EnabledInstances returns all enabled (platform, instance) pairs.
func (c *Config) EnabledInstances() []InstanceRef {
	var out []InstanceRef
	for name, list := range c.Channels {
		for i, ic := range list {
			if ic.Enabled {
				out = append(out, InstanceRef{
					Platform: name,
					Instance: ic,
					Index:    i,
				})
			}
		}
	}
	return out
}

// InstanceRef pairs a platform name with one of its instances.
type InstanceRef struct {
	Platform string
	Instance InstanceConfig
	Index    int
}

// AdapterID returns the unique identifier for this instance, used in
// InboundEvent.AdapterID and ChannelBinding.AdapterID. For
// single-instance platforms, AdapterID is the platform name (legacy
// mode: always matches empty binding). For multi-instance, it's the
// instance Name.
func (ir InstanceRef) AdapterID() string {
	if ir.Instance.Name != "" {
		return ir.Instance.Name
	}
	return ir.Platform
}

func isEnabled(pc PlatformConfig) bool {
	if pc == nil {
		return false
	}
	v, ok := pc["enabled"]
	if !ok {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val == "true" || val == "yes" || val == "1"
	case int:
		return val != 0
	default:
		return false
	}
}
