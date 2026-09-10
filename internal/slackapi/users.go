package slackapi

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// User is a resolved Slack identity: a human account or a bot/app.
type User struct {
	// ID is the Slack user ID ("U123") or bot ID ("B123").
	ID string
	// DisplayName is the handle Slack shows in the client.
	DisplayName string
	// RealName is the full name from the profile; a bot's name for bots.
	RealName string
	// IsBot marks bot and app accounts.
	IsBot bool
}

// Name is the label to show for the author: the display name when Slack has
// one, the real name otherwise, and the bare ID as a last resort.
func (u User) Name() string {
	switch {
	case u.DisplayName != "":
		return u.DisplayName
	case u.RealName != "":
		return u.RealName
	default:
		return u.ID
	}
}

// UserCache is the persistent side of user resolution. The store implements
// it; tests substitute an in-memory fake.
type UserCache interface {
	// GetUsers returns the cached profiles for the given IDs. IDs that are
	// not cached are simply absent from the map.
	GetUsers(ctx context.Context, ids []string) (map[string]User, error)
	// SaveUsers writes freshly fetched profiles into the cache.
	SaveUsers(ctx context.Context, users []User) error
}

// nopCache is used when a client was built without a cache: every lookup
// misses and nothing is stored.
type nopCache struct{}

func (nopCache) GetUsers(context.Context, []string) (map[string]User, error) {
	return map[string]User{}, nil
}

func (nopCache) SaveUsers(context.Context, []User) error { return nil }

// WithUserCache attaches the profile cache consulted before users.info.
func WithUserCache(c UserCache) Option {
	return func(cl *HTTPClient) {
		if c != nil {
			cl.users = c
		}
	}
}

// errUserNotFound / errBotNotFound are the Slack codes that mean "this ID is
// gone": deleted accounts and uninstalled apps. They must not fail a thread
// load, so ResolveUsers skips them.
var (
	errUserNotFound = &APIError{Code: "user_not_found"}
	errBotNotFound  = &APIError{Code: "bot_not_found"}
)

// userInfoResponse is the users.info payload.
type userInfoResponse struct {
	User struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		RealName string `json:"real_name"`
		IsBot    bool   `json:"is_bot"`
		Deleted  bool   `json:"deleted"`
		Profile  struct {
			DisplayName string `json:"display_name"`
			RealName    string `json:"real_name"`
		} `json:"profile"`
	} `json:"user"`
}

// botInfoResponse is the bots.info payload.
type botInfoResponse struct {
	Bot struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		AppID   string `json:"app_id"`
		Deleted bool   `json:"deleted"`
	} `json:"bot"`
}

// ResolveUsers turns author IDs into profiles, hitting users.info (or
// bots.info for bot IDs) only for the IDs the cache does not know yet.
// Unresolvable IDs are left out of the result instead of failing the call:
// a deleted account must not block reading a thread.
func (c *HTTPClient) ResolveUsers(ctx context.Context, ids []string) (map[string]User, error) {
	wanted := dedupe(ids)
	if len(wanted) == 0 {
		return map[string]User{}, nil
	}

	out, err := c.users.GetUsers(ctx, wanted)
	if err != nil {
		return nil, err
	}

	if out == nil {
		out = make(map[string]User, len(wanted))
	}

	var fetched []User

	for _, id := range wanted {
		if _, ok := out[id]; ok {
			continue
		}

		u, err := c.fetchUser(ctx, id)

		switch {
		case errors.Is(err, errUserNotFound), errors.Is(err, errBotNotFound):
			continue
		case err != nil:
			return nil, err
		}

		out[id] = u
		fetched = append(fetched, u)
	}

	if len(fetched) > 0 {
		if err := c.users.SaveUsers(ctx, fetched); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// fetchUser reads one profile from Slack, picking the method that matches
// the ID kind.
func (c *HTTPClient) fetchUser(ctx context.Context, id string) (User, error) {
	if isBotID(id) {
		return c.fetchBot(ctx, id)
	}

	params := url.Values{}
	params.Set("user", id)

	var resp userInfoResponse
	if err := c.get(ctx, "users.info", params, &resp); err != nil {
		return User{}, err
	}

	u := User{
		ID:          firstNonEmpty(resp.User.ID, id),
		DisplayName: firstNonEmpty(resp.User.Profile.DisplayName, resp.User.Name),
		RealName:    firstNonEmpty(resp.User.Profile.RealName, resp.User.RealName),
		IsBot:       resp.User.IsBot,
	}

	return u, nil
}

// fetchBot reads a bot profile through bots.info.
func (c *HTTPClient) fetchBot(ctx context.Context, id string) (User, error) {
	params := url.Values{}
	params.Set("bot", id)

	var resp botInfoResponse
	if err := c.get(ctx, "bots.info", params, &resp); err != nil {
		return User{}, err
	}

	return User{
		ID:          firstNonEmpty(resp.Bot.ID, id),
		DisplayName: resp.Bot.Name,
		RealName:    resp.Bot.Name,
		IsBot:       true,
	}, nil
}

// isBotID reports whether the ID names a bot ("B…") rather than a user.
func isBotID(id string) bool {
	return strings.HasPrefix(id, "B")
}

// dedupe drops empties and repeats, keeping the original order.
func dedupe(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))

	for _, id := range ids {
		if id == "" {
			continue
		}

		if _, ok := seen[id]; ok {
			continue
		}

		seen[id] = struct{}{}

		out = append(out, id)
	}

	return out
}

// firstNonEmpty returns the first non-empty argument.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}

	return ""
}
