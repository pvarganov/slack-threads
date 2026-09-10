package app

import (
	"testing"
	"time"

	"github.com/pavelvarganov/slack-threads/internal/store"
	syncsvc "github.com/pavelvarganov/slack-threads/internal/sync"
)

func TestThreadItemFormatsTimes(t *testing.T) {
	item := threadItem(store.Thread{
		ID: 7, ChannelID: "C7", ThreadTS: "1717171717.000200", Workspace: "acme",
		AddedAt: time.Unix(1700000000, 0).UTC(),
	})

	if item.AddedAt != "2023-11-14T22:13:20Z" {
		t.Errorf("addedAt = %q", item.AddedAt)
	}

	if item.LastFetchedAt != "" {
		t.Errorf("never fetched thread got lastFetchedAt = %q", item.LastFetchedAt)
	}
}

func TestThreadPermalinkNeedsAllParts(t *testing.T) {
	if got := threadPermalink(store.Thread{ChannelID: "C1", ThreadTS: "1.2"}); got != "" {
		t.Errorf("permalink without a workspace = %q, want empty", got)
	}
}

func TestAuthorNamePrefersTheHandle(t *testing.T) {
	tests := []struct {
		name string
		user store.User
		want string
	}{
		{name: "handle", user: store.User{DisplayName: "pavel", RealName: "Pavel V"}, want: "pavel"},
		{name: "real name", user: store.User{RealName: "Pavel V"}, want: "Pavel V"},
		{name: "unknown", want: "U9"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := authorName("U9", tc.user); got != tc.want {
				t.Errorf("authorName = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAuthorIDsAreDistinctAndSkipEmpty(t *testing.T) {
	got := authorIDs([]store.Message{
		{UserID: "U1"}, {UserID: ""}, {UserID: "U2"}, {UserID: "U1"},
	})

	if len(got) != 2 || got[0] != "U1" || got[1] != "U2" {
		t.Errorf("authorIDs = %v, want [U1 U2]", got)
	}
}

func TestReactionsTolerateBrokenPayloads(t *testing.T) {
	if got := reactions(""); got != nil {
		t.Errorf("empty payload = %v, want nil", got)
	}

	if got := reactions("{not json"); got != nil {
		t.Errorf("broken payload = %v, want nil", got)
	}

	if got := reactions(`{"text":"hi"}`); got != nil {
		t.Errorf("payload without reactions = %v, want nil", got)
	}
}

func TestTSTime(t *testing.T) {
	if got := tsTime("1717171717.000200"); got.Unix() != 1717171717 {
		t.Errorf("tsTime = %v", got)
	}

	if got := tsTime("не время"); !got.IsZero() {
		t.Errorf("tsTime of garbage = %v, want zero", got)
	}
}

func TestChangedCountSumsTheDelta(t *testing.T) {
	got := changedCount(syncsvc.Result{Inserted: 2, Updated: 1, Deleted: 3, Unchanged: 40})
	if got != 6 {
		t.Errorf("changedCount = %d, want 6", got)
	}
}
