package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pavelvarganov/slack-threads/internal/store"
)

func TestSaveAndGetUser(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()

	updated := time.Date(2026, 9, 10, 8, 0, 0, 0, time.UTC)
	if err := s.SaveUsers(ctx, []store.User{
		{ID: "U1", DisplayName: "pavel", RealName: "Pavel Varganov", UpdatedAt: updated},
		{ID: "B1", DisplayName: "deploybot", IsBot: true, UpdatedAt: updated},
	}); err != nil {
		t.Fatalf("SaveUsers: %v", err)
	}

	got, err := s.GetUser(ctx, "U1")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}

	want := store.User{ID: "U1", DisplayName: "pavel", RealName: "Pavel Varganov", UpdatedAt: updated}
	if got != want {
		t.Fatalf("user = %+v, want %+v", got, want)
	}

	bot, err := s.GetUser(ctx, "B1")
	if err != nil {
		t.Fatalf("GetUser(bot): %v", err)
	}

	if !bot.IsBot {
		t.Fatalf("bot flag = %v, want true", bot.IsBot)
	}
}

func TestGetUserNotFound(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)

	if _, err := s.GetUser(context.Background(), "UZZZ"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetUser(missing) = %v, want ErrNotFound", err)
	}
}

func TestSaveUsersRefreshesCache(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()

	if err := s.SaveUsers(ctx, []store.User{{ID: "U1", DisplayName: "old"}}); err != nil {
		t.Fatalf("SaveUsers: %v", err)
	}

	if err := s.SaveUsers(ctx, []store.User{{ID: "U1", DisplayName: "new", RealName: "Pavel"}}); err != nil {
		t.Fatalf("SaveUsers (refresh): %v", err)
	}

	got, err := s.GetUser(ctx, "U1")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}

	if got.DisplayName != "new" || got.RealName != "Pavel" {
		t.Fatalf("user = %+v, want the refreshed profile", got)
	}

	if got.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt is zero, want a default timestamp")
	}

	if n := countRows(t, s, "users", ""); n != 1 {
		t.Fatalf("users = %d, want 1 row replaced in place", n)
	}
}

func TestSaveUsersErrors(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()

	if err := s.SaveUsers(ctx, nil); err != nil {
		t.Fatalf("SaveUsers(nil): %v", err)
	}

	err := s.SaveUsers(ctx, []store.User{{ID: "U1", DisplayName: "pavel"}, {DisplayName: "без id"}})
	if err == nil {
		t.Fatal("SaveUsers without ID: want error, got nil")
	}

	if n := countRows(t, s, "users", ""); n != 0 {
		t.Fatalf("users = %d, want 0 (transaction rolled back)", n)
	}
}

func TestGetUsers(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	ctx := context.Background()

	if err := s.SaveUsers(ctx, []store.User{
		{ID: "U1", DisplayName: "pavel"},
		{ID: "U2", DisplayName: "alex"},
		{ID: "U3", DisplayName: "kate"},
	}); err != nil {
		t.Fatalf("SaveUsers: %v", err)
	}

	got, err := s.GetUsers(ctx, []string{"U1", "U3", "UZZZ"})
	if err != nil {
		t.Fatalf("GetUsers: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("users = %d, want 2 (unknown IDs are skipped)", len(got))
	}

	if got["U1"].DisplayName != "pavel" || got["U3"].DisplayName != "kate" {
		t.Fatalf("users = %+v, want U1 and U3", got)
	}

	if _, ok := got["UZZZ"]; ok {
		t.Fatal("unknown ID present in the result")
	}
}

func TestGetUsersEmpty(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)

	got, err := s.GetUsers(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetUsers(nil): %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("users = %d, want 0", len(got))
	}
}
