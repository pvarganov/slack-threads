package slackapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the Slack Web API root used unless a test overrides it.
const DefaultBaseURL = "https://slack.com/api"

const (
	// defaultMaxRetries is how many extra attempts a throttled call gets
	// before it fails with a RateLimitError.
	defaultMaxRetries = 5
	// defaultRetryAfter is used when Slack answers 429 without a usable
	// Retry-After header.
	defaultRetryAfter = 30 * time.Second
	// maxRetryAfter caps a hostile or mistaken Retry-After value.
	maxRetryAfter = 5 * time.Minute
	// defaultPageLimit is the page size asked for on paginated calls. Slack
	// may return fewer messages than requested.
	defaultPageLimit = 200
	// maxPages guards against a server that keeps handing out cursors.
	maxPages = 500
)

// Client is the Slack surface the rest of the app depends on. Everything
// above this interface is testable without a network.
type Client interface {
	// FetchThread returns every message of a thread, parent first.
	FetchThread(ctx context.Context, channelID, threadTS string) ([]Message, error)
	// ResolveUsers maps author IDs onto profiles, using the cache first.
	ResolveUsers(ctx context.Context, ids []string) (map[string]User, error)
	// PostMessage posts a reply into a thread and returns its coordinates
	// and permalink.
	PostMessage(ctx context.Context, channelID, threadTS, text string) (Posted, error)
}

// Doer is the subset of *http.Client the transport needs.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// HTTPClient talks to the Slack Web API with a user token (`xoxp-…`) passed
// as a bearer token.
type HTTPClient struct {
	token      string
	baseURL    string
	httpc      Doer
	maxRetries int
	pageLimit  int
	users      UserCache
	sleep      func(ctx context.Context, d time.Duration) error
}

// Option customises a client; the defaults are meant for production, the
// options mostly exist for tests.
type Option func(*HTTPClient)

// WithBaseURL points the client at another API root, e.g. an httptest server.
func WithBaseURL(u string) Option {
	return func(c *HTTPClient) { c.baseURL = strings.TrimRight(u, "/") }
}

// WithHTTPClient replaces the transport.
func WithHTTPClient(d Doer) Option {
	return func(c *HTTPClient) { c.httpc = d }
}

// WithMaxRetries sets how many extra attempts a 429 gets.
func WithMaxRetries(n int) Option {
	return func(c *HTTPClient) { c.maxRetries = n }
}

// WithPageLimit sets the page size requested from paginated methods.
func WithPageLimit(n int) Option {
	return func(c *HTTPClient) { c.pageLimit = n }
}

// WithSleep replaces the wait between retries, so tests do not spend real
// seconds honouring Retry-After.
func WithSleep(f func(ctx context.Context, d time.Duration) error) Option {
	return func(c *HTTPClient) { c.sleep = f }
}

// New builds a Slack client for the given user token.
func New(token string, opts ...Option) *HTTPClient {
	c := &HTTPClient{
		token:      token,
		baseURL:    DefaultBaseURL,
		httpc:      &http.Client{Timeout: 30 * time.Second},
		maxRetries: defaultMaxRetries,
		pageLimit:  defaultPageLimit,
		users:      nopCache{},
		sleep:      sleepCtx,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// sleepCtx waits for d unless the context ends first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// apiResponse is the envelope shared by every Slack Web API reply.
type apiResponse struct {
	OK               bool   `json:"ok"`
	Error            string `json:"error"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

// get performs a GET against a Slack method, retrying while Slack answers
// 429, and unmarshals the body into out. out must embed apiResponse-shaped
// fields; the envelope is checked separately.
func (c *HTTPClient) get(ctx context.Context, method string, params url.Values, out any) error {
	endpoint := c.baseURL + "/" + method
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}

	return c.call(ctx, method, out, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	})
}

// post performs a form-encoded POST against a Slack method, with the same
// retry and envelope handling as get.
func (c *HTTPClient) post(ctx context.Context, method string, form url.Values, out any) error {
	endpoint := c.baseURL + "/" + method

	return c.call(ctx, method, out, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, err
		}

		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")

		return req, nil
	})
}

// call sends the request newReq builds, repeating it while Slack answers
// 429, and decodes the reply into out. newReq is called once per attempt:
// a retried POST needs its own body reader.
func (c *HTTPClient) call(ctx context.Context, method string, out any, newReq func() (*http.Request, error)) error {
	var lastRetryAfter time.Duration

	for attempt := 1; attempt <= c.maxRetries+1; attempt++ {
		body, retryAfter, err := c.doOnce(method, newReq)

		switch {
		case err != nil:
			return err
		case retryAfter > 0:
			lastRetryAfter = retryAfter

			if attempt == c.maxRetries+1 {
				return &RateLimitError{Method: method, Attempts: attempt, RetryAfter: retryAfter}
			}

			if err := c.sleep(ctx, retryAfter); err != nil {
				return fmt.Errorf("slackapi: %s: %w", method, err)
			}

			continue
		}

		var envelope apiResponse
		if err := json.Unmarshal(body, &envelope); err != nil {
			return fmt.Errorf("slackapi: %s: decode response: %w", method, err)
		}

		if !envelope.OK {
			return &APIError{Method: method, Code: envelope.Error}
		}

		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("slackapi: %s: decode response: %w", method, err)
		}

		return nil
	}

	return &RateLimitError{Method: method, Attempts: c.maxRetries + 1, RetryAfter: lastRetryAfter}
}

// doOnce sends a single request. A non-zero retryAfter means Slack answered
// 429 and the call should be repeated after that delay.
func (c *HTTPClient) doOnce(method string, newReq func() (*http.Request, error)) ([]byte, time.Duration, error) {
	req, err := newReq()
	if err != nil {
		return nil, 0, fmt.Errorf("slackapi: build request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("slackapi: request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // nothing to do about a failing close on a read body

	if resp.StatusCode == http.StatusTooManyRequests {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck // draining a throttled body is best effort

		return nil, parseRetryAfter(resp.Header.Get("Retry-After")), nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("slackapi: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, 0, &HTTPError{Method: method, StatusCode: resp.StatusCode}
	}

	return body, 0, nil
}

// parseRetryAfter turns the header into a delay, falling back to a sane
// default when it is missing or unparsable and capping absurd values.
func parseRetryAfter(header string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds <= 0 {
		return defaultRetryAfter
	}

	d := time.Duration(seconds) * time.Second
	if d > maxRetryAfter {
		return maxRetryAfter
	}

	return d
}
