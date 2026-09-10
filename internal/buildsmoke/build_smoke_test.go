// Package buildsmoke holds the repository-wide build smoke test.
package buildsmoke

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot returns the module root, derived from this file's location.
func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine caller file")
	}

	return filepath.Join(filepath.Dir(file), "..", "..")
}

// TestAllPackagesBuild is the smoke test guarding that every package in the
// repository still compiles.
func TestAllPackagesBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build smoke test in -short mode")
	}

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = repoRoot(t)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build ./... failed: %v\n%s", err, out)
	}

	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("go build ./... produced output:\n%s", out)
	}
}

// TestAllPackagesVet guards that the repository passes go vet.
func TestAllPackagesVet(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping vet smoke test in -short mode")
	}

	cmd := exec.Command("go", "vet", "./...")
	cmd.Dir = repoRoot(t)

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go vet ./... failed: %v\n%s", err, out)
	}
}
