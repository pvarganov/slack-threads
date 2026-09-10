package translate

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DefaultModel is the model the translator asks for; quality matters more
// than latency here.
const DefaultModel = "opus"

// defaultIdleTimeout is how long an unused thread session is kept alive
// before its process is shut down. Reopening is cheap thanks to --resume.
const defaultIdleTimeout = 15 * time.Minute

// Config describes how claude processes are started.
type Config struct {
	// Model is the --model value; empty means DefaultModel.
	Model string
	// SystemPrompt is appended to the built-in system prompt and carries
	// the translation rules and the glossary.
	SystemPrompt string
	// ResponseTimeout bounds a single turn; empty means
	// defaultResponseTimeout.
	ResponseTimeout time.Duration
	// IdleTimeout is how long an idle session survives; empty means
	// defaultIdleTimeout.
	IdleTimeout time.Duration
	// OnRateLimit, when set, is called with rate_limit_info.status of
	// every rate limit event claude reports.
	OnRateLimit func(status string)
}

// buildArgs assembles the claude command line. The translator gets no
// tools at all: --tools "" disables the built-ins and --strict-mcp-config
// keeps the user's MCP servers out (both are required, see
// docs/claude-cli-contract.md).
func buildArgs(cfg Config, resumeID string) []string {
	model := cfg.Model
	if model == "" {
		model = DefaultModel
	}

	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--strict-mcp-config",
		"--tools", "",
		"--permission-mode", "dontAsk",
		"--permission-prompts", "none",
		"--model", model,
	}

	if cfg.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", cfg.SystemPrompt)
	}

	if resumeID != "" {
		// No --fork-session: the thread must keep its session id.
		args = append(args, "--resume", resumeID)
	}

	return args
}

// Manager owns one claude session per thread: lazily started, reused for
// every turn of that thread, and reaped once it has been idle for a while.
type Manager struct {
	runner Runner
	cfg    Config
	now    func() time.Time

	mu       sync.Mutex
	sessions map[string]*Session
}

// NewManager returns a manager starting processes through runner.
func NewManager(runner Runner, cfg Config) *Manager {
	return &Manager{
		runner:   runner,
		cfg:      cfg,
		now:      time.Now,
		sessions: make(map[string]*Session),
	}
}

// Session returns the session of a thread, starting a process on first use.
// resumeID is the thread's stored claude session id, if any: it lets a
// restarted app pick the conversation back up with --resume.
func (m *Manager) Session(ctx context.Context, threadID, resumeID string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.reapLocked()

	if s, ok := m.sessions[threadID]; ok {
		if !s.Closed() {
			return s, nil
		}

		delete(m.sessions, threadID)
	}

	proc, err := m.runner.Start(ctx, buildArgs(m.cfg, resumeID))
	if err != nil {
		return nil, fmt.Errorf("translate: starting session for thread %s: %w", threadID, err)
	}

	s := newSession(threadID, proc, m.cfg.ResponseTimeout, m.now(), m.cfg.OnRateLimit)
	if resumeID != "" {
		s.sessionID = resumeID
	}

	m.sessions[threadID] = s

	return s, nil
}

// CloseThread shuts the session of one thread down, e.g. when the thread is
// deleted. Unknown threads are not an error.
func (m *Manager) CloseThread(threadID string) error {
	m.mu.Lock()
	s, ok := m.sessions[threadID]
	delete(m.sessions, threadID)
	m.mu.Unlock()

	if !ok {
		return nil
	}

	return s.Close()
}

// CloseIdle shuts down every session that has been unused for longer than
// the idle timeout. It also runs implicitly on every Session call.
func (m *Manager) CloseIdle() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.reapLocked()
}

func (m *Manager) reapLocked() {
	idle := m.cfg.IdleTimeout
	if idle <= 0 {
		idle = defaultIdleTimeout
	}

	deadline := m.now().Add(-idle)

	for id, s := range m.sessions {
		if s.Closed() {
			delete(m.sessions, id)

			continue
		}

		if s.Busy() || s.LastUsed().After(deadline) {
			continue
		}

		delete(m.sessions, id)

		_ = s.Close()
	}
}

// Close shuts every session down; the manager stays usable afterwards.
func (m *Manager) Close() error {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = make(map[string]*Session)
	m.mu.Unlock()

	var firstErr error

	for _, s := range sessions {
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}
