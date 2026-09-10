package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/pavelvarganov/slack-threads/internal/store"
)

// newThread adds a thread and returns its ID.
func newThread(t *testing.T, s *store.Store, channelID, threadTS string) int64 {
	t.Helper()

	th, err := s.AddThread(context.Background(), store.Thread{ChannelID: channelID, ThreadTS: threadTS})
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	return th.ID
}

// translateAll stores a translation for every message of the thread.
func translateAll(t *testing.T, s *store.Store, threadID int64) {
	t.Helper()

	ctx := context.Background()

	msgs, err := s.ListMessages(ctx, threadID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	trs := make([]store.Translation, 0, len(msgs))
	for _, m := range msgs {
		trs = append(trs, store.Translation{MessageID: m.ID, TextRU: "перевод " + m.TS, Model: "opus"})
	}

	if err := s.SaveTranslations(ctx, trs); err != nil {
		t.Fatalf("SaveTranslations: %v", err)
	}
}

func TestTextHash(t *testing.T) {
	t.Parallel()

	want := sha256.Sum256([]byte("hello"))
	if got := store.TextHash("hello"); got != hex.EncodeToString(want[:]) {
		t.Fatalf("TextHash = %q, want sha256 hex", got)
	}

	if store.TextHash("hello") == store.TextHash("hello!") {
		t.Fatal("different texts hashed to the same value")
	}

	if store.TextHash("") == "" {
		t.Fatal("TextHash(\"\") = empty, want a digest")
	}
}

func TestUpsertMessagesInserts(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	threadID := newThread(t, s, "C1", "1.1")

	stats, err := s.UpsertMessages(ctx, threadID, []store.Message{
		{TS: "1.1", UserID: "U1", Text: "hello", RawJSON: `{"ts":"1.1"}`},
		{TS: "2.2", UserID: "U2", Text: "world"},
	})
	if err != nil {
		t.Fatalf("UpsertMessages: %v", err)
	}

	if stats != (store.UpsertStats{Inserted: 2}) {
		t.Fatalf("stats = %+v, want 2 inserted", stats)
	}

	msgs, err := s.ListMessages(ctx, threadID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	if len(msgs) != 2 {
		t.Fatalf("stored messages = %d, want 2", len(msgs))
	}

	first := msgs[0]
	if first.TS != "1.1" || first.UserID != "U1" || first.Text != "hello" || first.RawJSON != `{"ts":"1.1"}` {
		t.Fatalf("first message = %+v, want the one that was inserted", first)
	}

	if first.ThreadID != threadID {
		t.Fatalf("thread ID = %d, want %d", first.ThreadID, threadID)
	}

	if first.TextHash != store.TextHash("hello") {
		t.Fatalf("text hash = %q, want hash of the text", first.TextHash)
	}
}

func TestUpsertMessagesEmptyIsNoop(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	threadID := newThread(t, s, "C1", "1.1")

	stats, err := s.UpsertMessages(context.Background(), threadID, nil)
	if err != nil {
		t.Fatalf("UpsertMessages(nil): %v", err)
	}

	if stats != (store.UpsertStats{}) {
		t.Fatalf("stats = %+v, want zero", stats)
	}
}

func TestUpsertMessagesRejectsMissingTS(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	threadID := newThread(t, s, "C1", "1.1")

	if _, err := s.UpsertMessages(ctx, threadID, []store.Message{{Text: "no ts"}}); err == nil {
		t.Fatal("UpsertMessages without ts: want error, got nil")
	}

	if n := countRows(t, s, "messages", ""); n != 0 {
		t.Fatalf("rows after failed upsert = %d, want 0 (transaction rolled back)", n)
	}
}

func TestUpsertMessagesUnchangedKeepsTranslation(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	threadID := newThread(t, s, "C1", "1.1")

	msgs := []store.Message{{TS: "1.1", UserID: "U1", Text: "hello"}}
	if _, err := s.UpsertMessages(ctx, threadID, msgs); err != nil {
		t.Fatalf("UpsertMessages: %v", err)
	}

	translateAll(t, s, threadID)

	stats, err := s.UpsertMessages(ctx, threadID, msgs)
	if err != nil {
		t.Fatalf("second UpsertMessages: %v", err)
	}

	if stats != (store.UpsertStats{Unchanged: 1}) {
		t.Fatalf("stats = %+v, want 1 unchanged", stats)
	}

	if n := countRows(t, s, "translations", ""); n != 1 {
		t.Fatalf("translations = %d, want the existing one kept", n)
	}

	pending, err := s.UntranslatedMessages(ctx, threadID)
	if err != nil {
		t.Fatalf("UntranslatedMessages: %v", err)
	}

	if len(pending) != 0 {
		t.Fatalf("pending = %d, want 0", len(pending))
	}
}

func TestUpsertMessagesChangedTextInvalidatesTranslation(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	threadID := newThread(t, s, "C1", "1.1")

	if _, err := s.UpsertMessages(ctx, threadID, []store.Message{
		{TS: "1.1", UserID: "U1", Text: "hello"},
		{TS: "2.2", UserID: "U2", Text: "world"},
	}); err != nil {
		t.Fatalf("UpsertMessages: %v", err)
	}

	translateAll(t, s, threadID)

	stats, err := s.UpsertMessages(ctx, threadID, []store.Message{
		{TS: "1.1", UserID: "U1", Text: "hello, edited", EditedTS: "3.3"},
		{TS: "2.2", UserID: "U2", Text: "world"},
		{TS: "4.4", UserID: "U3", Text: "new one"},
	})
	if err != nil {
		t.Fatalf("second UpsertMessages: %v", err)
	}

	want := store.UpsertStats{Inserted: 1, Updated: 1, Unchanged: 1}
	if stats != want {
		t.Fatalf("stats = %+v, want %+v", stats, want)
	}

	msgs, err := s.ListMessages(ctx, threadID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3", len(msgs))
	}

	if msgs[0].Text != "hello, edited" || msgs[0].EditedTS != "3.3" {
		t.Fatalf("edited message = %+v, want the new text", msgs[0])
	}

	if msgs[0].TextHash != store.TextHash("hello, edited") {
		t.Fatalf("hash not refreshed: %q", msgs[0].TextHash)
	}

	pending, err := s.UntranslatedMessages(ctx, threadID)
	if err != nil {
		t.Fatalf("UntranslatedMessages: %v", err)
	}

	gotTS := make([]string, len(pending))
	for i, m := range pending {
		gotTS[i] = m.TS
	}

	if len(gotTS) != 2 || gotTS[0] != "1.1" || gotTS[1] != "4.4" {
		t.Fatalf("pending timestamps = %v, want [1.1 4.4]", gotTS)
	}
}

func TestUpsertMessagesMetadataOnlyChangeKeepsTranslation(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	threadID := newThread(t, s, "C1", "1.1")

	if _, err := s.UpsertMessages(ctx, threadID, []store.Message{
		{TS: "1.1", UserID: "U1", Text: "hello", RawJSON: `{"v":1}`},
	}); err != nil {
		t.Fatalf("UpsertMessages: %v", err)
	}

	translateAll(t, s, threadID)

	stats, err := s.UpsertMessages(ctx, threadID, []store.Message{
		{TS: "1.1", UserID: "U1", Text: "hello", RawJSON: `{"v":2,"reactions":[]}`},
	})
	if err != nil {
		t.Fatalf("second UpsertMessages: %v", err)
	}

	if stats != (store.UpsertStats{Updated: 1}) {
		t.Fatalf("stats = %+v, want 1 updated", stats)
	}

	if n := countRows(t, s, "translations", ""); n != 1 {
		t.Fatalf("translations = %d, want 1 (text did not change)", n)
	}

	msgs, err := s.ListMessages(ctx, threadID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	if msgs[0].RawJSON != `{"v":2,"reactions":[]}` {
		t.Fatalf("raw payload = %q, want the refreshed one", msgs[0].RawJSON)
	}
}

func TestUpsertMessagesIsolatesThreads(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	a := newThread(t, s, "C1", "1.1")
	b := newThread(t, s, "C2", "2.2")

	for _, id := range []int64{a, b} {
		if _, err := s.UpsertMessages(ctx, id, []store.Message{{TS: "1.1", Text: "same ts"}}); err != nil {
			t.Fatalf("UpsertMessages: %v", err)
		}
	}

	for _, id := range []int64{a, b} {
		msgs, err := s.ListMessages(ctx, id)
		if err != nil {
			t.Fatalf("ListMessages: %v", err)
		}

		if len(msgs) != 1 {
			t.Fatalf("thread %d messages = %d, want 1", id, len(msgs))
		}
	}
}

func TestUpsertMessagesUnknownThread(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)

	if _, err := s.UpsertMessages(context.Background(), 4242, []store.Message{{TS: "1.1"}}); err == nil {
		t.Fatal("UpsertMessages into a missing thread: want foreign key error, got nil")
	}
}

func TestGetMessage(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	threadID := newThread(t, s, "C1", "1.1")

	if _, err := s.UpsertMessages(ctx, threadID, []store.Message{{TS: "1.1", Text: "hello"}}); err != nil {
		t.Fatalf("UpsertMessages: %v", err)
	}

	msgs, err := s.ListMessages(ctx, threadID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	got, err := s.GetMessage(ctx, msgs[0].ID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}

	if got != msgs[0] {
		t.Fatalf("GetMessage = %+v, want %+v", got, msgs[0])
	}

	if _, err := s.GetMessage(ctx, 4242); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetMessage(missing) = %v, want ErrNotFound", err)
	}
}

func TestListMessagesEmptyThread(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	threadID := newThread(t, s, "C1", "1.1")

	msgs, err := s.ListMessages(context.Background(), threadID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	if len(msgs) != 0 {
		t.Fatalf("messages = %d, want 0", len(msgs))
	}
}

func TestDeleteThreadRemovesMessages(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()
	threadID := newThread(t, s, "C1", "1.1")

	if _, err := s.UpsertMessages(ctx, threadID, []store.Message{{TS: "1.1", Text: "hello"}}); err != nil {
		t.Fatalf("UpsertMessages: %v", err)
	}

	translateAll(t, s, threadID)

	if err := s.DeleteThread(ctx, threadID); err != nil {
		t.Fatalf("DeleteThread: %v", err)
	}

	if n := countRows(t, s, "messages", ""); n != 0 {
		t.Fatalf("messages left = %d, want 0", n)
	}

	if n := countRows(t, s, "translations", ""); n != 0 {
		t.Fatalf("translations left = %d, want 0", n)
	}
}

func TestUpsertMessagesRespectsContext(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	threadID := newThread(t, s, "C1", "1.1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.UpsertMessages(ctx, threadID, []store.Message{{TS: "1.1", Text: "hello"}}); err == nil {
		t.Fatal("UpsertMessages with a cancelled context: want error, got nil")
	}

	if n := countRows(t, s, "messages", ""); n != 0 {
		t.Fatalf("messages = %d, want 0", n)
	}
}
