package translate

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	// defaultResponseTimeout bounds a single turn. Translating a batch of
	// long messages with opus is slow, so the budget is generous.
	defaultResponseTimeout = 10 * time.Minute
	// maxEventLine is the largest stdout line accepted; a translated batch
	// arrives as one JSON event and can be big.
	maxEventLine = 16 << 20
	// maxStderrKept caps how much stderr is remembered for error messages.
	maxStderrKept = 4 << 10
)

// streamEvent is the subset of the claude stream-json protocol the
// translator reads. Unknown types and fields are ignored on purpose — see
// docs/claude-cli-contract.md.
type streamEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Message   struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
	IsError        bool   `json:"is_error"`
	Result         string `json:"result"`
	APIErrorStatus string `json:"api_error_status"`
	RateLimitInfo  struct {
		Status string `json:"status"`
	} `json:"rate_limit_info"`
}

// readResult is one line off stdout: either a decoded event or the decoding
// error that killed the reader.
type readResult struct {
	event streamEvent
	err   error
}

// Session is one long-lived claude process dedicated to a single thread.
// Turns are serialised: the next request goes in only after the previous
// result event came out, which is what the CLI protocol requires.
type Session struct {
	threadID    string
	proc        Process
	stdin       io.WriteCloser
	events      <-chan readResult
	done        chan struct{}
	doneOnce    sync.Once
	timeout     time.Duration
	onRateLimit func(status string)

	// mu guards a turn: only one Send may be in flight.
	mu sync.Mutex
	// state guards the fields below, which Manager reads between turns.
	state     sync.Mutex
	sessionID string
	closed    bool
	lastUsed  time.Time
	busy      bool

	stderr     *stderrBuffer
	stderrDone chan struct{}
	waitOnce   sync.Once
	waitErr    error
}

// newSession wires a started process up to the event reader.
func newSession(threadID string, proc Process, timeout time.Duration, now time.Time, onRateLimit func(string)) *Session {
	if timeout <= 0 {
		timeout = defaultResponseTimeout
	}

	s := &Session{
		threadID:    threadID,
		proc:        proc,
		stdin:       proc.Stdin(),
		timeout:     timeout,
		onRateLimit: onRateLimit,
		lastUsed:    now,
		stderr:      &stderrBuffer{},
		done:        make(chan struct{}),
		stderrDone:  make(chan struct{}),
	}

	events := make(chan readResult)
	s.events = events

	go readEvents(proc.Stdout(), events, s.done)
	go func() {
		defer close(s.stderrDone)

		s.stderr.drain(proc.Stderr())
	}()

	return s
}

// readEvents decodes stdout line by line and closes the channel on EOF. It
// also stops when done is closed, so a killed session leaks no goroutine.
func readEvents(r io.Reader, out chan<- readResult, done <-chan struct{}) {
	defer close(out)

	emit := func(res readResult) bool {
		select {
		case out <- res:
			return true
		case <-done:
			return false
		}
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), maxEventLine)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var ev streamEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			emit(readResult{err: &ProtocolError{Line: truncate(line, 200), Err: err}})

			return
		}

		if !emit(readResult{event: ev}) {
			return
		}
	}

	if err := scanner.Err(); err != nil {
		emit(readResult{err: fmt.Errorf("translate: reading claude stdout: %w", err)})
	}
}

// ThreadID is the thread this session is pinned to.
func (s *Session) ThreadID() string { return s.threadID }

// SessionID is the claude session id, learned from the first event that
// carries one. It is stored on the thread and reused via --resume.
func (s *Session) SessionID() string {
	s.state.Lock()
	defer s.state.Unlock()

	return s.sessionID
}

// LastUsed is when the last turn finished; the manager reaps idle sessions
// by this timestamp.
func (s *Session) LastUsed() time.Time {
	s.state.Lock()
	defer s.state.Unlock()

	return s.lastUsed
}

// Closed reports whether the session is unusable, either because Close was
// called or because a failed turn tore the process down.
func (s *Session) Closed() bool {
	s.state.Lock()
	defer s.state.Unlock()

	return s.closed
}

// Busy reports whether a turn is currently in flight. The manager must not
// reap a busy session: LastUsed is only bumped once the turn completes, so
// a long-running turn would otherwise look idle to reapLocked while it is
// still reading from s.events, racing Close's own drain of that channel.
func (s *Session) Busy() bool {
	s.state.Lock()
	defer s.state.Unlock()

	return s.busy
}

// Send runs one turn: the prompt goes to stdin and the assistant text is
// returned once the result event arrives. Any failure kills the process,
// because a half-consumed stream cannot be reused.
func (s *Session) Send(ctx context.Context, prompt string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Closed() {
		return "", ErrSessionClosed
	}

	s.setBusy(true)
	defer s.setBusy(false)

	if err := s.writeRequest(prompt); err != nil {
		s.abort()

		return "", err
	}

	text, err := s.awaitResult(ctx)
	if err != nil {
		s.abort()

		return "", err
	}

	s.touch(time.Now())

	return text, nil
}

