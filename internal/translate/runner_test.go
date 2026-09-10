package translate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// writeFakeClaude drops a shell script that speaks the stream-json protocol
// and records the arguments it was called with.
func writeFakeClaude(t *testing.T, body string) (bin, argsFile string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the fake claude binary is a shell script")
	}

	dir := t.TempDir()
	bin = filepath.Join(dir, "claude")
	argsFile = filepath.Join(dir, "args.txt")

	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n" + body

	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake claude: %v", err)
	}

	return bin, argsFile
}

func TestExecRunnerRunsRealProcess(t *testing.T) {
	body := `while IFS= read -r line; do
  printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-exec"}'
  printf '%s\n' '{"type":"assistant","session_id":"sess-exec","message":{"content":[{"type":"text","text":"привет"}]}}'
  printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"session_id":"sess-exec","result":"привет"}'
done
echo "fake claude done" >&2
`
	bin, argsFile := writeFakeClaude(t, body)

	m := NewManager(ExecRunner{Binary: bin}, Config{
		SystemPrompt:    "правила",
		ResponseTimeout: 30 * time.Second,
	})

	defer m.Close()

	s, err := m.Session(context.Background(), "T1", "sess-exec")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	got, err := s.Send(context.Background(), "переведи")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got != "привет" {
		t.Errorf("text = %q, want привет", got)
	}

	if s.SessionID() != "sess-exec" {
		t.Errorf("SessionID = %q, want sess-exec", s.SessionID())
	}

	// Closing stdin must let the real process exit cleanly.
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading recorded args: %v", err)
	}

	args := strings.Split(strings.TrimSpace(string(raw)), "\n")
	for _, want := range []string{"-p", "--verbose", "--strict-mcp-config", "--tools", "--resume", "sess-exec"} {
		if !slices.Contains(args, want) {
			t.Errorf("recorded args %v: missing %q", args, want)
		}
	}
}

func TestExecRunnerSecondTurnAndTimeout(t *testing.T) {
	body := `while IFS= read -r line; do
  sleep 5
done
`
	bin, _ := writeFakeClaude(t, body)

	m := NewManager(ExecRunner{Binary: bin}, Config{ResponseTimeout: 100 * time.Millisecond})

	defer m.Close()

	s, err := m.Session(context.Background(), "T1", "")
	if err != nil {
		t.Fatalf("Session: %v", err)
	}

	start := time.Now()

	if _, err := s.Send(context.Background(), "переведи"); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}

	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("Send blocked for %s, the timeout must kill the process", elapsed)
	}

	if !s.Closed() {
		t.Error("session must be closed after a timeout")
	}
}

func TestExecRunnerMissingBinary(t *testing.T) {
	r := ExecRunner{Binary: filepath.Join(t.TempDir(), "definitely-not-here")}

	if _, err := r.Start(context.Background(), buildArgs(Config{}, "")); err == nil {
		t.Fatal("Start must fail for a missing binary")
	}
}
