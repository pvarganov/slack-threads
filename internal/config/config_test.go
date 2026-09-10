package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pavelvarganov/slack-threads/internal/config"
	"github.com/pavelvarganov/slack-threads/internal/translate"
)

// envMap turns a map into the lookup LoadFrom expects.
func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.LoadFrom(envMap(nil))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}

	wantDB := filepath.Join(dir, config.AppDir, config.DBFile)
	if cfg.DatabasePath != wantDB {
		t.Errorf("DatabasePath = %q, want %q", cfg.DatabasePath, wantDB)
	}

	if cfg.ClaudeBinary != translate.DefaultBinary {
		t.Errorf("ClaudeBinary = %q, want %q", cfg.ClaudeBinary, translate.DefaultBinary)
	}

	if cfg.Model != translate.DefaultModel {
		t.Errorf("Model = %q, want %q", cfg.Model, translate.DefaultModel)
	}

	if cfg.ResponseTimeout != config.DefaultResponseTimeout {
		t.Errorf("ResponseTimeout = %s", cfg.ResponseTimeout)
	}

	if cfg.IdleTimeout != config.DefaultIdleTimeout {
		t.Errorf("IdleTimeout = %s", cfg.IdleTimeout)
	}

	if cfg.SlackTimeout != config.DefaultSlackTimeout {
		t.Errorf("SlackTimeout = %s", cfg.SlackTimeout)
	}
}

func TestLoadFromEnvOverrides(t *testing.T) {
	cfg, err := config.LoadFrom(envMap(map[string]string{
		config.EnvDBPath:          "/tmp/x/threads.db",
		config.EnvClaudeBinary:    "/opt/bin/claude",
		config.EnvModel:           "sonnet",
		config.EnvResponseTimeout: "90s",
		config.EnvIdleTimeout:     "2m",
		config.EnvSlackTimeout:    "5s",
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	want := config.Config{
		DatabasePath:    "/tmp/x/threads.db",
		ClaudeBinary:    "/opt/bin/claude",
		Model:           "sonnet",
		ResponseTimeout: 90 * time.Second,
		IdleTimeout:     2 * time.Minute,
		SlackTimeout:    5 * time.Second,
	}
	if cfg != want {
		t.Errorf("Config = %+v, want %+v", cfg, want)
	}
}

func TestLoadFromIgnoresBlankOverrides(t *testing.T) {
	cfg, err := config.LoadFrom(envMap(map[string]string{
		config.EnvDBPath:       "   ",
		config.EnvModel:        "",
		config.EnvSlackTimeout: "  ",
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	def, err := config.Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}

	if cfg != def {
		t.Errorf("Config = %+v, want defaults %+v", cfg, def)
	}
}

func TestLoadFromRejectsBadDurations(t *testing.T) {
	cases := map[string]map[string]string{
		"not a duration": {config.EnvResponseTimeout: "soon"},
		"zero":           {config.EnvIdleTimeout: "0s"},
		"negative":       {config.EnvSlackTimeout: "-1s"},
	}

	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := config.LoadFrom(envMap(env)); err == nil {
				t.Fatal("LoadFrom: want error, got nil")
			}
		})
	}
}

func TestLoadReadsProcessEnvironment(t *testing.T) {
	t.Setenv(config.EnvDBPath, "/tmp/from-process/threads.db")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.DatabasePath != "/tmp/from-process/threads.db" {
		t.Errorf("DatabasePath = %q", cfg.DatabasePath)
	}
}

func TestEnsureDataDirCreatesDirectory(t *testing.T) {
	root := t.TempDir()
	cfg := config.Config{DatabasePath: filepath.Join(root, "nested", "deep", config.DBFile)}

	if err := cfg.EnsureDataDir(); err != nil {
		t.Fatalf("EnsureDataDir: %v", err)
	}

	info, err := os.Stat(filepath.Join(root, "nested", "deep"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if !info.IsDir() {
		t.Fatal("data path is not a directory")
	}

	// Calling it twice must stay a no-op.
	if err := cfg.EnsureDataDir(); err != nil {
		t.Fatalf("EnsureDataDir twice: %v", err)
	}
}

func TestEnsureDataDirReportsFailure(t *testing.T) {
	root := t.TempDir()

	blocker := filepath.Join(root, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg := config.Config{DatabasePath: filepath.Join(blocker, "sub", config.DBFile)}

	err := cfg.EnsureDataDir()
	if err == nil {
		t.Fatal("EnsureDataDir: want error, got nil")
	}

	if !strings.Contains(err.Error(), "config: create") {
		t.Errorf("error = %v", err)
	}
}
