package app

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/pavelvarganov/slack-threads/internal/config"
	"github.com/pavelvarganov/slack-threads/internal/permalink"
	"github.com/pavelvarganov/slack-threads/internal/slackapi"
	"github.com/pavelvarganov/slack-threads/internal/store"
	syncsvc "github.com/pavelvarganov/slack-threads/internal/sync"
	"github.com/pavelvarganov/slack-threads/internal/translate"
)

func TestUserErrorPassesNilThrough(t *testing.T) {
	if err := userError(nil); err != nil {
		t.Fatalf("userError(nil) = %v, want nil", err)
	}
}

func TestUserErrorKeepsAnAlreadyTranslatedError(t *testing.T) {
	want := &UserError{Message: "Готово", Fixable: true}

	var got *UserError
	if !errors.As(userError(want), &got) {
		t.Fatal("userError dropped the UserError")
	}

	if got != want {
		t.Fatalf("userError re-wrapped a UserError: %#v", got)
	}
}

func TestUserErrorMessages(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		want    string
		fixable bool
	}{
		{
			name: "invalid link", err: permalink.ErrInvalid, want: "ссылк", fixable: true,
		},
		{
			name: "duplicate", err: store.ErrThreadExists, want: "уже добавлен",
		},
		{
			name: "missing thread", err: store.ErrNotFound, want: "не найден",
		},
		{
			name: "busy", err: ErrBusy, want: "обновляется",
		},
		{
			name: "empty reply", err: syncsvc.ErrEmptyReply, want: "пустой", fixable: true,
		},
		{
			name: "bad token", err: slackapi.ErrInvalidAuth, want: "токен", fixable: true,
		},
		{
			name: "not in channel", err: slackapi.ErrNotInChannel, want: "не состоите", fixable: true,
		},
		{
			name: "no channel", err: slackapi.ErrChannelNotFound, want: "Канал недоступен", fixable: true,
		},
		{
			name: "deleted thread", err: slackapi.ErrThreadNotFound, want: "Тред недоступен",
		},
		{
			name: "empty thread", err: syncsvc.ErrEmptyThread, want: "Тред недоступен",
		},
		{
			name: "slack connect", err: slackapi.ErrPostRestricted, want: "Slack Connect",
		},
		{
			name:    "rate limited",
			err:     &slackapi.RateLimitError{Method: "conversations.replies", Attempts: 4},
			want:    "ограничил частоту",
			fixable: true,
		},
		{
			name:    "http error",
			err:     &slackapi.HTTPError{Method: "chat.postMessage", StatusCode: 503},
			want:    "HTTP 503",
			fixable: true,
		},
		{
			name: "translator timeout", err: translate.ErrTimeout, want: "не ответил", fixable: true,
		},
		{
			name: "session closed", err: translate.ErrSessionClosed, want: "Сессия переводчика", fixable: true,
		},
		{
			name:    "claude missing",
			err:     &translate.ProcessError{Err: errors.New("exec: \"claude\": not found")},
			want:    "запустить claude",
			fixable: true,
		},
		{
			name:    "turn failed",
			err:     &translate.TurnError{Subtype: "error_during_execution", Message: "usage limit"},
			want:    "usage limit",
			fixable: true,
		},
		{
			name:    "bad answer",
			err:     &translate.ResponseError{Reason: "not json"},
			want:    "неожиданный ответ",
			fixable: true,
		},
		{
			name:    "protocol error",
			err:     &translate.ProtocolError{Line: "oops", Err: errors.New("invalid character")},
			want:    "неожиданный ответ",
			fixable: true,
		},
		{
			name: "cancelled", err: context.Canceled, want: "прервана",
		},
		{
			name: "deadline", err: context.DeadlineExceeded, want: "не уложилась", fixable: true,
		},
		{
			name:    "network down",
			err:     &net.OpError{Op: "dial", Err: errors.New("connection refused")},
			want:    "Нет связи",
			fixable: true,
		},
		{
			name:    "token problem",
			err:     &config.TokenProblem{Message: "Токен Slack не задан.", Fixable: true},
			want:    "Токен Slack не задан.",
			fixable: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := userError(tc.err)

			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("message = %q, want it to contain %q", err, tc.want)
			}

			var ue *UserError
			if !errors.As(err, &ue) {
				t.Fatalf("error = %#v, want *UserError", err)
			}

			if ue.Fixable != tc.fixable {
				t.Errorf("fixable = %v, want %v", ue.Fixable, tc.fixable)
			}

			if !errors.Is(err, tc.err) {
				t.Error("the cause is no longer reachable through errors.Is")
			}
		})
	}
}

func TestUserErrorFallsBackToTheRawText(t *testing.T) {
	err := userError(errors.New("database is locked"))

	if !strings.Contains(err.Error(), "database is locked") {
		t.Errorf("message = %q, want the cause spelled out", err)
	}

	var ue *UserError
	if !errors.As(err, &ue) || ue.Fixable {
		t.Errorf("error = %#v, want a non-fixable UserError", err)
	}
}

func TestWrappedErrorsAreStillRecognised(t *testing.T) {
	wrapped := &UserError{}
	if errors.As(userError(errors.Join(errStub, slackapi.ErrInvalidAuth)), &wrapped); !wrapped.Fixable {
		t.Errorf("wrapped invalid_auth = %+v, want fixable", wrapped)
	}
}
