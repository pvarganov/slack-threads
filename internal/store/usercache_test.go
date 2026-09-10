package store_test

import (
	"context"
	"testing"

	"github.com/pavelvarganov/slack-threads/internal/slackapi"
	"github.com/pavelvarganov/slack-threads/internal/store"
)

func TestUserCacheRoundTrip(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)
	cache := store.NewUserCache(s)
	ctx := context.Background()

	// Nothing cached yet: the Slack client would have to fetch both IDs.
	missing, err := cache.GetUsers(ctx, []string{"U1", "B1"})
	if err != nil {
		t.Fatalf("GetUsers: %v", err)
	}

	if len(missing) != 0 {
		t.Fatalf("got %+v, want empty cache", missing)
	}

	want := []slackapi.User{
		{ID: "U1", DisplayName: "pavel", RealName: "Pavel Varganov"},
		{ID: "B1", DisplayName: "deploybot", RealName: "deploybot", IsBot: true},
	}
	if err := cache.SaveUsers(ctx, want); err != nil {
		t.Fatalf("SaveUsers: %v", err)
	}

	got, err := cache.GetUsers(ctx, []string{"U1", "B1", "UZZZ"})
	if err != nil {
		t.Fatalf("GetUsers after save: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %+v, want two entries", got)
	}

	for _, u := range want {
		if got[u.ID] != u {
			t.Fatalf("cached %s = %+v, want %+v", u.ID, got[u.ID], u)
		}
	}
}

func TestUserCacheSatisfiesSlackInterface(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)

	var _ slackapi.UserCache = store.NewUserCache(s)
}

func TestUserCacheSaveEmpty(t *testing.T) {
	t.Parallel()

	s, _ := openTemp(t)

	if err := store.NewUserCache(s).SaveUsers(context.Background(), nil); err != nil {
		t.Fatalf("SaveUsers(nil): %v", err)
	}
}
