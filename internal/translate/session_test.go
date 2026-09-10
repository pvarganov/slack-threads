package translate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSessionSendReadsUntilResult(t *testing.T) {
	s, _ := startSession(t, Config{}, func(p *fakeProcess) {
		if _, ok := p.next(); !ok {
			return
		}

		p.emit(
			`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"},"session_id":"sess-1"}`,
			initEvent("sess-1"),
			`{"type":"unknown_future_event","session_id":"sess-1"}`,
			assistantEvent("sess-1", "Перевод"),
			resultEvent("sess-1", "Перевод"),
		)

		// A second turn's events must not leak into the first one.
		_, _ = p.next()
	})

	got, err := s.Send(context.Background(), "translate this")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got != "Перевод" {
		t.Errorf("text = %q, want %q", got, "Перевод")
	}

	if s.SessionID() != "sess-1" {
		t.Errorf("SessionID = %q, want sess-1", s.SessionID())
	}
}

func TestSessionSendWritesStreamJSONRequest(t *testing.T) {
	lines := make(chan string, 1)

	s, _ := startSession(t, Config{}, func(p *fakeProcess) {
		line, ok := p.next()
		if !ok {
			return
		}

		lines <- line

		p.emit(resultEvent("sess-1", "ok"))
		_, _ = p.next()
	})

	if _, err := s.Send(context.Background(), `say "hi"`); err != nil {
		t.Fatalf("Send: %v", err)
	}

	var req struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}

	if err := json.Unmarshal([]byte(<-lines), &req); err != nil {
		t.Fatalf("request is not JSON: %v", err)
	}

	if req.Type != "user" || req.Message.Role != "user" {
		t.Errorf("request = %+v, want a user message", req)
	}

	if len(req.Message.Content) != 1 || req.Message.Content[0].Text != `say "hi"` {
		t.Errorf("content = %+v, want the prompt verbatim", req.Message.Content)
	}
}

func TestSessionSendFallsBackToAssistantBlocks(t *testing.T) {
	s, _ := startSession(t, Config{}, func(p *fakeProcess) {
		if _, ok := p.next(); !ok {
			return
		}

		p.emit(
			initEvent("sess-1"),
			`{"type":"assistant","session_id":"sess-1","message":{"content":[`+
				`{"type":"text","text":"часть 1 "},{"type":"thinking","text":"игнор"},`+
				`{"type":"text","text":"часть 2"}]}}`,
			`{"type":"result","subtype":"success","is_error":false,"session_id":"sess-1","result":""}`,
		)

		_, _ = p.next()
	})

	got, err := s.Send(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got != "часть 1 часть 2" {
		t.Errorf("text = %q, want the concatenated text blocks", got)
	}
}

func TestSessionSendReusesProcessForNextTurn(t *testing.T) {
	s, runner := startSession(t, Config{}, echoTurns("sess-1", "первый", "второй"))

	first, err := s.Send(context.Background(), "a")
	if err != nil {
		t.Fatalf("first Send: %v", err)
	}

	second, err := s.Send(context.Background(), "b")
	if err != nil {
		t.Fatalf("second Send: %v", err)
	}

	if first != "первый" || second != "второй" {
		t.Errorf("answers = %q, %q; want первый, второй", first, second)
	}

	if runner.startCount() != 1 {
		t.Errorf("started %d processes, want 1", runner.startCount())
	}
}

func TestSessionSendReportsRateLimitStatus(t *testing.T) {
	statuses := make(chan string, 4)
	cfg := Config{OnRateLimit: func(status string) { statuses <- status }}

	s, _ := startSession(t, cfg, func(p *fakeProcess) {
		if _, ok := p.next(); !ok {
			return
		}

		p.emit(
			`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected"},"session_id":"sess-1"}`,
			resultEvent("sess-1", "ok"),
		)

		_, _ = p.next()
	})

	if _, err := s.Send(context.Background(), "prompt"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case got := <-statuses:
		if got != "rejected" {
			t.Errorf("status = %q, want rejected", got)
		}
	default:
		t.Error("OnRateLimit was not called")
	}
}

func TestSessionSendProcessExitedBeforeResult(t *testing.T) {
	s, runner := startSession(t, Config{}, func(p *fakeProcess) {
		if _, ok := p.next(); !ok {
			return
		}

		p.emit(initEvent("sess-1"))
		p.emitStderr("claude: fatal: out of credits\n")
		p.exitWith(errors.New("exit status 1"))
	})

	_, err := s.Send(context.Background(), "prompt")

	var procErr *ProcessError
	if !errors.As(err, &procErr) {
		t.Fatalf("err = %v, want *ProcessError", err)
	}

	if procErr.Err == nil || !strings.Contains(procErr.Err.Error(), "exit status 1") {
		t.Errorf("ProcessError.Err = %v, want the exit status", procErr.Err)
	}

	if !strings.Contains(procErr.Stderr, "out of credits") {
		t.Errorf("ProcessError.Stderr = %q, want the stderr tail", procErr.Stderr)
	}

	if !s.Closed() {
		t.Error("session must be closed after the process died")
	}

	if _, err := s.Send(context.Background(), "again"); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("second Send err = %v, want ErrSessionClosed", err)
	}

	if runner.startCount() != 1 {
		t.Errorf("started %d processes, want 1", runner.startCount())
	}
}

