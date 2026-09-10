package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pavelvarganov/slack-threads/internal/config"
	"github.com/pavelvarganov/slack-threads/internal/slackapi"
)

// fakeStore is an in-memory TokenStore standing in for the keychain.
type fakeStore struct {
	token string
	// setErr, when set, is what SetToken returns instead of storing.
	setErr error
	// getErr, when set, is what Token returns instead of the token.
	getErr error
}

func (s *fakeStore) Token() (string, error) {
	switch {
	case s.getErr != nil:
		return "", s.getErr
	case s.token == "":
		return "", config.ErrNoToken
	}

	return s.token, nil
}

func (s *fakeStore) SetToken(token string) error {
	if s.setErr != nil {
		return s.setErr
	}

	s.token = token

	return nil
}

func (s *fakeStore) DeleteToken() error {
	s.token = ""

	return nil
}

// okChecker accepts every token and records the last one it saw.
func okChecker(seen *string) config.TokenChecker {
	return func(_ context.Context, token string) (slackapi.AuthInfo, error) {
		if seen != nil {
			*seen = token
		}

		return slackapi.AuthInfo{UserID: "U1", User: "pavel", TeamID: "T1", Team: "Acme"}, nil
	}
}

func failingChecker(err error) config.TokenChecker {
	return func(context.Context, string) (slackapi.AuthInfo, error) {
		return slackapi.AuthInfo{}, err
	}
}

func TestTokenStatusEmptyBeforeCheck(t *testing.T) {
	a := New(WithTokenStore(&fakeStore{}), WithTokenChecker(okChecker(nil)))

	if status := a.TokenStatus(); status.OK || status.Message != "" {
		t.Fatalf("TokenStatus before startup = %+v, want the zero value", status)
	}
}

func TestCheckTokenReportsMissingToken(t *testing.T) {
	a := New(WithTokenStore(&fakeStore{}), WithTokenChecker(okChecker(nil)))

	status := a.CheckToken()
	if status.OK {
		t.Fatal("CheckToken reported OK without a stored token")
	}

	if !status.Fixable {
		t.Error("a missing token must be fixable from the UI")
	}

	if !strings.Contains(status.Message, "не задан") {
		t.Errorf("Message = %q", status.Message)
	}

	if cached := a.TokenStatus(); cached != status {
		t.Errorf("cached status = %+v, want %+v", cached, status)
	}
}

func TestCheckTokenReportsRejectedToken(t *testing.T) {
	a := New(
		WithTokenStore(&fakeStore{token: "xoxp-revoked"}),
		WithTokenChecker(failingChecker(slackapi.ErrInvalidAuth)),
	)

	status := a.CheckToken()
	if status.OK {
		t.Fatal("CheckToken accepted a revoked token")
	}

	if !strings.Contains(status.Message, "отклонил токен") {
		t.Errorf("Message = %q", status.Message)
	}

	if !status.Fixable {
		t.Error("a revoked token must be fixable from the UI")
	}
}

func TestCheckTokenReportsRateLimitAsNotFixable(t *testing.T) {
	a := New(
		WithTokenStore(&fakeStore{token: "xoxp-x"}),
		WithTokenChecker(failingChecker(&slackapi.RateLimitError{Method: "auth.test"})),
	)

	status := a.CheckToken()
	if status.Fixable {
		t.Error("a rate limit is not fixed by entering another token")
	}

	if status.OK {
		t.Error("a throttled check must not report OK")
	}
}

func TestSaveTokenStoresAndVerifies(t *testing.T) {
	store := &fakeStore{}

	var seen string

	a := New(WithTokenStore(store), WithTokenChecker(okChecker(&seen)))

	status := a.SaveToken("xoxp-new")
	if !status.OK {
		t.Fatalf("SaveToken = %+v, want OK", status)
	}

	if store.token != "xoxp-new" {
		t.Errorf("stored token = %q", store.token)
	}

	if seen != "xoxp-new" {
		t.Errorf("checker saw %q", seen)
	}

	if cached := a.TokenStatus(); !cached.OK || cached.User != "pavel" {
		t.Errorf("cached status = %+v", cached)
	}
}

func TestSaveTokenRejectsBadFormatWithoutStoring(t *testing.T) {
	store := &fakeStore{}
	a := New(WithTokenStore(store), WithTokenChecker(okChecker(nil)))

	status := a.SaveToken("xoxb-bot")
	if status.OK {
		t.Fatal("SaveToken accepted a bot token")
	}

	if !status.Fixable {
		t.Error("a malformed token must be fixable")
	}

	if store.token != "" {
		t.Errorf("a malformed token was stored: %q", store.token)
	}
}

func TestSaveTokenReportsStorageFailure(t *testing.T) {
	a := New(
		WithTokenStore(&fakeStore{setErr: config.ErrReadOnlyStore}),
		WithTokenChecker(okChecker(nil)),
	)

	status := a.SaveToken("xoxp-new")
	if status.OK {
		t.Fatal("SaveToken reported OK after a failed write")
	}

	if !strings.Contains(status.Message, "SLACK_THREADS_TOKEN") {
		t.Errorf("Message = %q, want it to name the environment variable", status.Message)
	}
}

func TestSaveTokenReportsKeychainFailure(t *testing.T) {
	a := New(
		WithTokenStore(&fakeStore{setErr: errors.New("keychain is locked")}),
		WithTokenChecker(okChecker(nil)),
	)

	status := a.SaveToken("xoxp-new")
	if status.OK || !strings.Contains(status.Message, "keychain is locked") {
		t.Errorf("status = %+v", status)
	}
}

func TestCheckTokenUsesStoredContext(t *testing.T) {
	type key struct{}

	var got any

	checker := func(ctx context.Context, _ string) (slackapi.AuthInfo, error) {
		got = ctx.Value(key{})

		return slackapi.AuthInfo{}, nil
	}

	a := New(WithTokenStore(&fakeStore{token: "xoxp-x"}), WithTokenChecker(checker))
	a.startup(context.WithValue(context.Background(), key{}, "v"))

	if got != "v" {
		t.Errorf("checker context value = %v, want %q", got, "v")
	}
}
