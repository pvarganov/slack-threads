package slackapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pavelvarganov/slack-threads/internal/slackapi"
)

func TestAuthTestReturnsIdentity(t *testing.T) {
	t.Parallel()

	var gotAuth, gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok": true, "url": "https://acme.slack.com/", "team": "Acme",
			"user": "pavel", "team_id": "T1", "user_id": "U1"}`))
	}))
	defer srv.Close()

	c := slackapi.New("xoxp-test-token", slackapi.WithBaseURL(srv.URL))

	info, err := c.AuthTest(context.Background())
	if err != nil {
		t.Fatalf("AuthTest: %v", err)
	}

	if gotPath != "/auth.test" {
		t.Errorf("path = %q, want /auth.test", gotPath)
	}

	if gotAuth != "Bearer xoxp-test-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}

	want := slackapi.AuthInfo{
		UserID: "U1", User: "pavel", TeamID: "T1", Team: "Acme",
		URL: "https://acme.slack.com/",
	}
	if info != want {
		t.Errorf("AuthInfo = %+v, want %+v", info, want)
	}
}

func TestAuthTestInvalidToken(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok": false, "error": "invalid_auth"}`))
	}))
	defer srv.Close()

	c := slackapi.New("xoxp-bad", slackapi.WithBaseURL(srv.URL))

	if _, err := c.AuthTest(context.Background()); !errors.Is(err, slackapi.ErrInvalidAuth) {
		t.Fatalf("AuthTest error = %v, want ErrInvalidAuth", err)
	}
}

func TestAuthTestMissingScope(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok": false, "error": "missing_scope"}`))
	}))
	defer srv.Close()

	c := slackapi.New("xoxp-test-token", slackapi.WithBaseURL(srv.URL))

	if _, err := c.AuthTest(context.Background()); !errors.Is(err, slackapi.ErrMissingScope) {
		t.Fatalf("AuthTest error = %v, want ErrMissingScope", err)
	}
}
