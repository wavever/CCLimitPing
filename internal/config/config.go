// Package config loads and validates limitping's TOML configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// Duration is a time.Duration that (un)marshals from TOML as a string like
// "10s" so the config file stays human-friendly.
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.Duration.String()), nil
}

// ProviderConfig holds the per-provider knobs. ReasoningEffort applies to
// Codex-backed providers and is ignored by Claude.
type ProviderConfig struct {
	Enabled         bool     `toml:"enabled"`
	Prompt          string   `toml:"prompt"`
	ExtraArgs       []string `toml:"extra_args"`
	Model           string   `toml:"model"`
	ReasoningEffort string   `toml:"reasoning_effort"`
	AlignStart      string   `toml:"align_start"`
}

// Config is the full configuration.
type Config struct {
	// WeeklyThreshold: skip pinging when the weekly window's utilization
	// (0..1) is at or above this value, until the weekly window resets.
	WeeklyThreshold float64 `toml:"weekly_threshold"`
	// ResetBuffer: wait this long after a window's reset time before pinging,
	// to be sure the window has actually rolled over.
	ResetBuffer Duration `toml:"reset_buffer"`
	// Notify: emit macOS notifications on ping success/failure/skip.
	Notify bool `toml:"notify"`

	Claude ProviderConfig `toml:"claude"`
	Codex  ProviderConfig `toml:"codex"`
	Spark  ProviderConfig `toml:"spark"`
}

// Default returns the built-in defaults used when no config file exists.
func Default() Config {
	return Config{
		WeeklyThreshold: 0.99,
		ResetBuffer:     Duration{10 * time.Second},
		Notify:          true,
		Claude: ProviderConfig{
			Enabled:   true,
			Prompt:    ".",
			Model:     "haiku",
			ExtraArgs: []string{},
		},
		Codex: ProviderConfig{
			Enabled:         true,
			Prompt:          "ok",
			Model:           "gpt-5.4-mini",
			ReasoningEffort: "low",
		},
		Spark: ProviderConfig{
			Enabled:         false,
			Prompt:          "ok",
			Model:           "gpt-5.3-codex-spark",
			ReasoningEffort: "low",
		},
	}
}

// Dir returns limitping's config directory, honoring $XDG_CONFIG_HOME and
// falling back to ~/.config/limitping.
func Dir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "limitping"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "limitping"), nil
}

// Path returns the absolute path of the config file.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// Load reads the config file, applying defaults for any missing fields. If the
// file does not exist, the full default config is returned.
func Load() (Config, error) {
	cfg := Default()
	path, err := Path()
	if err != nil {
		return cfg, err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	} else if err != nil {
		return cfg, err
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	if c.WeeklyThreshold < 0 || c.WeeklyThreshold > 1 {
		return fmt.Errorf("weekly_threshold must be between 0 and 1, got %v", c.WeeklyThreshold)
	}
	if c.ResetBuffer.Duration < 0 {
		return errors.New("reset_buffer must not be negative")
	}
	return nil
}

// WriteDefault writes a commented default config to Path(). It refuses to
// overwrite an existing file unless force is true.
func WriteDefault(force bool) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "config.toml")
	if !force {
		if _, err := os.Stat(path); err == nil {
			return path, fmt.Errorf("config already exists at %s (use --force to overwrite)", path)
		}
	}
	if err := os.WriteFile(path, []byte(defaultTOML), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

const defaultTOML = `# limitping configuration

# Skip pinging when the weekly window utilization (0..1) is at/above this,
# until the weekly window resets.
weekly_threshold = 0.99

# Wait this long after a window's reset before pinging (ensures rollover).
reset_buffer = "10s"

# Emit macOS notifications on ping success/failure/skip.
notify = true

[claude]
enabled = true
prompt = "."
# Cheapest tier; triggering doesn't need a SOTA model and this avoids burning
# Sonnet/Opus budget (incl. the separate weekly Opus bucket). Alias or full id.
model = "haiku"
# Extra Claude CLI args. Headless/print-only flags such as -p, --print,
# --output-format, and --max-turns are ignored.
extra_args = []
# Optional RFC3339 anchor for the first window's phase; empty = start ASAP.
align_start = ""

[codex]
enabled = true
prompt = "ok"
# Cheapest Codex model for triggering (see ~/.codex/models_cache.json for the
# list available to your plan). Empty = use the Codex default model.
model = "gpt-5.4-mini"
# "low" keeps the ping cheap; "minimal" is rejected when web_search/image_gen
# tools are enabled in your Codex config.
reasoning_effort = "low"
extra_args = []
align_start = ""

[spark]
# Spark is a separate watch target backed by the Codex CLI and credentials.
# Disabled by default so upgrades do not add another quota-consuming ping.
enabled = false
prompt = "ok"
model = "gpt-5.3-codex-spark"
reasoning_effort = "low"
extra_args = []
align_start = ""
`
