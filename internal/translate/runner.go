package translate

import (
	"context"
	"fmt"
	"io"
	"os/exec"
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

	cmd := exec.CommandContext(ctx, bin, args...)
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
		return nil, fmt.Errorf("translate: start %s: %w", bin, err)
	}

	return &execProcess{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr}, nil
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