// writeRequest emits one user event line on stdin.
func (s *Session) writeRequest(prompt string) error {
	req := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role":    "user",
			"content": []map[string]any{{"type": "text", "text": prompt}},
		},
	}

	line, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("translate: encoding request: %w", err)
	}

	if _, err := s.stdin.Write(append(line, '\n')); err != nil {
		return &ProcessError{Err: err, Stderr: s.stderr.String()}
	}

	return nil
}

// awaitResult consumes events until the turn's result event.
func (s *Session) awaitResult(ctx context.Context) (string, error) {
	timer := time.NewTimer(s.timeout)
	defer timer.Stop()

	var assistant strings.Builder

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			return "", ErrTimeout
		case r, ok := <-s.events:
			if !ok {
				return "", &ProcessError{Err: s.wait(), Stderr: s.stderrTail()}
			}

			if r.err != nil {
				return "", r.err
			}

			text, done, err := s.consume(r.event, &assistant)
			if err != nil {
				return "", err
			}

			if done {
				return text, nil
			}
		}
	}
}

// consume folds one event into the turn, reporting whether it ended it.
func (s *Session) consume(ev streamEvent, assistant *strings.Builder) (string, bool, error) {
	s.rememberSessionID(ev.SessionID)

	switch ev.Type {
	case "assistant":
		for _, block := range ev.Message.Content {
			if block.Type == "text" {
				assistant.WriteString(block.Text)
			}
		}
	case "rate_limit_event":
		if s.onRateLimit != nil && ev.RateLimitInfo.Status != "" {
			s.onRateLimit(ev.RateLimitInfo.Status)
		}
	case "result":
		if ev.IsError || (ev.Subtype != "" && ev.Subtype != "success") {
			return "", true, &TurnError{
				Subtype:        ev.Subtype,
				Message:        truncate(ev.Result, 500),
				APIErrorStatus: ev.APIErrorStatus,
			}
		}

		// result.result is the whole answer; the assembled assistant
		// blocks are the fallback when it comes back empty.
		if ev.Result != "" {
			return ev.Result, true, nil
		}

		return assistant.String(), true, nil
	}

	return "", false, nil
}

func (s *Session) rememberSessionID(id string) {
	if id == "" {
		return
	}

	s.state.Lock()
	defer s.state.Unlock()

	if s.sessionID == "" {
		s.sessionID = id
	}
}

func (s *Session) touch(t time.Time) {
	s.state.Lock()
	defer s.state.Unlock()

	s.lastUsed = t
}

func (s *Session) setBusy(busy bool) {
	s.state.Lock()
	defer s.state.Unlock()

	s.busy = busy
}

// abort kills a session that failed mid-turn so it is never reused.
func (s *Session) abort() {
	s.state.Lock()
	s.closed = true
	s.state.Unlock()

	s.doneOnce.Do(func() { close(s.done) })

	_ = s.proc.Kill()
	_ = s.wait()
}

// Close ends the session politely: closing stdin makes claude exit with 0.
func (s *Session) Close() error {
	s.state.Lock()
	already := s.closed
	s.closed = true
	s.state.Unlock()

	if already {
		return nil
	}

	if err := s.stdin.Close(); err != nil {
		_ = s.proc.Kill()

		return fmt.Errorf("translate: closing claude stdin: %w", err)
	}

	// Drain what is left so Wait does not race the reader goroutine.
	for range s.events { //nolint:revive // draining, values are irrelevant
	}

	s.doneOnce.Do(func() { close(s.done) })

	return s.wait()
}

// wait reaps the process exactly once and caches the exit status.
func (s *Session) wait() error {
	s.waitOnce.Do(func() { s.waitErr = s.proc.Wait() })

	return s.waitErr
}

// stderrTail returns what the process printed on stderr, waiting briefly
// for the drain goroutine to catch up after the process exited.
func (s *Session) stderrTail() string {
	select {
	case <-s.stderrDone:
	case <-time.After(time.Second):
	}

	return s.stderr.String()
}

// stderrBuffer keeps the tail of stderr for error messages.
type stderrBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *stderrBuffer) drain(r io.Reader) {
	if r == nil {
		return
	}

	chunk := make([]byte, 4<<10)

	for {
		n, err := r.Read(chunk)
		if n > 0 {
			b.append(chunk[:n])
		}

		if err != nil {
			return
		}
	}
}

func (b *stderrBuffer) append(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.buf = append(b.buf, p...)
	if len(b.buf) > maxStderrKept {
		b.buf = b.buf[len(b.buf)-maxStderrKept:]
	}
}

func (b *stderrBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return truncate(string(b.buf), maxStderrKept)
}
