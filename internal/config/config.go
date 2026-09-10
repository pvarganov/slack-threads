// Package config loads application configuration and the Slack token.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pavelvarganov/slack-threads/internal/translate"
)

// Defaults for everything the user does not override. They are the values
// the app is meant to run with; the environment variables exist for
// development and for putting the database somewhere else.
const (
	// AppDir is the directory created under the user config directory.
	AppDir = "slack-threads"
	// DBFile is the SQLite file name inside AppDir.
	DBFile = "threads.db"
	// DefaultResponseTimeout bounds one translation turn.
	DefaultResponseTimeout = 5 * time.Minute
	// DefaultIdleTimeout is how long an unused claude session is kept.
	DefaultIdleTimeout = 15 * time.Minute
	// DefaultSlackTimeout bounds a single Slack HTTP request.
	DefaultSlackTimeout = 30 * time.Second
)

// Environment variables read by Load.
const (
	// EnvDBPath overrides the SQLite file location.
	EnvDBPath = "SLACK_THREADS_DB"
	// EnvClaudeBinary overrides the claude executable.
	EnvClaudeBinary = "SLACK_THREADS_CLAUDE_BIN"
	// EnvModel overrides the translation model.
	EnvModel = "SLACK_THREADS_MODEL"
	// EnvResponseTimeout overrides the per-turn timeout.
	EnvResponseTimeout = "SLACK_THREADS_RESPONSE_TIMEOUT"
	// EnvIdleTimeout overrides the session idle timeout.
	EnvIdleTimeout = "SLACK_THREADS_IDLE_TIMEOUT"
	// EnvSlackTimeout overrides the Slack HTTP timeout.
	EnvSlackTimeout = "SLACK_THREADS_SLACK_TIMEOUT"
	// EnvToken supplies the Slack token without touching the keychain.
	EnvToken = "SLACK_THREADS_TOKEN"
)

// Config is everything the app needs to know before it can be wired up.
// The Slack token is deliberately not part of it: it lives in the keychain
// and is read through a TokenStore.
type Config struct {
	// DatabasePath is the SQLite file; its directory is created on Load.
	DatabasePath string
	// ClaudeBinary is the claude executable, looked up in PATH when it is
	// a bare name.
	ClaudeBinary string
	// Model is the --model value passed to claude.
	Model string
	// ResponseTimeout bounds a single translation turn.
	ResponseTimeout time.Duration
	// IdleTimeout is how long an idle claude session survives.
	IdleTimeout time.Duration
	// SlackTimeout bounds a single Slack HTTP request.
	SlackTimeout time.Duration
}

// Default returns the configuration used when nothing is overridden. It
// fails only when the user config directory cannot be determined.
func Default() (Config, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return Config{}, fmt.Errorf("config: locate user config dir: %w", err)
	}

	return Config{
		DatabasePath:    filepath.Join(dir, AppDir, DBFile),
		ClaudeBinary:    translate.DefaultBinary,
		Model:           translate.DefaultModel,
		ResponseTimeout: DefaultResponseTimeout,
		IdleTimeout:     DefaultIdleTimeout,
		SlackTimeout:    DefaultSlackTimeout,
	}, nil
}

// Load builds the configuration from the defaults and the environment. It
// does not touch the filesystem; call EnsureDataDir before opening the
// database.
func Load() (Config, error) {
	return LoadFrom(os.Getenv)
}

// LoadFrom is Load against an arbitrary environment lookup, which is what
// the tests use.
func LoadFrom(getenv func(string) string) (Config, error) {
	cfg, err := Default()
	if err != nil {
		return Config{}, err
	}

	if v := strings.TrimSpace(getenv(EnvDBPath)); v != "" {
		cfg.DatabasePath = v
	}

	if v := strings.TrimSpace(getenv(EnvClaudeBinary)); v != "" {
		cfg.ClaudeBinary = v
	}

	if v := strings.TrimSpace(getenv(EnvModel)); v != "" {
		cfg.Model = v
	}

	for _, d := range []struct {
		name string
		dst  *time.Duration
	}{
		{EnvResponseTimeout, &cfg.ResponseTimeout},
		{EnvIdleTimeout, &cfg.IdleTimeout},
		{EnvSlackTimeout, &cfg.SlackTimeout},
	} {
		if err := applyDuration(getenv(d.name), d.name, d.dst); err != nil {
			return Config{}, err
		}
	}

	return cfg, nil
}

// applyDuration parses a duration override, leaving the default in place
// when the variable is unset. A malformed or non-positive value is an
// error: silently falling back to the default would hide a typo.
func applyDuration(raw, name string, dst *time.Duration) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("config: %s: %q is not a duration", name, raw)
	}

	if d <= 0 {
		return fmt.Errorf("config: %s: %q must be positive", name, raw)
	}

	*dst = d

	return nil
}

// EnsureDataDir creates the directory holding the database.
func (c Config) EnsureDataDir() error {
	dir := filepath.Dir(c.DatabasePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: create %s: %w", dir, err)
	}

	return nil
}
