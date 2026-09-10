package slackapi

import (
	"context"
	"net/url"
)

// AuthInfo identifies the owner of a token, as reported by auth.test. It is
// shown in the UI so the user can tell which account the app posts as.
type AuthInfo struct {
	// UserID is the Slack ID of the token owner ("U123").
	UserID string
	// User is the owner's handle.
	User string
	// TeamID is the workspace ID ("T123").
	TeamID string
	// Team is the workspace name.
	Team string
	// URL is the workspace root ("https://acme.slack.com/").
	URL string
	// BotID is set when the token belongs to a bot; a user token
	// (`xoxp-…`) leaves it empty.
	BotID string
}

// ErrMissingScope means the token is valid but was not granted one of the
// scopes the app needs.
var ErrMissingScope = &APIError{Code: "missing_scope"}

// AuthTest verifies the token against Slack and returns who it belongs to.
func (c *HTTPClient) AuthTest(ctx context.Context) (AuthInfo, error) {
	var resp struct {
		UserID string `json:"user_id"`
		User   string `json:"user"`
		TeamID string `json:"team_id"`
		Team   string `json:"team"`
		URL    string `json:"url"`
		BotID  string `json:"bot_id"`
	}

	if err := c.get(ctx, "auth.test", url.Values{}, &resp); err != nil {
		return AuthInfo{}, err
	}

	return AuthInfo{
		UserID: resp.UserID,
		User:   resp.User,
		TeamID: resp.TeamID,
		Team:   resp.Team,
		URL:    resp.URL,
		BotID:  resp.BotID,
	}, nil
}
