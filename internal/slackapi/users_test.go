package slackapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pavelvarganov/slack-threads/internal/slackapi"
)

// fakeCache is an in-memory slackapi.UserCache that counts its calls.
type fakeCache struct {
	users  map[string]slackapi.User
	reads  int
	writes int
	err    error
}

func newFakeCache() *fakeCache {
	return &fakeCache{users: map[string]slackapi.User{}}
}

func (f *fakeCache) GetUsers(_ context.Context, ids []string) (map[string]slackapi.User, error) {
	f.reads++

	if f.err != nil {
		return nil, f.err
	}

	out := map[string]slackapi.User{}

	for _, id := range ids {
		if u, ok := f.users[id]; ok {
			out[id] = u
		}
	}

	return out, nil
}

func (f *fakeCache) SaveUsers(_ context.Context, users []slackapi.User) error {
	f.writes++

	for _, u := range users {
		f.users[u.ID] = u
	}

	return nil
}

// userServer answers users.info/bots.info from the given per-ID bodies and
// records how many requests each ID got.
func userServer(t *testing.T, bodies map[string]string) (*httptest.Server, map[string]int) {
	t.Helper()

	hits := map[string]int{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("user")
		if id == "" {
			id = r.URL.Query().Get("bot")
		}

		hits[id]++

		body, ok := bodies[id]
		if !ok {
			body = `{"ok":false,"error":"user_not_found"}`
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body)) //nolint:errcheck // test server
	}))
	t.Cleanup(srv.Close)

	return srv, hits
}

func TestResolveUsersFetchesAndCaches(t *testing.T) {
	t.Parallel()

	srv, hits := userServer(t, map[string]string{
		"U1": `{"ok":true,"user":{"id":"U1","name":"pavel","real_name":"Pavel Varganov",
			"profile":{"display_name":"pavel","real_name":"Pavel Varganov"}}}`,
		"B9": `{"ok":true,"bot":{"id":"B9","name":"deploybot","app_id":"A1"}}`,
	})

	cache := newFakeCache()
	client, _ := newClient(t, srv, slackapi.WithUserCache(cache))
	ctx := context.Background()

	got, err := client.ResolveUsers(ctx, []string{"U1", "B9", "U1", ""})
	if err != nil {
		t.Fatalf("ResolveUsers: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("resolved %d users, want 2: %+v", len(got), got)
	}

	if got["U1"].Name() != "pavel" || got["U1"].RealName != "Pavel Varganov" {
		t.Fatalf("U1 = %+v", got["U1"])
	}

	if !got["B9"].IsBot || got["B9"].Name() != "deploybot" {
		t.Fatalf("B9 = %+v", got["B9"])
	}

	if hits["U1"] != 1 || hits["B9"] != 1 {
		t.Fatalf("hits = %v, want one per id", hits)
	}

	if cache.writes != 1 {
		t.Fatalf("cache writes = %d, want 1", cache.writes)
	}
}

func TestResolveUsersSecondCallHitsCacheOnly(t *testing.T) {
	t.Parallel()

	srv, hits := userServer(t, map[string]string{
		"U1": `{"ok":true,"user":{"id":"U1","name":"pavel","profile":{"display_name":"pavel"}}}`,
	})

	cache := newFakeCache()
	client, _ := newClient(t, srv, slackapi.WithUserCache(cache))
	ctx := context.Background()

	if _, err := client.ResolveUsers(ctx, []string{"U1"}); err != nil {
		t.Fatalf("first ResolveUsers: %v", err)
	}

	got, err := client.ResolveUsers(ctx, []string{"U1"})
	if err != nil {
		t.Fatalf("second ResolveUsers: %v", err)
	}

	if got["U1"].Name() != "pavel" {
		t.Fatalf("U1 = %+v", got["U1"])
	}

	if hits["U1"] != 1 {
		t.Fatalf("network hits = %d, want 1 (second call must come from cache)", hits["U1"])
	}

	if cache.writes != 1 {
		t.Fatalf("cache writes = %d, want 1", cache.writes)
	}
}

func TestResolveUsersSkipsUnknownIDs(t *testing.T) {
	t.Parallel()

	srv, _ := userServer(t, map[string]string{
		"U1":     `{"ok":true,"user":{"id":"U1","name":"pavel","profile":{"display_name":"pavel"}}}`,
		"UGONE":  `{"ok":false,"error":"user_not_found"}`,
		"BGONE":  `{"ok":false,"error":"bot_not_found"}`,
		"UOTHER": `{"ok":true,"user":{"id":"UOTHER","name":"other"}}`,
	})

	client, _ := newClient(t, srv, slackapi.WithUserCache(newFakeCache()))

	got, err := client.ResolveUsers(context.Background(), []string{"UGONE", "U1", "BGONE", "UOTHER"})
	if err != nil {
		t.Fatalf("ResolveUsers must not fail on unknown ids: %v", err)
	}

	if _, ok := got["UGONE"]; ok {
		t.Fatalf("deleted user must be absent: %+v", got)
	}

	if _, ok := got["BGONE"]; ok {
		t.Fatalf("uninstalled bot must be absent: %+v", got)
	}

	if got["U1"].Name() != "pavel" || got["UOTHER"].Name() != "other" {
		t.Fatalf("resolvable users lost: %+v", got)
	}
}

func TestResolveUsersPropagatesRealErrors(t *testing.T) {
	t.Parallel()

	srv, _ := userServer(t, map[string]string{
		"U1": `{"ok":false,"error":"invalid_auth"}`,
	})

	client, _ := newClient(t, srv, slackapi.WithUserCache(newFakeCache()))

	if _, err := client.ResolveUsers(context.Background(), []string{"U1"}); !errors.Is(err, slackapi.ErrInvalidAuth) {
		t.Fatalf("err = %v, want ErrInvalidAuth", err)
	}
}

func TestResolveUsersEmptyInput(t *testing.T) {
	t.Parallel()

	cache := newFakeCache()
	srv, _ := userServer(t, nil)
	client, _ := newClient(t, srv, slackapi.WithUserCache(cache))

	got, err := client.ResolveUsers(context.Background(), []string{"", ""})
	if err != nil {
		t.Fatalf("ResolveUsers: %v", err)
	}

	if len(got) != 0 {
		t.Fatalf("got %+v, want empty", got)
	}

	if cache.reads != 0 {
		t.Fatalf("cache reads = %d, want 0", cache.reads)
	}
}

func TestResolveUsersWithoutCacheStillWorks(t *testing.T) {
	t.Parallel()

	srv, hits := userServer(t, map[string]string{
		"U1": `{"ok":true,"user":{"id":"U1","name":"pavel","is_bot":false,"profile":{"display_name":""}}}`,
	})

	client, _ := newClient(t, srv)

	got, err := client.ResolveUsers(context.Background(), []string{"U1"})
	if err != nil {
		t.Fatalf("ResolveUsers: %v", err)
	}

	if got["U1"].Name() != "pavel" {
		t.Fatalf("U1 = %+v", got["U1"])
	}

	if hits["U1"] != 1 {
		t.Fatalf("hits = %d, want 1", hits["U1"])
	}
}

func TestUserName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		user slackapi.User
		want string
	}{
		{"display name wins", slackapi.User{ID: "U1", DisplayName: "pavel", RealName: "Pavel"}, "pavel"},
		{"real name fallback", slackapi.User{ID: "U1", RealName: "Pavel"}, "Pavel"},
		{"id as last resort", slackapi.User{ID: "U1"}, "U1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.user.Name(); got != tc.want {
				t.Fatalf("Name() = %q, want %q", got, tc.want)
			}
		})
	}
}
