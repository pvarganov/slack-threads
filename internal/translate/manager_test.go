package translate

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBuildArgs(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		resumeID string
		want     []string
		absent   []string
	}{
		{
			name: "defaults isolate the translator",
			want: []string{
				"-p",
				"--input-format", "stream-json",
				"--output-format", "stream-json",
				"--verbose",
				"--strict-mcp-config",
				"--setting-sources", "",
				"--tools", "",
				"--permission-mode", "dontAsk",
				"--permission-prompts", "none",
				"--model", "opus",
			},
			absent: []string{"--resume", "--append-system-prompt", "--fork-session"},
		},
		{
			name: "model and system prompt",
			cfg:  Config{Model: "sonnet", SystemPrompt: "правила перевода"},
			want: []string{"--model", "sonnet", "--append-system-prompt", "правила перевода"},
		},
		{
			name:     "resume keeps the session id",
			resumeID: "sess-42",
			want:     []string{"--resume", "sess-42"},
			absent:   []string{"--fork-session"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildArgs(tc.cfg, tc.resumeID)

			for i := 0; i+1 < len(tc.want); i += 2 {
				if !hasPair(got, tc.want[i], tc.want[i+1]) && !slices.Contains(got, tc.want[i]) {
					t.Errorf("args %v: missing %q", got, tc.want[i])
				}
			}

			for _, flag := range tc.absent {
				if slices.Contains(got, flag) {
					t.Errorf("args %v: must not contain %q", got, flag)
				}
			}
		})
	}
}

// hasPair reports whether flag is immediately followed by value.
func hasPair(args []string, flag, value string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}

	return false
}

func TestBuildArgsDefaultArgsAreComplete(t *testing.T) {
	got := strings.Join(buildArgs(Config{}, ""), " ")
	// -p is required for the stream-json formats, --verbose for the full
	// event stream; both are easy to lose in a refactor.
	for _, want := range []string{"-p", "--verbose", "--input-format stream-json", "--output-format stream-json"} {
		if !strings.Contains(got, want) {
			t.Errorf("args %q: missing %q", got, want)
		}
	}
}

// The user's own settings must stay out of the translator: their hooks
// would rewrite the translation and add their timeout to every message.
func TestBuildArgsDropsUserSettings(t *testing.T) {
	got := buildArgs(Config{}, "")

	if !hasPair(got, "--setting-sources", "") {
		t.Errorf("args %v: missing empty --setting-sources", got)
	}
}

func TestManagerReusesSessionPerThread(t *testing.T) {
	runner := &fakeRunner{script: func(_ int, p *fakeProcess) { echoTurns("sess", "ok")(p) }}
	m := NewManager(runner, Config{})

	defer m.Close()

	first, err := m.Session(context.Background(), "T1", "")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	again, err := m.Session(context.Background(), "T1", "")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	if first != again {
		t.Error("the same thread must reuse its session")
	}

	other, err := m.Session(context.Background(), "T2", "")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	if other == first {
		t.Error("a different thread must get its own session")
	}

	if runner.startCount() != 2 {
		t.Errorf("started %d processes, want 2", runner.startCount())
	}
}

func TestManagerPassesResumeIDAndPrefillsSessionID(t *testing.T) {
	runner := &fakeRunner{script: func(_ int, p *fakeProcess) { echoTurns("sess-42", "ok")(p) }}
	m := NewManager(runner, Config{})

	defer m.Close()

	s, err := m.Session(context.Background(), "T1", "sess-42")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	if !hasPair(runner.argsAt(0), "--resume", "sess-42") {
		t.Errorf("args %v: want --resume sess-42", runner.argsAt(0))
	}

	if s.SessionID() != "sess-42" {
		t.Errorf("SessionID = %q, want the resumed id", s.SessionID())
	}
}

func TestManagerReplacesDeadSession(t *testing.T) {
	runner := &fakeRunner{script: func(call int, p *fakeProcess) {
		if call == 0 {
			_, _ = p.next()
			p.exitWith(errors.New("exit status 1"))

			return
		}

		echoTurns("sess-2", "ok")(p)
	}}
	m := NewManager(runner, Config{})

	defer m.Close()

	dead, err := m.Session(context.Background(), "T1", "")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	if _, err := dead.Send(context.Background(), "prompt"); err == nil {
		t.Fatal("Send must fail when the process dies")
	}

	fresh, err := m.Session(context.Background(), "T1", "")
	if err != nil {
		t.Fatalf("Session after death: %v", err)
	}

	if fresh == dead {
		t.Fatal("a dead session must not be handed out again")
	}

	if _, err := fresh.Send(context.Background(), "prompt"); err != nil {
		t.Errorf("Send on the fresh session: %v", err)
	}
}

func TestManagerClosesIdleSessions(t *testing.T) {
	runner := &fakeRunner{script: func(_ int, p *fakeProcess) { echoTurns("sess", "ok")(p) }}
	m := NewManager(runner, Config{IdleTimeout: time.Minute})

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }

	defer m.Close()

	idle, err := m.Session(context.Background(), "T1", "")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	now = now.Add(30 * time.Second)

	m.CloseIdle()

	if idle.Closed() {
		t.Fatal("session must survive within the idle timeout")
	}

	now = now.Add(2 * time.Minute)

	m.CloseIdle()

	if !idle.Closed() {
		t.Error("session idle past the timeout must be closed")
	}

	fresh, err := m.Session(context.Background(), "T1", "")
	if err != nil {
		t.Fatalf("Session after reaping: %v", err)
	}

	if fresh == idle {
		t.Error("a reaped session must be replaced by a new one")
	}
}

func TestManagerCloseThread(t *testing.T) {
	runner := &fakeRunner{script: func(_ int, p *fakeProcess) { echoTurns("sess", "ok")(p) }}
	m := NewManager(runner, Config{})

	defer m.Close()

	s, err := m.Session(context.Background(), "T1", "")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	if err := m.CloseThread("T1"); err != nil {
		t.Fatalf("CloseThread: %v", err)
	}

	if !s.Closed() {
		t.Error("CloseThread must close the session")
	}

	if err := m.CloseThread("unknown"); err != nil {
		t.Errorf("CloseThread on an unknown thread = %v, want nil", err)
	}
}

func TestManagerCloseClosesEverything(t *testing.T) {
	runner := &fakeRunner{script: func(_ int, p *fakeProcess) { echoTurns("sess", "ok")(p) }}
	m := NewManager(runner, Config{})

	var sessions []*Session

	for _, id := range []string{"T1", "T2", "T3"} {
		s, err := m.Session(context.Background(), id, "")
		if err != nil {
			t.Fatalf("Session %s: %v", id, err)
		}

		sessions = append(sessions, s)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for _, s := range sessions {
		if !s.Closed() {
			t.Errorf("session %s is still open", s.ThreadID())
		}
	}
}

func TestManagerStartError(t *testing.T) {
	runner := &fakeRunner{startErr: errors.New("no such file")}
	m := NewManager(runner, Config{})

	if _, err := m.Session(context.Background(), "T1", ""); err == nil {
		t.Fatal("Session must fail when the process cannot start")
	}
}
