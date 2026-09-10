package slackapi

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// Posted is what Slack accepted: the coordinates of the new message plus a
// link to it. Permalink is empty when Slack accepted the message but the
// follow-up chat.getPermalink call failed — the reply is sent either way,
// so that is not treated as an error.
type Posted struct {
	// Channel is the conversation the message landed in.
	Channel string
	// TS is the timestamp of the new message.
	TS string
	// ThreadTS is the thread it was posted into.
	ThreadTS string
	// Permalink is the link to the message in Slack.
	Permalink string
}

// Sentinels for the chat.postMessage failures the reply flow has to explain
// to the user. Slack keeps adding codes, so the exact set is empirical (see
// docs/); anything unknown surfaces as a plain *APIError.
var (
	// ErrMsgTooLong means the reply exceeds Slack's per-message limit.
	ErrMsgTooLong = &APIError{Code: "msg_too_long"}
	// ErrCannotReply means the parent message does not accept replies any
	// more, e.g. it was deleted while the reply was being written.
	ErrCannotReply = &APIError{Code: "cannot_reply_to_message"}
	// ErrIsArchived means the conversation is archived and read-only.
	ErrIsArchived = &APIError{Code: "is_archived"}
)

// ErrPostRestricted is the sentinel behind the "you are not allowed to post
// here" family of Slack codes: workspace restrictions (restricted_action…)
// and the Slack Connect rules of an external channel (slack_connect…),
// which is the common case for threads shared with another company.
var ErrPostRestricted = errors.New("slackapi: posting is not allowed in this conversation")

// restrictedPrefixes are the Slack error code prefixes that mean the post
// was refused by a policy rather than by a bad request.
var restrictedPrefixes = []string{"restricted_action", "slack_connect"}

// PostMessage posts text as a reply in a thread and returns the coordinates
// and the permalink of the new message.
func (c *HTTPClient) PostMessage(ctx context.Context, channelID, threadTS, text string) (Posted, error) {
	if strings.TrimSpace(text) == "" {
		return Posted{}, errors.New("slackapi: chat.postMessage: empty text")
	}

	form := url.Values{}
	form.Set("channel", channelID)
	form.Set("thread_ts", threadTS)
	form.Set("text", text)
	// The user token posts as the user; Slack must not re-interpret the
	// already rendered mrkdwn beyond the usual formatting.
	form.Set("unfurl_links", "false")
	form.Set("unfurl_media", "false")

	var resp struct {
		Channel string `json:"channel"`
		TS      string `json:"ts"`
		Message struct {
			ThreadTS string `json:"thread_ts"`
		} `json:"message"`
	}

	if err := c.post(ctx, "chat.postMessage", form, &resp); err != nil {
		return Posted{}, restrictedError(err)
	}

	posted := Posted{Channel: resp.Channel, TS: resp.TS, ThreadTS: resp.Message.ThreadTS}
	if posted.Channel == "" {
		posted.Channel = channelID
	}

	if posted.ThreadTS == "" {
		posted.ThreadTS = threadTS
	}

	// The message is already in Slack; a missing link is a cosmetic loss.
	if link, err := c.Permalink(ctx, posted.Channel, posted.TS); err == nil {
		posted.Permalink = link
	}

	return posted, nil
}

// Permalink returns the Slack link to a single message.
func (c *HTTPClient) Permalink(ctx context.Context, channelID, ts string) (string, error) {
	params := url.Values{}
	params.Set("channel", channelID)
	params.Set("message_ts", ts)

	var resp struct {
		Permalink string `json:"permalink"`
	}

	if err := c.get(ctx, "chat.getPermalink", params, &resp); err != nil {
		return "", err
	}

	return resp.Permalink, nil
}

// restrictedError ties the policy-refusal codes to ErrPostRestricted, so
// callers can tell "you may not post here" from "the request was wrong"
// without listing every code Slack has.
func restrictedError(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}

	for _, prefix := range restrictedPrefixes {
		if strings.HasPrefix(apiErr.Code, prefix) {
			return &RestrictedError{APIError: apiErr}
		}
	}

	return err
}

// RestrictedError is a post refused by a workspace or Slack Connect policy.
type RestrictedError struct {
	// APIError is the underlying Slack error with its raw code.
	*APIError
}

// Unwrap ties the error to ErrPostRestricted while keeping errors.As on
// *APIError working through the embedded value.
func (e *RestrictedError) Unwrap() []error { return []error{ErrPostRestricted, e.APIError} }
