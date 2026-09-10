package translate

import (
	"errors"
	"fmt"
	"strings"
)

// ErrSessionClosed is returned when a turn is attempted on a session that
// was closed, or that a previous failure has already torn down.
var ErrSessionClosed = errors.New("translate: session closed")

// ErrTimeout is returned when claude produced no result event within the
// session's response timeout. The process is killed in that case.
var ErrTimeout = errors.New("translate: timed out waiting for claude")

// ProcessError means the claude process died instead of finishing the turn.
type ProcessError struct {
	// Err is the underlying exec error, nil on a clean but premature exit.
	Err error
	// Stderr is what the process printed before dying, truncated.
	Stderr string
}

func (e *ProcessError) Error() string {
	var b strings.Builder

	b.WriteString("translate: claude exited before finishing the turn")

	if e.Err != nil {
		fmt.Fprintf(&b, ": %v", e.Err)
	}

	if e.Stderr != "" {
		fmt.Fprintf(&b, ": %s", e.Stderr)
	}

	return b.String()
}

// Unwrap exposes the exec error so callers can inspect the exit code.
func (e *ProcessError) Unwrap() error { return e.Err }

// ProtocolError is a line on stdout that is not a JSON event. Unknown event
// types are ignored, malformed JSON is not.
type ProtocolError struct {
	// Line is the offending stdout line, truncated.
	Line string
	// Err is the JSON decoding error.
	Err error
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("translate: bad event line %q: %v", e.Line, e.Err)
}

// Unwrap exposes the decoding error.
func (e *ProtocolError) Unwrap() error { return e.Err }

// TurnError is a result event reporting failure (`is_error` or a subtype
// other than "success").
type TurnError struct {
	// Subtype is the result subtype, e.g. "error_during_execution".
	Subtype string
	// Message is the human-readable text from the result field.
	Message string
	// APIErrorStatus is the upstream HTTP status, when the failure came
	// from the API rather than the CLI.
	APIErrorStatus string
}

func (e *TurnError) Error() string {
	var b strings.Builder

	b.WriteString("translate: turn failed")

	if e.Subtype != "" {
		fmt.Fprintf(&b, " (%s)", e.Subtype)
	}

	if e.APIErrorStatus != "" {
		fmt.Fprintf(&b, " [api %s]", e.APIErrorStatus)
	}

	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}

	return b.String()
}

// truncate keeps error messages readable when claude dumps a lot of text.
func truncate(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}

	return s[:limit] + "…"
}
