package store

import (
	"context"

	"github.com/pavelvarganov/slack-threads/internal/slackapi"
)

// UserCache adapts the store to slackapi.UserCache: it translates between
// the cached row and the Slack profile so the Slack client stays free of
// storage concerns.
type UserCache struct {
	store *Store
}

// NewUserCache wraps a store as the Slack client's profile cache.
func NewUserCache(s *Store) *UserCache { return &UserCache{store: s} }

// GetUsers returns the cached profiles for the given IDs; unknown IDs are
// absent from the map.
func (c *UserCache) GetUsers(ctx context.Context, ids []string) (map[string]slackapi.User, error) {
	rows, err := c.store.GetUsers(ctx, ids)
	if err != nil {
		return nil, err
	}

	out := make(map[string]slackapi.User, len(rows))
	for id, u := range rows {
		out[id] = slackapi.User{
			ID:          u.ID,
			DisplayName: u.DisplayName,
			RealName:    u.RealName,
			IsBot:       u.IsBot,
		}
	}

	return out, nil
}

// SaveUsers writes freshly fetched profiles into the cache.
func (c *UserCache) SaveUsers(ctx context.Context, users []slackapi.User) error {
	rows := make([]User, 0, len(users))
	for _, u := range users {
		rows = append(rows, User{
			ID:          u.ID,
			DisplayName: u.DisplayName,
			RealName:    u.RealName,
			IsBot:       u.IsBot,
		})
	}

	return c.store.SaveUsers(ctx, rows)
}
