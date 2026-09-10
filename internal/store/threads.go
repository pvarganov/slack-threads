package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Thread is a Slack thread tracked by the app.
type Thread struct {
	// ID is the local autoincrement identifier.
	ID int64
	// ChannelID is the Slack conversation ID the thread lives in.
	ChannelID string
	// ThreadTS is the timestamp of the thread root message.
	ThreadTS string
	// Workspace is the slack.com subdomain the thread was added from.
	Workspace string
	// Title is a short human-readable label shown in the thread list.
	Title string
	// AddedAt is when the thread was added locally.
	AddedAt time.Time
	// LastFetchedAt is when the thread was last read from Slack; zero until
	// the first successful fetch.
	LastFetchedAt time.Time
	// Archived marks a thread that is kept but no longer refreshed.
	Archived bool
	// ClaudeSessionID is the translator session bound to this thread, used to
	// resume it after an app restart.
	ClaudeSessionID string
	// NeedsRefresh marks a thread the app knows to be out of date locally,
	// e.g. right after a reply was posted to it. The next successful sync
	// clears the flag.
	NeedsRefresh bool
}

// threadColumns is the column list shared by every thread SELECT.
const threadColumns = `id, channel_id, thread_ts, workspace, title, added_at, last_fetched_at, archived, claude_session_id, needs_refresh`

// AddThread stores a new thread and returns it with ID and AddedAt filled in.
// It returns ErrThreadExists if the (ChannelID, ThreadTS) pair is already
// stored.
func (s *Store) AddThread(ctx context.Context, t Thread) (Thread, error) {
	if t.ChannelID == "" || t.ThreadTS == "" {
		return Thread{}, errors.New("store: thread needs both channel ID and thread timestamp")
	}

	if t.AddedAt.IsZero() {
		t.AddedAt = s.now()
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO threads (channel_id, thread_ts, workspace, title, added_at, last_fetched_at, archived, claude_session_id, needs_refresh)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ChannelID, t.ThreadTS, t.Workspace, t.Title,
		toUnix(t.AddedAt), nullableUnix(t.LastFetchedAt), boolToInt(t.Archived), t.ClaudeSessionID,
		boolToInt(t.NeedsRefresh))
	if err != nil {
		if isUniqueViolation(err) {
			return Thread{}, fmt.Errorf("%w: %s/%s", ErrThreadExists, t.ChannelID, t.ThreadTS)
		}

		return Thread{}, fmt.Errorf("store: add thread: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Thread{}, fmt.Errorf("store: add thread: %w", err)
	}

	t.ID = id

	return t, nil
}

// GetThread returns the thread with the given ID, or ErrNotFound.
func (s *Store) GetThread(ctx context.Context, id int64) (Thread, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+threadColumns+` FROM threads WHERE id = ?`, id)

	t, err := scanThread(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Thread{}, fmt.Errorf("%w: thread %d", ErrNotFound, id)
	}

	if err != nil {
		return Thread{}, fmt.Errorf("store: get thread: %w", err)
	}

	return t, nil
}

// GetThreadByKey returns the thread identified by its Slack coordinates, or
// ErrNotFound.
func (s *Store) GetThreadByKey(ctx context.Context, channelID, threadTS string) (Thread, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+threadColumns+` FROM threads WHERE channel_id = ? AND thread_ts = ?`, channelID, threadTS)

	t, err := scanThread(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Thread{}, fmt.Errorf("%w: thread %s/%s", ErrNotFound, channelID, threadTS)
	}

	if err != nil {
		return Thread{}, fmt.Errorf("store: get thread: %w", err)
	}

	return t, nil
}

