package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

// Message is a single Slack message belonging to a tracked thread.
type Message struct {
	// ID is the local autoincrement identifier.
	ID int64
	// ThreadID is the owning thread.
	ThreadID int64
	// TS is the Slack message timestamp, unique inside the thread.
	TS string
	// UserID is the Slack ID of the author (user or bot).
	UserID string
	// Text is the raw mrkdwn text as Slack returned it.
	Text string
	// RawJSON is the original message payload, kept for later re-rendering.
	RawJSON string
	// EditedTS is set when Slack reports the message as edited.
	EditedTS string
	// TextHash is the digest of Text; it decides whether a stored
	// translation is still valid. UpsertMessages fills it in.
	TextHash string
	// Deleted marks a message that is no longer in the Slack thread. Such
	// a message is kept with its translation and only hidden away in the
	// UI; MarkMessagesDeleted sets the flag.
	Deleted bool
}

// UpsertStats reports what a single UpsertMessages call changed.
type UpsertStats struct {
	// Inserted counts messages seen for the first time.
	Inserted int
	// Updated counts messages whose text changed, so their translation was
	// dropped.
	Updated int
	// Unchanged counts messages left exactly as they were stored.
	Unchanged int
}

// TextHash returns the digest stored in messages.text_hash. It is a plain
// SHA-256 of the message text: the refresh flow compares it to decide whether
// a message has to be translated again.
func TextHash(text string) string {
	sum := sha256.Sum256([]byte(text))

	return hex.EncodeToString(sum[:])
}

// messageColumns is the column list shared by every message SELECT.
const messageColumns = `id, thread_id, ts, user_id, text, raw_json, edited_ts, text_hash, deleted`

// messageColumnsM is the same list qualified for queries that join another
// table.
const messageColumnsM = `m.id, m.thread_id, m.ts, m.user_id, m.text, m.raw_json, m.edited_ts, m.text_hash, m.deleted`

