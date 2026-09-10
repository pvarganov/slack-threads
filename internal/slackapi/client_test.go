package slackapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pavelvarganov/slack-threads/internal/slackapi"
)

// noSleep records the delays a retrying call asked for instead of waiting.
type noSleep struct {
	delays []time.Duration
}

func (n *noSleep) sleep(_ context.Context, d time.Duration) error {
	n.delays = append(n.delays, d)

	return nil
}

// newClient wires a client against a test server with instant retries.
func newClient(t *testing.T, srv *httptest.Server, opts ...slackapi.Option) (*slackapi.HTTPClient, *noSleep) {
	t.Helper()

	sleeper := &noSleep{}
	base := []slackapi.Option{
		slackapi.WithBaseURL(srv.URL),
		slackapi.WithSleep(sleeper.sleep),
	}

	return slackapi.New("xoxp-test-token", append(base, opts...)...), sleeper
}

func TestFetchThreadSinglePage(t *testing.T) {
	t.Parallel()

	var gotAuth, gotPath, gotQuery string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"ok": true,
			"has_more": false,
			"messages": [
				{"ts": "1788872615.903009", "user": "U1", "text": "parent"},
				{"ts": "1788872700.100100", "user": "U2", "text": "reply", "thread_ts": "1788872615.903009"}
			]
		}`))
	}))
	defer srv.Close()

	client, _ := newClient(t, srv)

	msgs, err := client.FetchThread(context.Background(), "C123", "1788872615.903009")
	if err != nil {
		t.Fatalf("FetchThread: %v", err)
	}

	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}

	if msgs[0].Text != "parent" || msgs[1].Text != "reply" {
		t.Errorf("unexpected order: %q, %q", msgs[0].Text, msgs[1].Text)
	}

	if gotAuth != "Bearer xoxp-test-token" {
		t.Errorf("Authorization = %q, want bearer user token", gotAuth)
	}

	if gotPath != "/conversations.replies" {
		t.Errorf("path = %q, want /conversations.replies", gotPath)
	}

	for _, want := range []string{"channel=C123", "ts=1788872615.903009", "limit="} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q misses %q", gotQuery, want)
		}
	}

	if strings.Contains(gotQuery, "cursor=") {
		t.Errorf("first page must not send a cursor, got %q", gotQuery)
	}
}

func TestFetchThreadPaginates(t *testing.T) {
	t.Parallel()

	var cursors []string

	pages := []string{
		`{"ok": true, "has_more": true, "messages": [{"ts": "1.1", "user": "U1", "text": "a"}],
		  "response_metadata": {"next_cursor": "c1"}}`,
		`{"ok": true, "has_more": true, "messages": [{"ts": "2.2", "user": "U2", "text": "b"}],
		  "response_metadata": {"next_cursor": "c2"}}`,
		`{"ok": true, "has_more": false, "messages": [{"ts": "3.3", "user": "U3", "text": "c"}],
		  "response_metadata": {"next_cursor": ""}}`,
	}

	var page int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cursors = append(cursors, r.URL.Query().Get("cursor"))

		w.Write([]byte(pages[page]))
		page++
	}))
	defer srv.Close()

	client, _ := newClient(t, srv)

	msgs, err := client.FetchThread(context.Background(), "C123", "1.1")
	if err != nil {
		t.Fatalf("FetchThread: %v", err)
	}

	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3 across pages", len(msgs))
	}

	if msgs[0].Text != "a" || msgs[2].Text != "c" {
		t.Errorf("pages concatenated out of order: %+v", msgs)
	}

	want := []string{"", "c1", "c2"}
	if len(cursors) != len(want) {
		t.Fatalf("got %d requests, want %d", len(cursors), len(want))
	}

	for i := range want {
		if cursors[i] != want[i] {
			t.Errorf("request %d cursor = %q, want %q", i, cursors[i], want[i])
		}
	}
}

func TestFetchThreadEmpty(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"ok": true, "has_more": false, "messages": []}`))
	}))
	defer srv.Close()

	client, _ := newClient(t, srv)

	msgs, err := client.FetchThread(context.Background(), "C123", "1.1")
	if err != nil {
		t.Fatalf("FetchThread: %v", err)
	}

	if len(msgs) != 0 {
		t.Errorf("got %d messages, want none", len(msgs))
	}
}

func TestFetchThreadAPIErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		code string
		want error
	}{
		{name: "channel not found", code: "channel_not_found", want: slackapi.ErrChannelNotFound},
		{name: "not in channel", code: "not_in_channel", want: slackapi.ErrNotInChannel},
		{name: "thread not found", code: "thread_not_found", want: slackapi.ErrThreadNotFound},
		{name: "invalid auth", code: "invalid_auth", want: slackapi.ErrInvalidAuth},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(`{"ok": false, "error": "` + tc.code + `"}`))
			}))
			defer srv.Close()

			client, _ := newClient(t, srv)

			_, err := client.FetchThread(context.Background(), "C123", "1.1")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}

			var apiErr *slackapi.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v, want *APIError", err)
			}

			if apiErr.Method != "conversations.replies" {
				t.Errorf("Method = %q, want conversations.replies", apiErr.Method)
			}
		})
	}
}

func TestFetchThreadRetriesOn429(t *testing.T) {
	t.Parallel()

	var attempts int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++

		if attempts <= 2 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)

			return
		}

		w.Write([]byte(`{"ok": true, "messages": [{"ts": "1.1", "user": "U1", "text": "hi"}]}`))
	}))
	defer srv.Close()

	client, sleeper := newClient(t, srv)

	msgs, err := client.FetchThread(context.Background(), "C123", "1.1")
	if err != nil {
		t.Fatalf("FetchThread: %v", err)
	}

	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}

	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}

	want := []time.Duration{7 * time.Second, 7 * time.Second}
	if len(sleeper.delays) != len(want) {
		t.Fatalf("waited %v, want %v", sleeper.delays, want)
	}

	for i := range want {
		if sleeper.delays[i] != want[i] {
			t.Errorf("wait %d = %v, want %v", i, sleeper.delays[i], want[i])
		}
	}
}

func TestFetchThreadRetryAfterFallbackAndCap(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{name: "missing header", header: "", want: 30 * time.Second},
		{name: "garbage header", header: "soon", want: 30 * time.Second},
		{name: "zero header", header: "0", want: 30 * time.Second},
		{name: "absurd header", header: "100000", want: 5 * time.Minute},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var attempts int

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts++

				if attempts == 1 {
					if tc.header != "" {
						w.Header().Set("Retry-After", tc.header)
					}

					w.WriteHeader(http.StatusTooManyRequests)

					return
				}

				w.Write([]byte(`{"ok": true, "messages": []}`))
			}))
			defer srv.Close()

			client, sleeper := newClient(t, srv)

			if _, err := client.FetchThread(context.Background(), "C123", "1.1"); err != nil {
				t.Fatalf("FetchThread: %v", err)
			}

			if len(sleeper.delays) != 1 || sleeper.delays[0] != tc.want {
				t.Errorf("waits = %v, want [%v]", sleeper.delays, tc.want)
			}
		})
	}
}

func TestFetchThreadRateLimitExhausted(t *testing.T) {
	t.Parallel()

	var attempts int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++

		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	client, sleeper := newClient(t, srv, slackapi.WithMaxRetries(2))

	_, err := client.FetchThread(context.Background(), "C123", "1.1")
	if !errors.Is(err, slackapi.ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}

	var rl *slackapi.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("err = %v, want *RateLimitError", err)
	}

	if rl.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", rl.Attempts)
	}

	if rl.RetryAfter != 3*time.Second {
		t.Errorf("RetryAfter = %v, want 3s", rl.RetryAfter)
	}

	if rl.Method != "conversations.replies" {
		t.Errorf("Method = %q, want conversations.replies", rl.Method)
	}

	if attempts != 3 {
		t.Errorf("attempts = %d, want 3 (1 try + 2 retries)", attempts)
	}

	if len(sleeper.delays) != 2 {
		t.Errorf("waits = %v, want 2 of them", sleeper.delays)
	}
}

func TestFetchThreadServerError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client, _ := newClient(t, srv)

	_, err := client.FetchThread(context.Background(), "C123", "1.1")

	var httpErr *slackapi.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}

	if httpErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", httpErr.StatusCode)
	}
}

func TestFetchThreadCancelledContext(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"ok": true, "messages": []}`))
	}))
	defer srv.Close()

	client, _ := newClient(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := client.FetchThread(ctx, "C123", "1.1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestFetchThreadBrokenJSON(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"ok": true, "messages": [`))
	}))
	defer srv.Close()

	client, _ := newClient(t, srv)

	if _, err := client.FetchThread(context.Background(), "C123", "1.1"); err == nil {
		t.Fatal("want a decode error, got nil")
	}
}
