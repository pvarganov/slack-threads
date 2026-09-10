package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/pavelvarganov/slack-threads/internal/store"
)

// openTemp opens a store on a fresh database inside the test's temp dir.
func openTemp(t *testing.T) (*store.Store, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "threads.db")
	s := openAt(t, path)

	return s, path
}

// openAt opens a store on the given path and closes it when the test ends.
func openAt(t *testing.T, path string) *store.Store {
	t.Helper()

	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}

	t.Cleanup(func() { s.Close() })

	return s
}

func TestOpenCreatesSchema(t *testing.T) {
	t.Parallel()

	s, path := openTemp(t)
	ctx := context.Background()

	if _, err := filepath.Abs(path); err != nil {
		t.Fatalf("abs path: %v", err)
	}

	version, err := s.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}

	if version != store.SchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, store.SchemaVersion)
	}

	want := []string{"drafts", "messages", "summaries", "threads", "translations", "users"}
	for _, table := range want {
		var name string

		err := store.DBOf(s).QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "threads.db")

	first := openAt(t, path)
	ctx := context.Background()

	added, err := first.AddThread(ctx, store.Thread{ChannelID: "C1", ThreadTS: "1.1"})
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second := openAt(t, path)

	version, err := second.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}

	if version != store.SchemaVersion {
		t.Fatalf("schema version after reopen = %d, want %d", version, store.SchemaVersion)
	}

	got, err := second.GetThread(ctx, added.ID)
	if err != nil {
		t.Fatalf("GetThread after reopen: %v", err)
	}

	if got.ChannelID != "C1" {
		t.Fatalf("channel after reopen = %q, want C1", got.ChannelID)
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "threads.db")
	s := openAt(t, path)
	ctx := context.Background()

	if _, err := store.DBOf(s).ExecContext(ctx, `PRAGMA user_version = 999`); err != nil {
		t.Fatalf("bump user_version: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := store.Open(ctx, path)
	if err == nil {
		reopened.Close()
		t.Fatal("Open on a newer schema: want error, got nil")
	}
}

func TestOpenEmptyPath(t *testing.T) {
	t.Parallel()

	if _, err := store.Open(context.Background(), ""); err == nil {
		t.Fatal("Open(\"\"): want error, got nil")
	}
}

func TestForeignKeysEnabled(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()

	_, err := store.DBOf(s).ExecContext(ctx,
		`INSERT INTO messages (thread_id, ts) VALUES (4242, '1.1')`)
	if err == nil {
		t.Fatal("insert with dangling thread_id: want foreign key error, got nil")
	}
}

// seedThreadChildren inserts one row into every child table of the thread.
func seedThreadChildren(t *testing.T, s *store.Store, threadID int64) {
	t.Helper()

	ctx := context.Background()
	db := store.DBOf(s)
	now := time.Now().UnixMilli()

	res, err := db.ExecContext(ctx,
		`INSERT INTO messages (thread_id, ts, user_id, text, raw_json, text_hash)
		 VALUES (?, '1.1', 'U1', 'hello', '{}', 'h1')`, threadID)
	if err != nil {
		t.Fatalf("seed message: %v", err)
	}

	msgID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed message id: %v", err)
	}

	stmts := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO translations (message_id, text_ru, model, created_at) VALUES (?, 'привет', 'opus', ?)`, []any{msgID, now}},
		{`INSERT INTO summaries (thread_id, text_ru, based_on_ts, updated_at) VALUES (?, 'суть', '1.1', ?)`, []any{threadID, now}},
		{`INSERT INTO drafts (thread_id, text_ru, text_en, back_ru, updated_at) VALUES (?, 'ру', 'en', 'обратно', ?)`, []any{threadID, now}},
	}

	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s.query, s.args...); err != nil {
			t.Fatalf("seed %q: %v", s.query, err)
		}
	}
}

// countRows counts rows in table matching the where clause.
func countRows(t *testing.T, s *store.Store, table, where string, args ...any) int {
	t.Helper()

	var n int

	query := "SELECT COUNT(*) FROM " + table
	if where != "" {
		query += " WHERE " + where
	}

	if err := store.DBOf(s).QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}

	return n
}

func TestAddThread(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()

	added, err := s.AddThread(ctx, store.Thread{
		ChannelID: "C024BE91L",
		ThreadTS:  "1788872615.903009",
		Workspace: "overgearcom",
		TeamID:    "T024BE91L",
		Title:     "deploy incident",
	})
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	if added.ID == 0 {
		t.Fatal("AddThread returned zero ID")
	}

	if added.AddedAt.IsZero() {
		t.Fatal("AddThread did not fill AddedAt")
	}

	got, err := s.GetThread(ctx, added.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}

	if got.ChannelID != added.ChannelID || got.ThreadTS != added.ThreadTS ||
		got.Workspace != added.Workspace || got.TeamID != added.TeamID || got.Title != added.Title {
		t.Fatalf("GetThread = %+v, want %+v", got, added)
	}

	if got.Archived {
		t.Fatal("new thread must not be archived")
	}

	if !got.LastFetchedAt.IsZero() {
		t.Fatalf("LastFetchedAt = %v, want zero", got.LastFetchedAt)
	}

	if !got.AddedAt.Equal(added.AddedAt.UTC().Truncate(time.Millisecond)) {
		t.Fatalf("AddedAt = %v, want %v", got.AddedAt, added.AddedAt)
	}
}

func TestAddThreadValidation(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()

	tests := []struct {
		name   string
		thread store.Thread
	}{
		{"no channel", store.Thread{ThreadTS: "1.1"}},
		{"no timestamp", store.Thread{ChannelID: "C1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.AddThread(ctx, tt.thread); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestAddThreadDuplicate(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()

	first := store.Thread{ChannelID: "C1", ThreadTS: "1788872615.903009", Title: "first"}
	if _, err := s.AddThread(ctx, first); err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	_, err := s.AddThread(ctx, store.Thread{ChannelID: "C1", ThreadTS: "1788872615.903009", Title: "second"})
	if !errors.Is(err, store.ErrThreadExists) {
		t.Fatalf("duplicate AddThread error = %v, want ErrThreadExists", err)
	}

	// The same timestamp in another channel is a different thread.
	if _, err := s.AddThread(ctx, store.Thread{ChannelID: "C2", ThreadTS: "1788872615.903009"}); err != nil {
		t.Fatalf("AddThread in another channel: %v", err)
	}

	if n := countRows(t, s, "threads", ""); n != 2 {
		t.Fatalf("threads stored = %d, want 2", n)
	}
}

func TestGetThreadByKey(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()

	added, err := s.AddThread(ctx, store.Thread{ChannelID: "C1", ThreadTS: "1.1"})
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	got, err := s.GetThreadByKey(ctx, "C1", "1.1")
	if err != nil {
		t.Fatalf("GetThreadByKey: %v", err)
	}

	if got.ID != added.ID {
		t.Fatalf("ID = %d, want %d", got.ID, added.ID)
	}

	if _, err := s.GetThreadByKey(ctx, "C1", "9.9"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing thread error = %v, want ErrNotFound", err)
	}
}

func TestGetThreadNotFound(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)

	if _, err := s.GetThread(context.Background(), 404); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

func TestListThreads(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()

	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	oldest, err := s.AddThread(ctx, store.Thread{ChannelID: "C1", ThreadTS: "1.1", AddedAt: base})
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	newest, err := s.AddThread(ctx, store.Thread{ChannelID: "C2", ThreadTS: "2.2", AddedAt: base.Add(time.Hour)})
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	archived, err := s.AddThread(ctx, store.Thread{ChannelID: "C3", ThreadTS: "3.3", AddedAt: base.Add(30 * time.Minute)})
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	if err := s.SetThreadArchived(ctx, archived.ID, true); err != nil {
		t.Fatalf("SetThreadArchived: %v", err)
	}

	active, err := s.ListThreads(ctx, false)
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}

	if len(active) != 2 {
		t.Fatalf("active threads = %d, want 2", len(active))
	}

	if active[0].ID != newest.ID || active[1].ID != oldest.ID {
		t.Fatalf("active order = [%d %d], want [%d %d]", active[0].ID, active[1].ID, newest.ID, oldest.ID)
	}

	all, err := s.ListThreads(ctx, true)
	if err != nil {
		t.Fatalf("ListThreads(all): %v", err)
	}

	if len(all) != 3 {
		t.Fatalf("all threads = %d, want 3", len(all))
	}

	if all[1].ID != archived.ID || !all[1].Archived {
		t.Fatalf("archived thread misplaced: %+v", all[1])
	}
}

func TestListThreadsEmpty(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)

	got, err := s.ListThreads(context.Background(), true)
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("threads = %d, want 0", len(got))
	}
}

func TestThreadMutators(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()

	added, err := s.AddThread(ctx, store.Thread{ChannelID: "C1", ThreadTS: "1.1"})
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	fetched := time.Date(2026, 9, 10, 15, 4, 5, 0, time.UTC)

	if err := s.SetThreadTitle(ctx, added.ID, "release checklist"); err != nil {
		t.Fatalf("SetThreadTitle: %v", err)
	}

	if err := s.SetThreadFetched(ctx, added.ID, fetched); err != nil {
		t.Fatalf("SetThreadFetched: %v", err)
	}

	if err := s.SetThreadSession(ctx, added.ID, "sess-42"); err != nil {
		t.Fatalf("SetThreadSession: %v", err)
	}

	if err := s.SetThreadArchived(ctx, added.ID, true); err != nil {
		t.Fatalf("SetThreadArchived: %v", err)
	}

	got, err := s.GetThread(ctx, added.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}

	if got.Title != "release checklist" {
		t.Fatalf("Title = %q", got.Title)
	}

	if !got.LastFetchedAt.Equal(fetched) {
		t.Fatalf("LastFetchedAt = %v, want %v", got.LastFetchedAt, fetched)
	}

	if got.ClaudeSessionID != "sess-42" {
		t.Fatalf("ClaudeSessionID = %q", got.ClaudeSessionID)
	}

	if !got.Archived {
		t.Fatal("thread must be archived")
	}

	if err := s.SetThreadArchived(ctx, added.ID, false); err != nil {
		t.Fatalf("SetThreadArchived(false): %v", err)
	}

	got, err = s.GetThread(ctx, added.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}

	if got.Archived {
		t.Fatal("thread must be unarchived")
	}
}

func TestSetThreadFetchedDefaultsToNow(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()

	added, err := s.AddThread(ctx, store.Thread{ChannelID: "C1", ThreadTS: "1.1"})
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	before := time.Now().Add(-time.Second)

	if err := s.SetThreadFetched(ctx, added.ID, time.Time{}); err != nil {
		t.Fatalf("SetThreadFetched: %v", err)
	}

	got, err := s.GetThread(ctx, added.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}

	if got.LastFetchedAt.Before(before) {
		t.Fatalf("LastFetchedAt = %v, want a recent timestamp", got.LastFetchedAt)
	}
}

func TestMutatorsOnMissingThread(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()

	tests := map[string]func() error{
		"archive":      func() error { return s.SetThreadArchived(ctx, 404, true) },
		"title":        func() error { return s.SetThreadTitle(ctx, 404, "x") },
		"fetched":      func() error { return s.SetThreadFetched(ctx, 404, time.Now()) },
		"session":      func() error { return s.SetThreadSession(ctx, 404, "s") },
		"needsRefresh": func() error { return s.SetThreadNeedsRefresh(ctx, 404, true) },
		"delete":       func() error { return s.DeleteThread(ctx, 404) },
	}

	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestDeleteThreadCascades(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()

	victim, err := s.AddThread(ctx, store.Thread{ChannelID: "C1", ThreadTS: "1.1"})
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	survivor, err := s.AddThread(ctx, store.Thread{ChannelID: "C2", ThreadTS: "2.2"})
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	seedThreadChildren(t, s, victim.ID)
	seedThreadChildren(t, s, survivor.ID)

	if err := s.DeleteThread(ctx, victim.ID); err != nil {
		t.Fatalf("DeleteThread: %v", err)
	}

	if _, err := s.GetThread(ctx, victim.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted thread error = %v, want ErrNotFound", err)
	}

	for _, table := range []string{"messages", "summaries", "drafts"} {
		if n := countRows(t, s, table, "thread_id = ?", victim.ID); n != 0 {
			t.Fatalf("%s rows left for deleted thread = %d, want 0", table, n)
		}

		if n := countRows(t, s, table, "thread_id = ?", survivor.ID); n != 1 {
			t.Fatalf("%s rows for survivor = %d, want 1", table, n)
		}
	}

	if n := countRows(t, s, "translations", ""); n != 1 {
		t.Fatalf("translations left = %d, want 1 (survivor only)", n)
	}

	// Deleting frees the (channel_id, thread_ts) pair for a fresh add.
	if _, err := s.AddThread(ctx, store.Thread{ChannelID: "C1", ThreadTS: "1.1"}); err != nil {
		t.Fatalf("re-add after delete: %v", err)
	}
}

func TestCloseIsReported(t *testing.T) {
	t.Parallel()

	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := s.Version(context.Background()); !errors.Is(err, sql.ErrConnDone) && err == nil {
		t.Fatal("Version after Close: want error, got nil")
	}
}
