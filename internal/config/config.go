// Package config loads, validates, and writes the TOML configuration.
// Precedence: CLI flags > config file > defaults. Secrets (Pushover token and
// user key) are read exclusively from the file — never flags or env (§2.11,
// §12).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/BurntSushi/toml"
	"github.com/adrg/xdg"
)

// Config mirrors the TOML document in §12.
type Config struct {
	Source        SourceConfig        `toml:"source"`
	Audio         AudioConfig         `toml:"audio"`
	Transcription TranscriptionConfig `toml:"transcription"`
	Storage       StorageConfig       `toml:"storage"`
	Pushover      PushoverConfig      `toml:"pushover"`
	Alerts        AlertsConfig        `toml:"alerts"`
}

type SourceConfig struct {
	Type           string `toml:"type"` // "soundcard" | "rtlsdr"
	Device         string `toml:"device"`
	Frequency      string `toml:"frequency"` // rtlsdr only
	Gain           string `toml:"gain"`      // "auto" | dB string
	SquelchLevel   int    `toml:"squelch_level"`
	PPM            int    `toml:"ppm"`
	SDRDeviceIndex int    `toml:"sdr_device_index"`
	WideFM         bool   `toml:"wide_fm"` // rtlsdr: wideband (broadcast) FM
}

type AudioConfig struct {
	ThresholdDBFS float64 `toml:"threshold_dbfs"`
	OpenBlocks    int     `toml:"open_blocks"`
	HangTimeS     float64 `toml:"hang_time_s"`
	PrerollMS     int     `toml:"preroll_ms"`
	MaxSegmentS   float64 `toml:"max_segment_s"`
	MinSegmentS   float64 `toml:"min_segment_s"`
}

type TranscriptionConfig struct {
	Model         string   `toml:"model"`
	BeamSize      int      `toml:"beam_size"`
	Language      string   `toml:"language"`
	InitialPrompt string   `toml:"initial_prompt"`
	Blocklist     []string `toml:"blocklist"`
	Workers       int      `toml:"workers"`
}

type StorageConfig struct {
	DBPath        string `toml:"db_path"`
	RetainAudio   bool   `toml:"retain_audio"`
	AudioDir      string `toml:"audio_dir"`
	RetentionDays int    `toml:"retention_days"`
}

// PushoverConfig holds secrets. These live only here (§2.11).
type PushoverConfig struct {
	Token   string `toml:"token"`
	UserKey string `toml:"user_key"`
}

type AlertsConfig struct {
	Enabled    bool         `toml:"enabled"`
	MaxPerHour int          `toml:"max_per_hour"`
	Rules      []RuleConfig `toml:"rules"`
}

type RuleConfig struct {
	Name          string `toml:"name"`
	Pattern       string `toml:"pattern"`
	CaseSensitive bool   `toml:"case_sensitive"`
	Priority      int    `toml:"priority"`
	Sound         string `toml:"sound"`
	CooldownS     int    `toml:"cooldown_s"`
	Retry         int    `toml:"retry"`  // required iff priority == 2
	Expire        int    `toml:"expire"` // required iff priority == 2
}

// Default returns the baseline configuration (§5, §6, §12 defaults).
func Default() Config {
	return Config{
		Source: SourceConfig{
			Type:         "soundcard",
			Device:       "default",
			Frequency:    "146.520M",
			Gain:         "auto",
			SquelchLevel: 50,
			PPM:          0,
		},
		Audio: AudioConfig{
			ThresholdDBFS: -45.0,
			OpenBlocks:    3,
			HangTimeS:     1.75,
			PrerollMS:     400,
			MaxSegmentS:   120,
			MinSegmentS:   0.4,
		},
		Transcription: TranscriptionConfig{
			Model:    "small.en-q8_0",
			BeamSize: 5,
			Language: "en",
			Workers:  1,
		},
		Storage: StorageConfig{
			RetainAudio:   true,
			RetentionDays: 14,
		},
		Alerts: AlertsConfig{
			Enabled:    false,
			MaxPerHour: 60,
		},
	}
}

// DefaultPath returns the platform config path (§12).
func DefaultPath() (string, error) {
	return xdg.ConfigFile("voxthief/config.toml")
}

// Load reads the TOML file at path (or the default path when empty), layered
// over defaults. A missing file is not an error — defaults are returned. The
// returned bool reports whether a file was actually read.
func Load(path string) (Config, bool, error) {
	cfg := Default()
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return cfg, false, err
		}
		path = p
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, false, nil
		}
		return cfg, false, err
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, true, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, true, nil
}

// Validate fails fast on any invalid rule or inconsistent alert config (§12).
func (c Config) Validate() error {
	if c.Source.Type != "soundcard" && c.Source.Type != "rtlsdr" {
		return fmt.Errorf("source.type must be \"soundcard\" or \"rtlsdr\", got %q", c.Source.Type)
	}
	if c.Alerts.Enabled && (c.Pushover.Token == "" || c.Pushover.UserKey == "") {
		return fmt.Errorf("alerts.enabled = true but [pushover] token/user_key are empty")
	}
	seen := map[string]bool{}
	for i, r := range c.Alerts.Rules {
		where := fmt.Sprintf("alerts.rules[%d]", i)
		if r.Name == "" {
			return fmt.Errorf("%s: name is required", where)
		}
		if seen[r.Name] {
			return fmt.Errorf("%s: duplicate rule name %q", where, r.Name)
		}
		seen[r.Name] = true
		if r.Pattern == "" {
			return fmt.Errorf("rule %q: pattern is required", r.Name)
		}
		if _, err := regexp.Compile(r.Pattern); err != nil {
			return fmt.Errorf("rule %q: uncompilable regex: %w", r.Name, err)
		}
		if r.Priority < -2 || r.Priority > 2 {
			return fmt.Errorf("rule %q: priority %d out of range -2..2", r.Name, r.Priority)
		}
		if r.Priority == 2 {
			if r.Retry < 30 {
				return fmt.Errorf("rule %q: priority 2 requires retry >= 30 (got %d)", r.Name, r.Retry)
			}
			if r.Expire <= 0 || r.Expire > 10800 {
				return fmt.Errorf("rule %q: priority 2 requires expire in 1..10800 (got %d)", r.Name, r.Expire)
			}
		}
	}
	return nil
}

// HasSecrets reports whether either Pushover secret is set.
func (c Config) HasSecrets() bool {
	return c.Pushover.Token != "" || c.Pushover.UserKey != ""
}

// ResolveDBPath returns the effective DB path, defaulting to the XDG data dir.
func (c Config) ResolveDBPath() (string, error) {
	if c.Storage.DBPath != "" {
		return c.Storage.DBPath, nil
	}
	return xdg.DataFile("voxthief/voxthief.db")
}

// ResolveAudioDir returns the effective audio directory (§5).
func (c Config) ResolveAudioDir() (string, error) {
	if c.Storage.AudioDir != "" {
		return c.Storage.AudioDir, nil
	}
	dir, err := xdg.DataFile("voxthief/audio/.keep")
	if err != nil {
		return "", err
	}
	return filepath.Dir(dir), nil
}

// ResolveModelsDir returns the models cache directory (§6).
func ResolveModelsDir() (string, error) {
	f, err := xdg.DataFile("voxthief/models/.keep")
	if err != nil {
		return "", err
	}
	return filepath.Dir(f), nil
}
