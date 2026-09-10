package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pavelvarganov/slack-threads/internal/store"
)

func TestSaveAndGetDraft(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	threadID := newThread(t, s, "C1", "1.1")

	updated := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	draft := store.Draft{
		ThreadID:  threadID,
		TextRU:    "откатил `billing`",
		TextEN:    "rolled `billing` back",
		BackRU:    "откатил `billing` назад",
		UpdatedAt: updated,
	}

	if err := s.SaveDraft(ctx, draft); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	got, err := s.GetDraft(ctx, threadID)
	if err != nil {
		t.Fatalf("GetDraft: %v", err)
	}

	if got != draft {
		t.Fatalf("draft = %+v, want %+v", got, draft)
	}
}

func TestSaveDraftReplacesPrevious(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	threadID := newThread(t, s, "C1", "1.1")

	if err := s.SaveDraft(ctx, store.Draft{ThreadID: threadID, TextRU: "первый", TextEN: "first"}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	if err := s.SaveDraft(ctx, store.Draft{ThreadID: threadID, TextRU: "второй", TextEN: "second"}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	got, err := s.GetDraft(ctx, threadID)
	if err != nil {
		t.Fatalf("GetDraft: %v", err)
	}

	if got.TextRU != "второй" || got.TextEN != "second" {
		t.Fatalf("draft = %+v, want the second one", got)
	}

	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt was not filled in by the store")
	}
}

func TestGetDraftMissing(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	threadID := newThread(t, s, "C1", "1.1")

	_, err := s.GetDraft(context.Background(), threadID)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetDraft error = %v, want ErrNotFound", err)
	}
}

func TestSaveDraftWithoutThreadID(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)

	if err := s.SaveDraft(context.Background(), store.Draft{TextRU: "текст"}); err == nil {
		t.Fatal("SaveDraft accepted a draft without a thread ID")
	}
}

func TestDeleteDraft(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	threadID := newThread(t, s, "C1", "1.1")

	if err := s.SaveDraft(ctx, store.Draft{ThreadID: threadID, TextRU: "текст"}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	if err := s.DeleteDraft(ctx, threadID); err != nil {
		t.Fatalf("DeleteDraft: %v", err)
	}

	if _, err := s.GetDraft(ctx, threadID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetDraft after delete = %v, want ErrNotFound", err)
	}

	// Deleting again is a no-op, not an error.
	if err := s.DeleteDraft(ctx, threadID); err != nil {
		t.Fatalf("DeleteDraft (second time): %v", err)
	}
}

func TestDeleteThreadDropsDraft(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	threadID := newThread(t, s, "C1", "1.1")

	if err := s.SaveDraft(ctx, store.Draft{ThreadID: threadID, TextRU: "текст"}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	if err := s.DeleteThread(ctx, threadID); err != nil {
		t.Fatalf("DeleteThread: %v", err)
	}

	if _, err := s.GetDraft(ctx, threadID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetDraft after thread delete = %v, want ErrNotFound", err)
	}
}

func TestSetThreadNeedsRefresh(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	threadID := newThread(t, s, "C1", "1.1")

	th, err := s.GetThread(ctx, threadID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}

	if th.NeedsRefresh {
		t.Fatal("a fresh thread must not be flagged as needing a refresh")
	}

	if err := s.SetThreadNeedsRefresh(ctx, threadID, true); err != nil {
		t.Fatalf("SetThreadNeedsRefresh: %v", err)
	}

	if th, err = s.GetThread(ctx, threadID); err != nil || !th.NeedsRefresh {
		t.Fatalf("thread = %+v, err = %v, want NeedsRefresh", th, err)
	}

	if err := s.SetThreadNeedsRefresh(ctx, threadID, false); err != nil {
		t.Fatalf("SetThreadNeedsRefresh(false): %v", err)
	}

	if th, err = s.GetThread(ctx, threadID); err != nil || th.NeedsRefresh {
		t.Fatalf("thread = %+v, err = %v, want the flag cleared", th, err)
	}

	if err := s.SetThreadNeedsRefresh(ctx, threadID+100, true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SetThreadNeedsRefresh on a missing thread = %v, want ErrNotFound", err)
	}
}
