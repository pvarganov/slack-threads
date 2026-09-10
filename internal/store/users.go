package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// User is a cached Slack user or bot profile. The cache spares us a
// users.info call per message author on every refresh.
type User struct {
	// ID is the Slack user or bot ID.
	ID string
	// DisplayName is the handle Slack shows in the client.
	DisplayName string
	// RealName is the full name from the profile.
	RealName string
	// IsBot marks bot accounts.
	IsBot bool
	// UpdatedAt is when the entry was last refreshed from Slack.
	UpdatedAt time.Time
}

// SaveUsers writes user profiles into the cache, replacing existing entries.
func (s *Store) SaveUsers(ctx context.Context, users []User) error {
	if len(users) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: save users: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op

	for _, u := range users {
		if u.ID == "" {
			return errors.New("store: user needs an ID")
		}

		if u.UpdatedAt.IsZero() {
			u.UpdatedAt = s.now()
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO users (id, display_name, real_name, is_bot, updated_at)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT (id) DO UPDATE SET
			   display_name = excluded.display_name, real_name = excluded.real_name,
			   is_bot = excluded.is_bot, updated_at = excluded.updated_at`,
			u.ID, u.DisplayName, u.RealName, boolToInt(u.IsBot), toUnix(u.UpdatedAt)); err != nil {
			return fmt.Errorf("store: save user %s: %w", u.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: save users: commit: %w", err)
	}

	return nil
}

// GetUser returns one cached profile, or ErrNotFound.
func (s *Store) GetUser(ctx context.Context, id string) (User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, display_name, real_name, is_bot, updated_at FROM users WHERE id = ?`, id)

	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("%w: user %s", ErrNotFound, id)
	}

	if err != nil {
		return User{}, fmt.Errorf("store: get user: %w", err)
	}

	return u, nil
}

// GetUsers returns the cached profiles for the requested IDs, keyed by ID.
// Unknown IDs are simply absent from the result: the caller resolves them
// against Slack and caches them with SaveUsers.
func (s *Store) GetUsers(ctx context.Context, ids []string) (map[string]User, error) {
	out := make(map[string]User, len(ids))

	if len(ids) == 0 {
		return out, nil
	}

	query := `SELECT id, display_name, real_name, is_bot, updated_at FROM users WHERE id IN (?` +
		repeatPlaceholders(len(ids)-1) + `)`

	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: get users: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only query

	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("store: get users: %w", err)
		}

		out[u.ID] = u
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get users: %w", err)
	}

	return out, nil
}

// scanUser reads one users row.
func scanUser(sc rowScanner) (User, error) {
	var (
		u       User
		isBot   int
		updated int64
	)

	if err := sc.Scan(&u.ID, &u.DisplayName, &u.RealName, &isBot, &updated); err != nil {
		return User{}, err
	}

	u.IsBot = isBot != 0
	u.UpdatedAt = fromUnix(updated)

	return u, nil
}

// repeatPlaceholders returns n more ", ?" placeholders for an IN clause.
func repeatPlaceholders(n int) string {
	out := make([]byte, 0, n*3)
	for range n {
		out = append(out, ", ?"...)
	}

	return string(out)
}