// ListThreads returns stored threads, newest first. Archived threads are
// included only when includeArchived is true.
func (s *Store) ListThreads(ctx context.Context, includeArchived bool) ([]Thread, error) {
	query := `SELECT ` + threadColumns + ` FROM threads`
	if !includeArchived {
		query += ` WHERE archived = 0`
	}

	query += ` ORDER BY added_at DESC, id DESC`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("store: list threads: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only query

	var out []Thread

	for rows.Next() {
		t, err := scanThread(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list threads: %w", err)
		}

		out = append(out, t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list threads: %w", err)
	}

	return out, nil
}

// SetThreadArchived archives or unarchives a thread.
func (s *Store) SetThreadArchived(ctx context.Context, id int64, archived bool) error {
	return s.updateThread(ctx, id, `UPDATE threads SET archived = ? WHERE id = ?`, boolToInt(archived), id)
}

// SetThreadTitle updates the thread title.
func (s *Store) SetThreadTitle(ctx context.Context, id int64, title string) error {
	return s.updateThread(ctx, id, `UPDATE threads SET title = ? WHERE id = ?`, title, id)
}

// SetThreadFetched records the moment the thread was last read from Slack.
func (s *Store) SetThreadFetched(ctx context.Context, id int64, at time.Time) error {
	if at.IsZero() {
		at = s.now()
	}

	return s.updateThread(ctx, id, `UPDATE threads SET last_fetched_at = ? WHERE id = ?`, toUnix(at), id)
}

// SetThreadNeedsRefresh flags (or unflags) a thread as locally out of date.
func (s *Store) SetThreadNeedsRefresh(ctx context.Context, id int64, needs bool) error {
	return s.updateThread(ctx, id, `UPDATE threads SET needs_refresh = ? WHERE id = ?`, boolToInt(needs), id)
}

// SetThreadSession stores the claude session ID bound to the thread.
func (s *Store) SetThreadSession(ctx context.Context, id int64, sessionID string) error {
	return s.updateThread(ctx, id, `UPDATE threads SET claude_session_id = ? WHERE id = ?`, sessionID, id)
}

// DeleteThread removes a thread together with its messages, translations,
// summary and draft (the foreign keys cascade).
func (s *Store) DeleteThread(ctx context.Context, id int64) error {
	return s.updateThread(ctx, id, `DELETE FROM threads WHERE id = ?`, id)
}

// updateThread runs a single-row statement and maps "no rows touched" to
// ErrNotFound.
func (s *Store) updateThread(ctx context.Context, id int64, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: update thread %d: %w", id, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update thread %d: %w", id, err)
	}

	if n == 0 {
		return fmt.Errorf("%w: thread %d", ErrNotFound, id)
	}

	return nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanThread reads one threads row.
func scanThread(sc rowScanner) (Thread, error) {
	var (
		t            Thread
		addedAt      int64
		lastFetched  sql.NullInt64
		archived     int
		needsRefresh int
	)

	err := sc.Scan(&t.ID, &t.ChannelID, &t.ThreadTS, &t.Workspace, &t.Title,
		&addedAt, &lastFetched, &archived, &t.ClaudeSessionID, &needsRefresh)
	if err != nil {
		return Thread{}, err
	}

	t.AddedAt = fromUnix(addedAt)
	t.Archived = archived != 0
	t.NeedsRefresh = needsRefresh != 0

	if lastFetched.Valid {
		t.LastFetchedAt = fromUnix(lastFetched.Int64)
	}

	return t, nil
}

// toUnix stores a time as milliseconds since the epoch.
func toUnix(t time.Time) int64 {
	return t.UnixMilli()
}

// fromUnix restores a stored timestamp in UTC.
func fromUnix(ms int64) time.Time {
	return time.UnixMilli(ms).UTC()
}

// nullableUnix maps the zero time to SQL NULL.
func nullableUnix(t time.Time) any {
	if t.IsZero() {
		return nil
	}

	return toUnix(t)
}

// boolToInt maps a bool to SQLite's 0/1.
func boolToInt(b bool) int {
	if b {
		return 1
	}

	return 0
}

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint failure.
// The pure-Go driver does not export a typed error for it, so the message is
// the only available signal.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
