package config_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pavelvarganov/slack-threads/internal/config"
	"github.com/pavelvarganov/slack-threads/internal/slackapi"
)

func TestValidateFormat(t *testing.T) {
	cases := map[string]struct {
		token string
		ok    bool
	}{
		"user token":  {"xoxp-123-456", true},
		"padded":      {"  xoxp-123-456  ", true},
		"bot token":   {"xoxb-123-456", false},
		"legacy":      {"xoxa-123", false},
		"empty":       {"", false},
		"blank":       {"   ", false},
		"prefix only": {"xoxp-", false},
		"random junk": {"hello", false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := config.ValidateFormat(tc.token)

			if tc.ok && err != nil {
				t.Fatalf("ValidateFormat(%q) = %v, want nil", tc.token, err)
			}

			if !tc.ok {
				if err == nil {
					t.Fatalf("ValidateFormat(%q) = nil, want error", tc.token)
				}

				if !errors.Is(err, config.ErrTokenFormat) {
					t.Errorf("error = %v, want ErrTokenFormat", err)
				}
			}
		})
	}
}

// okChecker records the token it was called with and reports success.
func okChecker(seen *string) config.TokenChecker {
	return func(_ context.Context, token string) (slackapi.AuthInfo, error) {
		*seen = token

		return slackapi.AuthInfo{UserID: "U1", User: "pavel", TeamID: "T1", Team: "Acme"}, nil
	}
}

func failChecker(err error) config.TokenChecker {
	return func(context.Context, string) (slackapi.AuthInfo, error) {
		return slackapi.AuthInfo{}, err
	}
}

func TestCheckTokenSuccess(t *testing.T) {
	var seen string

	info, err := config.CheckToken(context.Background(), "  xoxp-good  ", okChecker(&seen))
	if err != nil {
		t.Fatalf("CheckToken: %v", err)
	}

	if seen != "xoxp-good" {
		t.Errorf("checker got %q, want the trimmed token", seen)
	}

	if info.User != "pavel" || info.Team != "Acme" {
		t.Errorf("AuthInfo = %+v", info)
	}
}

func TestCheckTokenBadFormatSkipsNetwork(t *testing.T) {
	called := false
	check := func(context.Context, string) (slackapi.AuthInfo, error) {
		called = true

		return slackapi.AuthInfo{}, nil
	}

	_, err := config.CheckToken(context.Background(), "xoxb-bot", check)
	if err == nil {
		t.Fatal("CheckToken: want error, got nil")
	}

	if called {
		t.Error("checker was called for a malformed token")
	}

	var problem *config.TokenProblem
	if !errors.As(err, &problem) {
		t.Fatalf("error = %T, want *TokenProblem", err)
	}

	if !problem.Fixable {
		t.Error("a malformed token must be reported as fixable")
	}

	if !strings.Contains(problem.Message, "xoxp-") {
		t.Errorf("message = %q, want it to mention the expected prefix", problem.Message)
	}

	if !errors.Is(err, config.ErrTokenFormat) {
		t.Error("problem does not unwrap to ErrTokenFormat")
	}
}

func TestCheckTokenSlackErrors(t *testing.T) {
	cases := map[string]struct {
		err         error
		wantFixable bool
		wantIn      string
	}{
		"invalid_auth":  {slackapi.ErrInvalidAuth, true, "отклонил токен"},
		"missing_scope": {slackapi.ErrMissingScope, true, "не хватает прав"},
		"rate limited":  {&slackapi.RateLimitError{Method: "auth.test"}, false, "частоту запросов"},
		"network":       {errors.New("dial tcp: no route to host"), false, "Не удалось проверить токен"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := config.CheckToken(context.Background(), "xoxp-x", failChecker(tc.err))

			var problem *config.TokenProblem
			if !errors.As(err, &problem) {
				t.Fatalf("error = %v (%T), want *TokenProblem", err, err)
			}

			if problem.Fixable != tc.wantFixable {
				t.Errorf("Fixable = %v, want %v", problem.Fixable, tc.wantFixable)
			}

			if !strings.Contains(problem.Message, tc.wantIn) {
				t.Errorf("message = %q, want it to contain %q", problem.Message, tc.wantIn)
			}

			if !errors.Is(err, tc.err) {
				t.Error("problem does not unwrap to the underlying error")
			}
		})
	}
}

func TestCheckTokenWithoutChecker(t *testing.T) {
	if _, err := config.CheckToken(context.Background(), "xoxp-x", nil); err == nil {
		t.Fatal("CheckToken(nil checker): want error, got nil")
	}
}

func TestSlackCheckerCallsAuthTest(t *testing.T) {
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok": true, "user": "pavel", "user_id": "U1", "team": "Acme", "team_id": "T1"}`))
	}))
	defer srv.Close()

	info, err := config.SlackChecker(slackapi.WithBaseURL(srv.URL))(context.Background(), "xoxp-live")
	if err != nil {
		t.Fatalf("SlackChecker: %v", err)
	}

	if gotAuth != "Bearer xoxp-live" {
		t.Errorf("Authorization = %q", gotAuth)
	}

	if info.UserID != "U1" {
		t.Errorf("AuthInfo = %+v", info)
	}
}

func TestResolveTokenSuccess(t *testing.T) {
	var seen string

	store := &memStore{token: "xoxp-stored"}

	token, info, err := config.ResolveToken(context.Background(), store, okChecker(&seen))
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}

	if token != "xoxp-stored" {
		t.Errorf("token = %q", token)
	}

	if info.UserID != "U1" {
		t.Errorf("AuthInfo = %+v", info)
	}
}

func TestResolveTokenMissing(t *testing.T) {
	_, _, err := config.ResolveToken(context.Background(), &memStore{}, failChecker(errors.New("must not be called")))

	var problem *config.TokenProblem
	if !errors.As(err, &problem) {
		t.Fatalf("error = %v, want *TokenProblem", err)
	}

	if !problem.Fixable {
		t.Error("a missing token must be reported as fixable")
	}

	if !strings.Contains(problem.Message, "не задан") {
		t.Errorf("message = %q", problem.Message)
	}

	if !errors.Is(err, config.ErrNoToken) {
		t.Error("problem does not unwrap to ErrNoToken")
	}
}

func TestResolveTokenStoreFailure(t *testing.T) {
	boom := errors.New("keychain is locked")

	_, _, err := config.ResolveToken(context.Background(), &memStore{getErr: boom}, okChecker(new(string)))

	var problem *config.TokenProblem
	if !errors.As(err, &problem) {
		t.Fatalf("error = %v, want *TokenProblem", err)
	}

	if problem.Fixable {
		t.Error("a locked keychain is not fixed by entering a token")
	}

	if !errors.Is(err, boom) {
		t.Error("problem does not unwrap to the store error")
	}
}

func TestResolveTokenRejectedBySlack(t *testing.T) {
	_, _, err := config.ResolveToken(
		context.Background(),
		&memStore{token: "xoxp-revoked"},
		failChecker(slackapi.ErrInvalidAuth),
	)

	if !errors.Is(err, slackapi.ErrInvalidAuth) {
		t.Fatalf("error = %v, want ErrInvalidAuth", err)
	}
}
