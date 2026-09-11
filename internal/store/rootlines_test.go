package store_test

import (
	"context"
	"testing"

	"github.com/pavelvarganov/slack-threads/internal/store"
)

func TestRootTranslationsReturnsTheTranslatedRoot(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()

	id := newThread(t, s, "C1", "100.000")

	if _, err := s.UpsertMessages(ctx, id, []store.Message{
		{TS: "100.000", UserID: "U1", Text: "Deploy is stuck"},
		{TS: "101.000", UserID: "U2", Text: "Looking"},
	}); err != nil {
		t.Fatalf("UpsertMessages: %v", err)
	}

	msgs, err := s.ListMessages(ctx, id)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	if err := s.SaveTranslations(ctx, []store.Translation{
		{MessageID: msgs[0].ID, TextRU: "Деплой завис", Model: "opus"},
		{MessageID: msgs[1].ID, TextRU: "Смотрю", Model: "opus"},
	}); err != nil {
		t.Fatalf("SaveTranslations: %v", err)
	}

	roots, err := s.RootTranslations(ctx)
	if err != nil {
		t.Fatalf("RootTranslations: %v", err)
	}

	if roots[id] != "Деплой завис" {
		t.Errorf("root = %q, want the translation of the root message only", roots[id])
	}
}

// Пока корень не переведён, треда в выборке нет — список берёт заголовок
// из того, что прислал Slack.
func TestRootTranslationsSkipsUntranslatedAndEmptyThreads(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()

	withMessage := newThread(t, s, "C1", "100.000")
	empty := newThread(t, s, "C2", "200.000")

	if _, err := s.UpsertMessages(ctx, withMessage, []store.Message{
		{TS: "100.000", UserID: "U1", Text: "Deploy is stuck"},
	}); err != nil {
		t.Fatalf("UpsertMessages: %v", err)
	}

	roots, err := s.RootTranslations(ctx)
	if err != nil {
		t.Fatalf("RootTranslations: %v", err)
	}

	if _, ok := roots[withMessage]; ok {
		t.Error("an untranslated root must not appear")
	}

	if _, ok := roots[empty]; ok {
		t.Error("a thread with no messages must not appear")
	}
}
