package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pavelvarganov/slack-threads/internal/store"
)

// seedMessages upserts the given messages and returns their IDs keyed by ts.
func seedMessages(t *testing.T, s *store.Store, threadID int64, msgs ...store.Message) map[string]int64 {
	t.Helper()

	ctx := context.Background()

	if _, err := s.UpsertMessages(ctx, threadID, msgs); err != nil {
		t.Fatalf("UpsertMessages: %v", err)
	}

	stored, err := s.ListMessages(ctx, threadID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	ids := make(map[string]int64, len(stored))
	for _, m := range stored {
		ids[m.TS] = m.ID
	}

	return ids
}

func TestSaveAndListTranslations(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	threadID := newThread(t, s, "C1", "1.1")
	ids := seedMessages(t, s,
		threadID,
		store.Message{TS: "1.1", Text: "hello"},
		store.Message{TS: "2.2", Text: "world"},
	)

	created := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	if err := s.SaveTranslations(ctx, []store.Translation{
		{MessageID: ids["1.1"], TextRU: "привет", Model: "opus", CreatedAt: created},
		{MessageID: ids["2.2"], TextRU: "мир", Model: "opus", CreatedAt: created},
	}); err != nil {
		t.Fatalf("SaveTranslations: %v", err)
	}

	got, err := s.ListTranslations(ctx, threadID)
	if err != nil {
		t.Fatalf("ListTranslations: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("translations = %d, want 2", len(got))
	}

	tr := got[ids["1.1"]]
	if tr.TextRU != "привет" || tr.Model != "opus" || !tr.CreatedAt.Equal(created) {
		t.Fatalf("translation = %+v, want the saved one", tr)
	}
}

func TestSaveTranslationsReplaces(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	threadID := newThread(t, s, "C1", "1.1")
	ids := seedMessages(t, s, threadID, store.Message{TS: "1.1", Text: "hello"})

	for _, text := range []string{"привет", "здравствуйте"} {
		if err := s.SaveTranslations(ctx, []store.Translation{
			{MessageID: ids["1.1"], TextRU: text, Model: "opus"},
		}); err != nil {
			t.Fatalf("SaveTranslations(%q): %v", text, err)
		}
	}

	got, err := s.ListTranslations(ctx, threadID)
	if err != nil {
		t.Fatalf("ListTranslations: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("translations = %d, want 1 row replaced in place", len(got))
	}

	if got[ids["1.1"]].TextRU != "здравствуйте" {
		t.Fatalf("text = %q, want the latest translation", got[ids["1.1"]].TextRU)
	}
}

func TestSaveTranslationsDefaultsCreatedAt(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	threadID := newThread(t, s, "C1", "1.1")
	ids := seedMessages(t, s, threadID, store.Message{TS: "1.1", Text: "hello"})

	before := time.Now().Add(-time.Second)

	if err := s.SaveTranslations(ctx, []store.Translation{{MessageID: ids["1.1"], TextRU: "привет"}}); err != nil {
		t.Fatalf("SaveTranslations: %v", err)
	}

	got, err := s.ListTranslations(ctx, threadID)
	if err != nil {
		t.Fatalf("ListTranslations: %v", err)
	}

	if got[ids["1.1"]].CreatedAt.Before(before) {
		t.Fatalf("CreatedAt = %v, want a fresh timestamp", got[ids["1.1"]].CreatedAt)
	}
}

func TestSaveTranslationsErrors(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	threadID := newThread(t, s, "C1", "1.1")
	ids := seedMessages(t, s, threadID, store.Message{TS: "1.1", Text: "hello"})

	if err := s.SaveTranslations(ctx, nil); err != nil {
		t.Fatalf("SaveTranslations(nil): %v", err)
	}

	if err := s.SaveTranslations(ctx, []store.Translation{{TextRU: "без id"}}); err == nil {
		t.Fatal("SaveTranslations without message ID: want error, got nil")
	}

	// A batch fails as a whole: the valid row must not survive.
	err := s.SaveTranslations(ctx, []store.Translation{
		{MessageID: ids["1.1"], TextRU: "привет"},
		{MessageID: 4242, TextRU: "сирота"},
	})
	if err == nil {
		t.Fatal("SaveTranslations with a dangling message ID: want error, got nil")
	}

	if n := countRows(t, s, "translations", ""); n != 0 {
		t.Fatalf("translations = %d, want 0 (transaction rolled back)", n)
	}
}

func TestListTranslationsScopedToThread(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	a := newThread(t, s, "C1", "1.1")
	b := newThread(t, s, "C2", "2.2")
	idsA := seedMessages(t, s, a, store.Message{TS: "1.1", Text: "hello"})
	idsB := seedMessages(t, s, b, store.Message{TS: "1.1", Text: "hello"})

	if err := s.SaveTranslations(ctx, []store.Translation{
		{MessageID: idsA["1.1"], TextRU: "первый"},
		{MessageID: idsB["1.1"], TextRU: "второй"},
	}); err != nil {
		t.Fatalf("SaveTranslations: %v", err)
	}

	got, err := s.ListTranslations(ctx, a)
	if err != nil {
		t.Fatalf("ListTranslations: %v", err)
	}

	if len(got) != 1 || got[idsA["1.1"]].TextRU != "первый" {
		t.Fatalf("translations for thread A = %+v, want only its own", got)
	}
}

func TestSaveAndGetSummary(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	threadID := newThread(t, s, "C1", "1.1")

	updated := time.Date(2026, 9, 10, 9, 30, 0, 0, time.UTC)
	if err := s.SaveSummary(ctx, store.Summary{
		ThreadID:  threadID,
		TextRU:    "обсуждают деплой",
		BasedOnTS: "2.2",
		UpdatedAt: updated,
	}); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}

	got, err := s.GetSummary(ctx, threadID)
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}

	if got.TextRU != "обсуждают деплой" || got.BasedOnTS != "2.2" || !got.UpdatedAt.Equal(updated) {
		t.Fatalf("summary = %+v, want the saved one", got)
	}

	// Saving again replaces the row instead of failing.
	if err := s.SaveSummary(ctx, store.Summary{ThreadID: threadID, TextRU: "новая суть", BasedOnTS: "3.3"}); err != nil {
		t.Fatalf("SaveSummary (replace): %v", err)
	}

	got, err = s.GetSummary(ctx, threadID)
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}

	if got.TextRU != "новая суть" || got.BasedOnTS != "3.3" {
		t.Fatalf("summary = %+v, want the replacement", got)
	}

	if got.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt is zero, want a default timestamp")
	}

	if n := countRows(t, s, "summaries", ""); n != 1 {
		t.Fatalf("summaries = %d, want 1", n)
	}
}

func TestSummaryErrors(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()

	if err := s.SaveSummary(ctx, store.Summary{TextRU: "без треда"}); err == nil {
		t.Fatal("SaveSummary without thread ID: want error, got nil")
	}

	if err := s.SaveSummary(ctx, store.Summary{ThreadID: 4242, TextRU: "сирота"}); err == nil {
		t.Fatal("SaveSummary for a missing thread: want foreign key error, got nil")
	}

	if _, err := s.GetSummary(ctx, 4242); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetSummary(missing) = %v, want ErrNotFound", err)
	}
}
