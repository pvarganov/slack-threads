package translate

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

// fakeProcess is a scripted stand-in for a claude process: the test writes
// the stream-json events the session is supposed to read.
type fakeProcess struct {
	stdinW  *io.PipeWriter
	stdinR  *io.PipeReader
	reqs    *bufio.Scanner
	stdoutW *io.PipeWriter
	stdoutR *io.PipeReader
	stderrW *io.PipeWriter
	stderrR *io.PipeReader

	done chan struct{}

	mu      sync.Mutex
	exitErr error
	killed  bool
}

func newFakeProcess(script func(p *fakeProcess)) *fakeProcess {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()

	p := &fakeProcess{
		stdinW:  stdinW,
		stdinR:  stdinR,
		reqs:    bufio.NewScanner(stdinR),
		stdoutW: stdoutW,
		stdoutR: stdoutR,
		stderrW: stderrW,
		stderrR: stderrR,
		done:    make(chan struct{}),
	}

	go func() {
		defer close(p.done)
		defer p.stdoutW.Close()
		defer p.stderrW.Close()

		script(p)
	}()

	return p
}

func (p *fakeProcess) Stdin() io.WriteCloser { return p.stdinW }
func (p *fakeProcess) Stdout() io.Reader     { return p.stdoutR }
func (p *fakeProcess) Stderr() io.Reader     { return p.stderrR }

func (p *fakeProcess) Wait() error {
	<-p.done

	p.mu.Lock()
	defer p.mu.Unlock()

	return p.exitErr
}

func (p *fakeProcess) Kill() error {
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()

	_ = p.stdinR.CloseWithError(errors.New("killed"))
	_ = p.stdoutW.CloseWithError(errors.New("killed"))
	_ = p.stderrW.CloseWithError(errors.New("killed"))

	return nil
}

func (p *fakeProcess) wasKilled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.killed
}

// next returns the next request line written to stdin.
func (p *fakeProcess) next() (string, bool) {
	if !p.reqs.Scan() {
		return "", false
	}

	return p.reqs.Text(), true
}

// emit writes event lines to stdout.
func (p *fakeProcess) emit(lines ...string) {
	for _, line := range lines {
		if _, err := io.WriteString(p.stdoutW, line+"\n"); err != nil {
			return
		}
	}
}

// emitStderr writes diagnostics to the process' stderr.
func (p *fakeProcess) emitStderr(text string) {
	_, _ = io.WriteString(p.stderrW, text)
}

// exitWith makes Wait report err once the script returns.
func (p *fakeProcess) exitWith(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.exitErr = err
}

// fakeRunner hands out scripted processes and records the arguments the
// manager asked for.
type fakeRunner struct {
	script func(call int, p *fakeProcess)

	mu        sync.Mutex
	calls     [][]string
	processes []*fakeProcess
	startErr  error
}

func (r *fakeRunner) Start(_ context.Context, args []string) (Process, error) {
	r.mu.Lock()

	if r.startErr != nil {
		err := r.startErr
		r.mu.Unlock()

		return nil, err
	}

	call := len(r.calls)
	r.calls = append(r.calls, args)
	r.mu.Unlock()

	p := newFakeProcess(func(p *fakeProcess) { r.script(call, p) })

	r.mu.Lock()
	r.processes = append(r.processes, p)
	r.mu.Unlock()

	return p, nil
}

func (r *fakeRunner) startCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.calls)
}

func (r *fakeRunner) argsAt(i int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.calls[i]
}

func (r *fakeRunner) processAt(i int) *fakeProcess {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.processes[i]
}

// echoTurns answers every request with a canned successful turn.
func echoTurns(sessionID string, replies ...string) func(p *fakeProcess) {
	return func(p *fakeProcess) {
		for i := 0; ; i++ {
			if _, ok := p.next(); !ok {
				return
			}

			reply := "ok"
			if i < len(replies) {
				reply = replies[i]
			}

			p.emit(
				initEvent(sessionID),
				assistantEvent(sessionID, reply),
				resultEvent(sessionID, reply),
			)
		}
	}
}

func initEvent(sessionID string) string {
	return `{"type":"system","subtype":"init","session_id":"` + sessionID +
		`","tools":[],"model":"claude-opus-5","permissionMode":"dontAsk"}`
}

func assistantEvent(sessionID, text string) string {
	return `{"type":"assistant","session_id":"` + sessionID +
		`","message":{"role":"assistant","content":[{"type":"text","text":"` + jsonEscape(text) + `"}]}}`
}

func resultEvent(sessionID, text string) string {
	return `{"type":"result","subtype":"success","is_error":false,"session_id":"` + sessionID +
		`","result":"` + jsonEscape(text) + `"}`
}

func jsonEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

	return r.Replace(s)
}

// startSession spins up a session backed by a scripted process.
func startSession(t *testing.T, cfg Config, script func(p *fakeProcess)) (*Session, *fakeRunner) {
	t.Helper()

	runner := &fakeRunner{script: func(_ int, p *fakeProcess) { script(p) }}
	m := NewManager(runner, cfg)

	s, err := m.Session(context.Background(), "T1", "")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	t.Cleanup(func() { _ = m.Close() })

	return s, runner
}
