package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"default ok", func(c *Config) {}, ""},
		{"bad source type", func(c *Config) { c.Source.Type = "bogus" }, "source.type"},
		{"enabled no creds", func(c *Config) { c.Alerts.Enabled = true }, "token/user_key are empty"},
		{"bad regex", func(c *Config) {
			c.Alerts.Rules = []RuleConfig{{Name: "r", Pattern: "("}}
		}, "uncompilable regex"},
		{"priority range", func(c *Config) {
			c.Alerts.Rules = []RuleConfig{{Name: "r", Pattern: "a", Priority: 3}}
		}, "out of range"},
		{"prio2 no retry", func(c *Config) {
			c.Alerts.Rules = []RuleConfig{{Name: "r", Pattern: "a", Priority: 2, Expire: 3600}}
		}, "requires retry"},
		{"prio2 no expire", func(c *Config) {
			c.Alerts.Rules = []RuleConfig{{Name: "r", Pattern: "a", Priority: 2, Retry: 60}}
		}, "requires expire"},
		{"prio2 ok", func(c *Config) {
			c.Alerts.Rules = []RuleConfig{{Name: "r", Pattern: "a", Priority: 2, Retry: 60, Expire: 3600}}
		}, ""},
		{"dup name", func(c *Config) {
			c.Alerts.Rules = []RuleConfig{{Name: "r", Pattern: "a"}, {Name: "r", Pattern: "b"}}
		}, "duplicate rule name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Default()
			tt.mutate(&c)
			err := c.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("want ok, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestPermWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("perm check skipped on windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	warn, err := PermWarning(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn, "chmod 600") {
		t.Fatalf("want chmod warning, got %q", warn)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	warn, _ = PermWarning(path, true)
	if warn != "" {
		t.Fatalf("want no warning at 0600, got %q", warn)
	}
	// No secrets => no check regardless of perms.
	_ = os.Chmod(path, 0o644)
	warn, _ = PermWarning(path, false)
	if warn != "" {
		t.Fatalf("want no warning without secrets, got %q", warn)
	}
}

func TestWriteDefaultRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if _, err := WriteDefault(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("want mode 0600, got %#o", info.Mode().Perm())
	}
	cfg, read, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !read {
		t.Fatal("expected file to be read")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default written config invalid: %v", err)
	}
	if cfg.Audio.ThresholdDBFS != -45.0 {
		t.Fatalf("threshold not loaded: %v", cfg.Audio.ThresholdDBFS)
	}
	if _, err := WriteDefault(path); err == nil {
		t.Fatal("expected refusal to overwrite")
	}
}
