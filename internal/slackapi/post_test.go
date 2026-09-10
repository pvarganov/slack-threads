package slackapi_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/pavelvarganov/slack-threads/internal/slackapi"
)

// postServer answers chat.postMessage with body and chat.getPermalink with
// a fixed link, recording the form it received.
func postServer(t *testing.T, body string) (*httptest.Server, *url.Values) {
	t.Helper()

	form := &url.Values{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/chat.getPermalink" {
			w.Write([]byte(`{"ok": true, "channel": "C1", "permalink": "https://acme.slack.com/archives/C1/p1700000000000200?thread_ts=1700000000.000100&cid=C1"}`))

			return
		}

		if r.Method != http.MethodPost {
			t.Errorf("chat.postMessage method = %s, want POST", r.Method)
		}

		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}

		parsed, err := url.ParseQuery(string(raw))
		if err != nil {
			t.Errorf("parse body: %v", err)
		}

		*form = parsed

		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded; charset=utf-8" {
			t.Errorf("Content-Type = %q", ct)
		}

		if auth := r.Header.Get("Authorization"); auth != "Bearer xoxp-test-token" {
			t.Errorf("Authorization = %q", auth)
		}

		w.Write([]byte(body))
	}))

	t.Cleanup(srv.Close)

	return srv, form
}

func TestPostMessageSendsToThread(t *testing.T) {
	t.Parallel()

	srv, form := postServer(t, `{
		"ok": true,
		"channel": "C1",
		"ts": "1700000000.000200",
		"message": {"text": "hi", "thread_ts": "1700000000.000100"}
	}`)
	client, _ := newClient(t, srv)

	posted, err := client.PostMessage(context.Background(), "C1", "1700000000.000100", "rolled `billing` back")
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}

	want := slackapi.Posted{
		Channel:   "C1",
		TS:        "1700000000.000200",
		ThreadTS:  "1700000000.000100",
		Permalink: "https://acme.slack.com/archives/C1/p1700000000000200?thread_ts=1700000000.000100&cid=C1",
	}
	if posted != want {
		t.Fatalf("posted = %+v, want %+v", posted, want)
	}

	if got := form.Get("channel"); got != "C1" {
		t.Errorf("channel = %q", got)
	}

	if got := form.Get("thread_ts"); got != "1700000000.000100" {
		t.Errorf("thread_ts = %q", got)
	}

	if got := form.Get("text"); got != "rolled `billing` back" {
		t.Errorf("text = %q", got)
	}
}

func TestPostMessageKeepsGoingWithoutPermalink(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/chat.getPermalink" {
			w.Write([]byte(`{"ok": false, "error": "message_not_found"}`))

			return
		}

		w.Write([]byte(`{"ok": true, "channel": "C1", "ts": "1700000000.000200"}`))
	}))
	defer srv.Close()

	client, _ := newClient(t, srv)

	posted, err := client.PostMessage(context.Background(), "C1", "1700000000.000100", "hi")
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}

	if posted.TS != "1700000000.000200" || posted.Permalink != "" {
		t.Fatalf("posted = %+v, want the message without a permalink", posted)
	}

	// thread_ts is echoed back from the request when Slack omits it.
	if posted.ThreadTS != "1700000000.000100" {
		t.Fatalf("thread_ts = %q", posted.ThreadTS)
	}
}

func TestPostMessageRejectsEmptyText(t *testing.T) {
	t.Parallel()

	srv, _ := postServer(t, `{"ok": true}`)
	client, _ := newClient(t, srv)

	if _, err := client.PostMessage(context.Background(), "C1", "1.1", "  \n "); err == nil {
		t.Fatal("PostMessage accepted an empty reply")
	}
}

func TestPostMessageAPIErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code string
		want error
	}{
		{name: "not in channel", code: "not_in_channel", want: slackapi.ErrNotInChannel},
		{name: "channel gone", code: "channel_not_found", want: slackapi.ErrChannelNotFound},
		{name: "message too long", code: "msg_too_long", want: slackapi.ErrMsgTooLong},
		{name: "parent gone", code: "cannot_reply_to_message", want: slackapi.ErrCannotReply},
		{name: "archived", code: "is_archived", want: slackapi.ErrIsArchived},
		{name: "workspace policy", code: "restricted_action_read_only_channel", want: slackapi.ErrPostRestricted},
		{name: "slack connect", code: "slack_connect_external_channel_blocked", want: slackapi.ErrPostRestricted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := postServer(t, `{"ok": false, "error": "`+tt.code+`"}`)
			client, _ := newClient(t, srv)

			_, err := client.PostMessage(context.Background(), "C1", "1.1", "hi")
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}

			// Whatever the sentinel, the raw Slack code stays reachable.
			var apiErr *slackapi.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != tt.code {
				t.Fatalf("APIError = %+v, want code %q", apiErr, tt.code)
			}
		})
	}
}

func TestPostMessageRetriesOnRateLimit(t *testing.T) {
	t.Parallel()

	var calls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat.getPermalink" {
			w.Write([]byte(`{"ok": true, "permalink": "https://acme.slack.com/archives/C1/p1700000000000200"}`))

			return
		}

		calls++

		if calls == 1 {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusTooManyRequests)

			return
		}

		// The retried POST must carry its body again.
		raw, _ := io.ReadAll(r.Body)
		if len(raw) == 0 {
			t.Error("retried request has an empty body")
		}

		w.Write([]byte(`{"ok": true, "channel": "C1", "ts": "1700000000.000200"}`))
	}))
	defer srv.Close()

	client, sleeper := newClient(t, srv)

	posted, err := client.PostMessage(context.Background(), "C1", "1700000000.000100", "hi")
	if err != nil {
		t.Fatalf("PostMessage: %v", err)
	}

	if posted.TS != "1700000000.000200" {
		t.Fatalf("posted = %+v", posted)
	}

	if len(sleeper.delays) != 1 || sleeper.delays[0].Seconds() != 3 {
		t.Fatalf("delays = %v, want one 3s wait", sleeper.delays)
	}
}

func TestPostMessageGivesUpWhenThrottled(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	client, _ := newClient(t, srv, slackapi.WithMaxRetries(2))

	_, err := client.PostMessage(context.Background(), "C1", "1.1", "hi")
	if !errors.Is(err, slackapi.ErrRateLimited) {
		t.Fatalf("error = %v, want ErrRateLimited", err)
	}
}