// UpsertMessages stores the freshly fetched messages of a thread. New
// messages are inserted, messages whose text is unchanged are left untouched
// (so their translation survives), and messages whose text changed are
// updated and their translation is deleted. Messages that disappeared from
// Slack are kept: a thread is never truncated locally.
func (s *Store) UpsertMessages(ctx context.Context, threadID int64, msgs []Message) (UpsertStats, error) {
	var stats UpsertStats

	if len(msgs) == 0 {
		return stats, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UpsertStats{}, fmt.Errorf("store: upsert messages: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op

	for _, m := range msgs {
		if m.TS == "" {
			return UpsertStats{}, errors.New("store: message needs a timestamp")
		}

		m.ThreadID = threadID
		m.TextHash = TextHash(m.Text)

		var (
			id         int64
			oldHash    string
			oldEdit    string
			oldRaw     string
			oldUser    string
			oldDeleted int
			existing   = tx.QueryRowContext(ctx,
				`SELECT id, text_hash, edited_ts, raw_json, user_id, deleted
				 FROM messages WHERE thread_id = ? AND ts = ?`,
				threadID, m.TS)
		)

		switch err := existing.Scan(&id, &oldHash, &oldEdit, &oldRaw, &oldUser, &oldDeleted); {
		case err == nil:
			if oldHash == m.TextHash && oldEdit == m.EditedTS && oldRaw == m.RawJSON &&
				oldUser == m.UserID && oldDeleted == 0 {
				stats.Unchanged++

				continue
			}

			// A message Slack shows again is no longer deleted.
			if _, err := tx.ExecContext(ctx,
				`UPDATE messages
				 SET user_id = ?, text = ?, raw_json = ?, edited_ts = ?, text_hash = ?, deleted = 0
				 WHERE id = ?`,
				m.UserID, m.Text, m.RawJSON, m.EditedTS, m.TextHash, id); err != nil {
				return UpsertStats{}, fmt.Errorf("store: update message %s: %w", m.TS, err)
			}

			// Only a changed text invalidates the translation; a new
			// raw payload or author rename does not.
			if oldHash != m.TextHash {
				if _, err := tx.ExecContext(ctx, `DELETE FROM translations WHERE message_id = ?`, id); err != nil {
					return UpsertStats{}, fmt.Errorf("store: invalidate translation %s: %w", m.TS, err)
				}
			}

			stats.Updated++
		case errors.Is(err, sql.ErrNoRows):
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO messages (thread_id, ts, user_id, text, raw_json, edited_ts, text_hash)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
				threadID, m.TS, m.UserID, m.Text, m.RawJSON, m.EditedTS, m.TextHash); err != nil {
				return UpsertStats{}, fmt.Errorf("store: insert message %s: %w", m.TS, err)
			}

			stats.Inserted++
		default:
			return UpsertStats{}, fmt.Errorf("store: upsert message %s: %w", m.TS, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return UpsertStats{}, fmt.Errorf("store: upsert messages: commit: %w", err)
	}

	return stats, nil
}

// ListMessages returns every stored message of the thread, oldest first.
func (s *Store) ListMessages(ctx context.Context, threadID int64) ([]Message, error) {
	return s.queryMessages(ctx,
		`SELECT `+messageColumns+` FROM messages WHERE thread_id = ? ORDER BY ts ASC, id ASC`, threadID)
}

// UntranslatedMessages returns the thread messages that have no up-to-date
// translation: either never translated, or translated before the text
// changed (UpsertMessages drops the stale row).
func (s *Store) UntranslatedMessages(ctx context.Context, threadID int64) ([]Message, error) {
	return s.queryMessages(ctx,
		`SELECT `+messageColumnsM+`
		 FROM messages m
		 LEFT JOIN translations t ON t.message_id = m.id
		 WHERE m.thread_id = ? AND t.message_id IS NULL AND m.deleted = 0
		 ORDER BY m.ts ASC, m.id ASC`, threadID)
}

// MarkMessagesDeleted flags every stored message of the thread whose
// timestamp is not in presentTS: those are the messages Slack no longer
// returns. Nothing is removed — text and translation stay readable — and the
// number of newly flagged messages is returned. An empty presentTS is
// ignored: a thread that came back empty is an error upstream, not a reason
// to bury the whole local history.
func (s *Store) MarkMessagesDeleted(ctx context.Context, threadID int64, presentTS []string) (int, error) {
	if len(presentTS) == 0 {
		return 0, nil
	}

	args := make([]any, 0, len(presentTS)+1)
	args = append(args, threadID)

	for _, ts := range presentTS {
		args = append(args, ts)
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE messages SET deleted = 1
		 WHERE thread_id = ? AND deleted = 0 AND ts NOT IN (?`+repeatPlaceholders(len(presentTS)-1)+`)`, args...)
	if err != nil {
		return 0, fmt.Errorf("store: mark deleted messages of thread %d: %w", threadID, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: mark deleted messages of thread %d: %w", threadID, err)
	}

	return int(n), nil
}

// GetMessage returns one message by local ID, or ErrNotFound.
func (s *Store) GetMessage(ctx context.Context, id int64) (Message, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE id = ?`, id)

	m, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, fmt.Errorf("%w: message %d", ErrNotFound, id)
	}

	if err != nil {
		return Message{}, fmt.Errorf("store: get message: %w", err)
	}

	return m, nil
}

// queryMessages runs a message SELECT and scans every row.
func (s *Store) queryMessages(ctx context.Context, query string, args ...any) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query messages: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only query

	var out []Message

	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("store: query messages: %w", err)
		}

		out = append(out, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: query messages: %w", err)
	}

	return out, nil
}

// scanMessage reads one messages row.
func scanMessage(sc rowScanner) (Message, error) {
	var (
		m       Message
		deleted int
	)

	err := sc.Scan(&m.ID, &m.ThreadID, &m.TS, &m.UserID, &m.Text, &m.RawJSON, &m.EditedTS, &m.TextHash, &deleted)
	if err != nil {
		return Message{}, err
	}

	m.Deleted = deleted != 0

	return m, nil
}
