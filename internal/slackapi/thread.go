package slackapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
)

// Reaction is one emoji reaction attached to a message.
type Reaction struct {
	// Name is the emoji name without colons ("eyes").
	Name string
	// Count is how many people reacted.
	Count int
	// Users are the Slack IDs of the reacting users, as far as Slack
	// disclosed them.
	Users []string
}

// Message is a Slack thread message in the shape the rest of the app works
// with. Raw keeps the untouched payload so a later version can re-render a
// message without re-fetching the thread.
type Message struct {
	// TS is the message timestamp, unique inside a conversation.
	TS string
	// ThreadTS is the timestamp of the thread parent.
	ThreadTS string
	// User is the Slack user ID of the author; empty for bot messages.
	User string
	// BotID is set instead of User when a bot or app posted the message.
	BotID string
	// Username is the display name a bot posted under, when it set one.
	Username string
	// BotName is the name of the app the message came from (bot_profile).
	// Slack labels such a message with it, even when the author also has
	// a user profile, so it beats the cached profile name.
	BotName string
	// Text is the raw mrkdwn body.
	Text string
	// Subtype distinguishes joins, channel topic changes, file shares and
	// similar non-plain messages. Empty for a normal message.
	Subtype string
	// EditedTS is the timestamp of the last edit, empty if never edited.
	EditedTS string
	// Reactions are the emoji reactions on the message.
	Reactions []Reaction
	// Raw is the original JSON object Slack returned.
	Raw json.RawMessage
}

// Label is the name Slack itself puts on the message, when the payload
// carries one: the username a bot posted under, otherwise the app name
// from bot_profile. Empty for an ordinary message, whose author is named
// by their profile.
func (m Message) Label() string {
	if m.Username != "" {
		return m.Username
	}

	return m.BotName
}

// Author returns the identifier to attribute the message to: the user ID for
// humans, the bot ID for apps.
func (m Message) Author() string {
	if m.User != "" {
		return m.User
	}

	return m.BotID
}

// rawMessage mirrors the parts of the Slack message payload the app reads.
type rawMessage struct {
	TS         string `json:"ts"`
	ThreadTS   string `json:"thread_ts"`
	User       string `json:"user"`
	BotID      string `json:"bot_id"`
	Username   string `json:"username"`
	Text       string `json:"text"`
	BotProfile *struct {
		Name string `json:"name"`
	} `json:"bot_profile"`
	Subtype string `json:"subtype"`
	Edited  *struct {
		TS   string `json:"ts"`
		User string `json:"user"`
	} `json:"edited"`
	Reactions []struct {
		Name  string   `json:"name"`
		Count int      `json:"count"`
		Users []string `json:"users"`
	} `json:"reactions"`
}

// repliesResponse is the conversations.replies payload.
type repliesResponse struct {
	Messages         []json.RawMessage `json:"messages"`
	HasMore          bool              `json:"has_more"`
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

// ErrTooManyPages is returned when a thread keeps producing cursors past the
// page guard; it means something is wrong with the server, not the thread.
var ErrTooManyPages = errors.New("slackapi: conversations.replies: too many pages")

// FetchThread reads a whole thread through conversations.replies, following
// `response_metadata.next_cursor` until Slack stops handing out cursors. The
// messages come back in Slack's order: parent first, then replies.
func (c *HTTPClient) FetchThread(ctx context.Context, channelID, threadTS string) ([]Message, error) {
	var (
		out    []Message
		cursor string
	)

	for page := 0; page < maxPages; page++ {
		params := url.Values{}
		params.Set("channel", channelID)
		params.Set("ts", threadTS)
		params.Set("limit", strconv.Itoa(c.pageLimit))

		if cursor != "" {
			params.Set("cursor", cursor)
		}

		var resp repliesResponse
		if err := c.get(ctx, "conversations.replies", params, &resp); err != nil {
			return nil, err
		}

		for _, raw := range resp.Messages {
			msg, err := decodeMessage(raw)
			if err != nil {
				return nil, err
			}

			out = append(out, msg)
		}

		cursor = resp.ResponseMetadata.NextCursor
		if cursor == "" {
			return out, nil
		}
	}

	return nil, ErrTooManyPages
}

// decodeMessage maps one raw Slack message onto Message.
func decodeMessage(raw json.RawMessage) (Message, error) {
	var r rawMessage
	if err := json.Unmarshal(raw, &r); err != nil {
		return Message{}, fmt.Errorf("slackapi: conversations.replies: decode message: %w", err)
	}

	msg := Message{
		TS:       r.TS,
		ThreadTS: r.ThreadTS,
		User:     r.User,
		BotID:    r.BotID,
		Username: r.Username,
		Text:     r.Text,
		Subtype:  r.Subtype,
		Raw:      raw,
	}

	if r.BotProfile != nil {
		msg.BotName = r.BotProfile.Name
	}

	if r.Edited != nil {
		msg.EditedTS = r.Edited.TS
	}

	for _, reaction := range r.Reactions {
		msg.Reactions = append(msg.Reactions, Reaction{
			Name:  reaction.Name,
			Count: reaction.Count,
			Users: reaction.Users,
		})
	}

	return msg, nil
}