func TestSessionSendInvalidJSON(t *testing.T) {
	s, runner := startSession(t, Config{}, func(p *fakeProcess) {
		if _, ok := p.next(); !ok {
			return
		}

		p.emit(initEvent("sess-1"), `{"type":"assistant" oops`)
		_, _ = p.next()
	})

	_, err := s.Send(context.Background(), "prompt")

	var protoErr *ProtocolError
	if !errors.As(err, &protoErr) {
		t.Fatalf("err = %v, want *ProtocolError", err)
	}

	if !strings.Contains(protoErr.Line, "oops") {
		t.Errorf("ProtocolError.Line = %q, want the offending line", protoErr.Line)
	}

	if !s.Closed() {
		t.Error("session must be closed after a protocol error")
	}

	if !runner.processAt(0).wasKilled() {
		t.Error("process must be killed after a protocol error")
	}
}

func TestSessionSendTurnErrors(t *testing.T) {
	tests := []struct {
		name       string
		result     string
		wantStatus string
		wantSub    string
	}{
		{
			name: "is_error flag",
			result: `{"type":"result","subtype":"success","is_error":true,"session_id":"s",` +
				`"result":"Credit balance too low","api_error_status":"429"}`,
			wantStatus: "429",
			wantSub:    "success",
		},
		{
			name: "non-success subtype",
			result: `{"type":"result","subtype":"error_during_execution","is_error":false,` +
				`"session_id":"s","result":"boom"}`,
			wantSub: "error_during_execution",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := startSession(t, Config{}, func(p *fakeProcess) {
				if _, ok := p.next(); !ok {
					return
				}

				p.emit(tc.result)
				_, _ = p.next()
			})

			_, err := s.Send(context.Background(), "prompt")

			var turnErr *TurnError
			if !errors.As(err, &turnErr) {
				t.Fatalf("err = %v, want *TurnError", err)
			}

			if turnErr.Subtype != tc.wantSub {
				t.Errorf("Subtype = %q, want %q", turnErr.Subtype, tc.wantSub)
			}

			if turnErr.APIErrorStatus != tc.wantStatus {
				t.Errorf("APIErrorStatus = %q, want %q", turnErr.APIErrorStatus, tc.wantStatus)
			}

			if turnErr.Message == "" {
				t.Error("Message must carry the result text")
			}
		})
	}
}

func TestSessionSendTimesOut(t *testing.T) {
	s, runner := startSession(t, Config{ResponseTimeout: 20 * time.Millisecond}, func(p *fakeProcess) {
		if _, ok := p.next(); !ok {
			return
		}

		p.emit(initEvent("sess-1"))
		_, _ = p.next()
	})

	_, err := s.Send(context.Background(), "prompt")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}

	if !runner.processAt(0).wasKilled() {
		t.Error("a timed out process must be killed")
	}

	if !s.Closed() {
		t.Error("session must be closed after a timeout")
	}
}

func TestSessionSendCancelledByContext(t *testing.T) {
	s, runner := startSession(t, Config{}, func(p *fakeProcess) {
		if _, ok := p.next(); !ok {
			return
		}

		p.emit(initEvent("sess-1"))
		_, _ = p.next()
	})

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	_, err := s.Send(ctx, "prompt")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	if !runner.processAt(0).wasKilled() {
		t.Error("a cancelled turn must kill the process")
	}
}

func TestSessionCloseIsIdempotent(t *testing.T) {
	s, _ := startSession(t, Config{}, echoTurns("sess-1", "ok"))

	if _, err := s.Send(context.Background(), "a"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	if _, err := s.Send(context.Background(), "b"); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("Send after Close = %v, want ErrSessionClosed", err)
	}
}

// TestSessionCloseWaitsForInFlightSend guards against the race Close and
// Send used to have over the shared stdin/events plumbing: Close must not
// start tearing the session down while a turn is still being served.
func TestSessionCloseWaitsForInFlightSend(t *testing.T) {
	started := make(chan struct{})
	proceed := make(chan struct{})

	s, _ := startSession(t, Config{}, func(p *fakeProcess) {
		if _, ok := p.next(); !ok {
			return
		}

		close(started)
		<-proceed

		p.emit(resultEvent("sess-1", "ok"))
		_, _ = p.next()
	})

	sendDone := make(chan error, 1)

	go func() {
		_, err := s.Send(context.Background(), "prompt")
		sendDone <- err
	}()

	<-started

	closeDone := make(chan error, 1)

	go func() {
		closeDone <- s.Close()
	}()

	select {
	case <-closeDone:
		t.Fatal("Close returned before the in-flight Send finished")
	case <-time.After(30 * time.Millisecond):
	}

	close(proceed)

	if err := <-sendDone; err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close never returned after the in-flight Send finished")
	}
}

func TestSessionLastUsedAdvances(t *testing.T) {
	s, _ := startSession(t, Config{}, echoTurns("sess-1", "ok"))

	before := s.LastUsed()

	time.Sleep(2 * time.Millisecond)

	if _, err := s.Send(context.Background(), "a"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if !s.LastUsed().After(before) {
		t.Errorf("LastUsed = %v, want later than %v", s.LastUsed(), before)
	}
}
