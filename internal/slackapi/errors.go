package slackapi

import (
	"errors"
	"fmt"
	"time"
)

// APIError is a Slack response with `"ok": false`. Code holds the raw value
// of the response's `error` field, which is what callers branch on.
type APIError struct {
	// Method is the Slack Web API method that failed ("conversations.replies").
	Method string
	// Code is the machine-readable Slack error ("channel_not_found").
	Code string
}

// Sentinels for the Slack error codes the thread flow has to react to. They
// are matched by code, so errors.Is works on any APIError coming back from a
// call, whatever method produced it.
var (
	// ErrChannelNotFound means the conversation does not exist or the token
	// cannot see it.
	ErrChannelNotFound = &APIError{Code: "channel_not_found"}
	// ErrNotInChannel means the token owner is not a member of the channel.
	ErrNotInChannel = &APIError{Code: "not_in_channel"}
	// ErrThreadNotFound means the parent message is gone.
	ErrThreadNotFound = &APIError{Code: "thread_not_found"}
	// ErrInvalidAuth means the token is wrong, revoked or expired.
	ErrInvalidAuth = &APIError{Code: "invalid_auth"}
)

func (e *APIError) Error() string {
	if e.Method == "" {
		return fmt.Sprintf("slackapi: %s", e.Code)
	}

	return fmt.Sprintf("slackapi: %s: %s", e.Method, e.Code)
}

// Is reports equality by error code only: the sentinels above carry no
// method, so a comparison against them must ignore it.
func (e *APIError) Is(target error) bool {
	var other *APIError
	if !errors.As(target, &other) {
		return false
	}

	return e.Code == other.Code
}

// ErrRateLimited is the sentinel behind every RateLimitError, so callers can
// recognise exhausted rate-limit retries without unwrapping.
var ErrRateLimited = errors.New("slackapi: rate limited")

// RateLimitError is returned when Slack kept answering 429 until the retry
// budget was spent.
type RateLimitError struct {
	// Method is the Slack Web API method that was throttled.
	Method string
	// Attempts is how many requests were sent before giving up.
	Attempts int
	// RetryAfter is the delay asked for by the last 429 response.
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf(
		"slackapi: %s: rate limited after %d attempts, retry after %s",
		e.Method, e.Attempts, e.RetryAfter,
	)
}

// Unwrap ties the error to ErrRateLimited.
func (e *RateLimitError) Unwrap() error { return ErrRateLimited }

// HTTPError is a non-429 unsuccessful HTTP status from Slack.
type HTTPError struct {
	// Method is the Slack Web API method that failed.
	Method string
	// StatusCode is the HTTP status Slack replied with.
	StatusCode int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("slackapi: %s: unexpected HTTP status %d", e.Method, e.StatusCode)
}
