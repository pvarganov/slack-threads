package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Translation is the Russian rendering of one stored message.
type Translation struct {
	// MessageID is the local ID of the translated message.
	MessageID int64
	// TextRU is the translation itself.
	TextRU string
	// Model names the model that produced the translation.
	Model string
	// CreatedAt is when the translation was stored.
	CreatedAt time.Time
}

// Summary is the "Суть" block generated for a thread.
type Summary struct {
	// ThreadID is the thread the summary belongs to.
	ThreadID int64
	// TextRU is the summary text in Russian.
	TextRU string
	// BasedOnTS is the timestamp of the newest message the summary covers;
	// it tells the refresh flow whether the summary has to be rebuilt.
	BasedOnTS string
	// UpdatedAt is when the summary was last written.
	UpdatedAt time.Time
}

// SaveTranslations stores (or replaces) translations for messages. All rows
// are written in one transaction, so a failure leaves nothing half-saved.
func (s *Store) SaveTranslations(ctx context.Context, translations []Translation) error {
	if len(translations) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: save translations: begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op

	for _, tr := range translations {
		if tr.MessageID == 0 {
			return errors.New("store: translation needs a message ID")
		}

		if tr.CreatedAt.IsZero() {
			tr.CreatedAt = s.now()
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO translations (message_id, text_ru, model, created_at)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT (message_id) DO UPDATE SET
			   text_ru = excluded.text_ru, model = excluded.model, created_at = excluded.created_at`,
			tr.MessageID, tr.TextRU, tr.Model, toUnix(tr.CreatedAt)); err != nil {
			return fmt.Errorf("store: save translation for message %d: %w", tr.MessageID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: save translations: commit: %w", err)
	}

	return nil
}

// ListTranslations returns the translations of a thread keyed by message ID.
func (s *Store) ListTranslations(ctx context.Context, threadID int64) (map[int64]Translation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.message_id, t.text_ru, t.model, t.created_at
		 FROM translations t
		 JOIN messages m ON m.id = t.message_id
		 WHERE m.thread_id = ?`, threadID)
	if err != nil {
		return nil, fmt.Errorf("store: list translations: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only query

	out := make(map[int64]Translation)

	for rows.Next() {
		var (
			tr      Translation
			created int64
		)

		if err := rows.Scan(&tr.MessageID, &tr.TextRU, &tr.Model, &created); err != nil {
			return nil, fmt.Errorf("store: list translations: %w", err)
		}

		tr.CreatedAt = fromUnix(created)
		out[tr.MessageID] = tr
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list translations: %w", err)
	}

	return out, nil
}

// SaveSummary stores (or replaces) the summary of a thread.
func (s *Store) SaveSummary(ctx context.Context, sum Summary) error {
	if sum.ThreadID == 0 {
		return errors.New("store: summary needs a thread ID")
	}

	if sum.UpdatedAt.IsZero() {
		sum.UpdatedAt = s.now()
	}

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO summaries (thread_id, text_ru, based_on_ts, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (thread_id) DO UPDATE SET
		   text_ru = excluded.text_ru, based_on_ts = excluded.based_on_ts, updated_at = excluded.updated_at`,
		sum.ThreadID, sum.TextRU, sum.BasedOnTS, toUnix(sum.UpdatedAt)); err != nil {
		return fmt.Errorf("store: save summary for thread %d: %w", sum.ThreadID, err)
	}

	return nil
}

// GetSummary returns the summary of a thread, or ErrNotFound.
func (s *Store) GetSummary(ctx context.Context, threadID int64) (Summary, error) {
	var (
		sum     Summary
		updated int64
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT thread_id, text_ru, based_on_ts, updated_at FROM summaries WHERE thread_id = ?`, threadID).
		Scan(&sum.ThreadID, &sum.TextRU, &sum.BasedOnTS, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Summary{}, fmt.Errorf("%w: summary for thread %d", ErrNotFound, threadID)
	}

	if err != nil {
		return Summary{}, fmt.Errorf("store: get summary: %w", err)
	}

	sum.UpdatedAt = fromUnix(updated)

	return sum, nil
}
