package store

import (
	"context"
	"fmt"
)

// rootTranslationQuery reads the translated root message of every thread
// in one pass, so the thread list does not cost a query per thread.
const rootTranslationQuery = `
	SELECT m.thread_id, COALESCE(t.text_ru, '')
	FROM messages m
	JOIN threads th ON th.id = m.thread_id AND m.ts = th.thread_ts
	LEFT JOIN translations t ON t.message_id = m.id`

// RootTranslations returns the Russian text of every thread's root
// message, keyed by thread ID. A thread whose root is not translated yet
// is absent, as is a thread with no messages: the caller falls back to
// the title Slack gave it.
func (s *Store) RootTranslations(ctx context.Context) (map[int64]string, error) {
	rows, err := s.db.QueryContext(ctx, rootTranslationQuery)
	if err != nil {
		return nil, fmt.Errorf("store: root translations: %w", err)
	}
	defer rows.Close() //nolint:errcheck // read-only query

	out := make(map[int64]string)

	for rows.Next() {
		var (
			threadID int64
			textRU   string
		)

		if err := rows.Scan(&threadID, &textRU); err != nil {
			return nil, fmt.Errorf("store: root translations: %w", err)
		}

		if textRU != "" {
			out[threadID] = textRU
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: root translations: %w", err)
	}

	return out, nil
}
