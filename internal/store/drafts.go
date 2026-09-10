package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Draft is the reply being written for a thread: the Russian text the user
// typed, its English translation and the back translation shown for
// control. Only one draft per thread exists; writing a new one replaces it.
type Draft struct {
	// ThreadID is the thread the reply answers.
	ThreadID int64
	// TextRU is what the user wrote.
	TextRU string
	// TextEN is the translation that will be sent to Slack.
	TextEN string
	// BackRU is the English text translated back, for the user to check.
	BackRU string
	// UpdatedAt is when the draft was last written.
	UpdatedAt time.Time
}

// SaveDraft stores (or replaces) the draft reply of a thread.
func (s *Store) SaveDraft(ctx context.Context, d Draft) error {
	if d.ThreadID == 0 {
		return errors.New("store: draft needs a thread ID")
	}

	if d.UpdatedAt.IsZero() {
		d.UpdatedAt = s.now()
	}

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO drafts (thread_id, text_ru, text_en, back_ru, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (thread_id) DO UPDATE SET
		   text_ru = excluded.text_ru, text_en = excluded.text_en,
		   back_ru = excluded.back_ru, updated_at = excluded.updated_at`,
		d.ThreadID, d.TextRU, d.TextEN, d.BackRU, toUnix(d.UpdatedAt)); err != nil {
		return fmt.Errorf("store: save draft for thread %d: %w", d.ThreadID, err)
	}

	return nil
}

// GetDraft returns the draft reply of a thread, or ErrNotFound when the
// thread has none.
func (s *Store) GetDraft(ctx context.Context, threadID int64) (Draft, error) {
	var (
		d       Draft
		updated int64
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT thread_id, text_ru, text_en, back_ru, updated_at FROM drafts WHERE thread_id = ?`, threadID).
		Scan(&d.ThreadID, &d.TextRU, &d.TextEN, &d.BackRU, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, fmt.Errorf("%w: draft for thread %d", ErrNotFound, threadID)
	}

	if err != nil {
		return Draft{}, fmt.Errorf("store: get draft: %w", err)
	}

	d.UpdatedAt = fromUnix(updated)

	return d, nil
}

// DeleteDraft drops the draft of a thread, e.g. after the reply was sent.
// Deleting a draft that does not exist is not an error: the caller only
// cares that nothing is left behind.
func (s *Store) DeleteDraft(ctx context.Context, threadID int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM drafts WHERE thread_id = ?`, threadID); err != nil {
		return fmt.Errorf("store: delete draft for thread %d: %w", threadID, err)
	}

	return nil
}
