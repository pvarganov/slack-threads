package translate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Process is one running claude CLI process: JSON request lines go into
// Stdin, stream-json events come out of Stdout.
type Process interface {
	// Stdin is the request stream; closing it makes claude exit.
	Stdin() io.WriteCloser
	// Stdout carries the newline-delimited event stream.
	Stdout() io.Reader
	// Stderr carries parse errors and diagnostics, never events.
	Stderr() io.Reader
	// Wait blocks until the process is gone and reports its exit status.
	Wait() error
	// Kill terminates a process that stopped answering.
	Kill() error
}

// Runner starts claude processes. Production uses ExecRunner; tests
// substitute a scripted implementation so no binary is ever launched.
type Runner interface {
	Start(ctx context.Context, args []string) (Process, error)
}

// DefaultBinary is the executable looked up in PATH when no path is set.
const DefaultBinary = "claude"

// ExecRunner starts the real claude binary.
type ExecRunner struct {
	// Binary is the claude executable; empty means DefaultBinary from PATH.
	Binary string
	// Dir is the working directory of the process. The translator never
	// touches files, so an empty temp-ish directory is a fine choice.
	Dir string
	// Env, when non-nil, replaces the inherited environment.
	Env []string
}

// Start launches the binary with the given arguments and wires up the pipes.
func (r ExecRunner) Start(ctx context.Context, args []string) (Process, error) {
	bin := r.Binary
	if bin == "" {
		bin = DefaultBinary
	}

	cmd := exec.CommandContext(ctx, resolveBinary(bin), args...)
	cmd.Dir = r.Dir
	cmd.Env = r.Env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("translate: stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("translate: stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("translate: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		if isMissingBinary(err) {
			return nil, fmt.Errorf("translate: start %s: %w: %w", bin, ErrBinaryNotFound, err)
		}

		return nil, fmt.Errorf("translate: start %s: %w", bin, err)
	}

	return &execProcess{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr}, nil
}

// isMissingBinary reports a start failure caused by the executable itself
// being absent or not runnable, as opposed to a pipe or resource problem.
func isMissingBinary(err error) bool {
	return errors.Is(err, exec.ErrNotFound) ||
		errors.Is(err, fs.ErrNotExist) ||
		errors.Is(err, fs.ErrPermission)
}

type execProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.Reader
	stderr io.Reader
}

func (p *execProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *execProcess) Stdout() io.Reader     { return p.stdout }
func (p *execProcess) Stderr() io.Reader     { return p.stderr }
func (p *execProcess) Wait() error           { return p.cmd.Wait() }

func (p *execProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}

	return p.cmd.Process.Kill()
}

// installDirs are the usual homes of the claude CLI, relative to the home
// directory when they start with "~".
var installDirs = []string{
	"~/.local/bin",
	"~/.claude/local",
	"/opt/homebrew/bin",
	"/usr/local/bin",
}

// resolveBinary turns a bare executable name into a path. PATH is tried
// first, then the usual install locations: a bundled app started from
// Finder inherits launchd's PATH (/usr/bin:/bin:/usr/sbin:/sbin), which
// never contains claude, so without this the app only works when it is
// launched from a shell. An unresolvable name is returned as it is, so
// the caller still reports the familiar "not found" error.
func resolveBinary(bin string) string {
	if strings.ContainsRune(bin, filepath.Separator) {
		return bin
	}

	if path, err := exec.LookPath(bin); err == nil {
		return path
	}

	home, err := os.UserHomeDir()

	for _, dir := range installDirs {
		if strings.HasPrefix(dir, "~/") {
			if err != nil {
				continue
			}

			dir = filepath.Join(home, dir[2:])
		}

		candidate := filepath.Join(dir, bin)
		if isExecutableFile(candidate) {
			return candidate
		}
	}

	return bin
}

// isExecutableFile reports whether the path is a regular file the current
// user may run.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
