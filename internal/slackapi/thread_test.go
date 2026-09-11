package slackapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pavelvarganov/slack-threads/internal/slackapi"
)

// threadFixture is a recorded conversations.replies page covering every field
// the mapper reads: a plain reply, an edited message with reactions, a bot
// message and a join event.
const threadFixture = `{
  "ok": true,
  "has_more": false,
  "messages": [
    {
      "type": "message",
      "user": "U01PARENT",
      "ts": "1788872615.903009",
      "text": "Deploy is stuck on <#C0123|infra>",
      "thread_ts": "1788872615.903009",
      "reply_count": 3
    },
    {
      "type": "message",
      "user": "U02REPLY",
      "ts": "1788872700.100100",
      "thread_ts": "1788872615.903009",
      "text": "Rolled back, see <https://example.com|the run>",
      "edited": {"user": "U02REPLY", "ts": "1788872750.000000"},
      "reactions": [
        {"name": "eyes", "count": 2, "users": ["U01PARENT", "U03BOT"]},
        {"name": "+1", "count": 1, "users": ["U01PARENT"]}
      ]
    },
    {
      "type": "message",
      "subtype": "bot_message",
      "bot_id": "B0PIPELINE",
      "username": "CI",
      "ts": "1788872800.200200",
      "thread_ts": "1788872615.903009",
      "text": "build #42 failed"
    },
    {
      "type": "message",
      "subtype": "channel_join",
      "user": "U04LATE",
      "ts": "1788872900.300300",
      "thread_ts": "1788872615.903009",
      "text": "<@U04LATE> has joined the channel"
    }
  ]
}`

func TestFetchThreadMapsMessageFields(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(threadFixture))
	}))
	defer srv.Close()

	client, _ := newClient(t, srv)

	msgs, err := client.FetchThread(context.Background(), "C0123", "1788872615.903009")
	if err != nil {
		t.Fatalf("FetchThread: %v", err)
	}

	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want 4", len(msgs))
	}

	parent := msgs[0]
	if parent.TS != "1788872615.903009" || parent.User != "U01PARENT" {
		t.Errorf("parent identity = %q/%q", parent.TS, parent.User)
	}

	if parent.Text != "Deploy is stuck on <#C0123|infra>" {
		t.Errorf("parent text mangled: %q", parent.Text)
	}

	if parent.EditedTS != "" || parent.Subtype != "" || len(parent.Reactions) != 0 {
		t.Errorf("parent picked up fields it does not have: %+v", parent)
	}

	if parent.Author() != "U01PARENT" {
		t.Errorf("Author = %q, want the user ID", parent.Author())
	}

	edited := msgs[1]
	if edited.EditedTS != "1788872750.000000" {
		t.Errorf("EditedTS = %q, want the edit timestamp", edited.EditedTS)
	}

	if edited.ThreadTS != "1788872615.903009" {
		t.Errorf("ThreadTS = %q, want the parent ts", edited.ThreadTS)
	}

	wantReactions := []slackapi.Reaction{
		{Name: "eyes", Count: 2, Users: []string{"U01PARENT", "U03BOT"}},
		{Name: "+1", Count: 1, Users: []string{"U01PARENT"}},
	}

	if len(edited.Reactions) != len(wantReactions) {
		t.Fatalf("got %d reactions, want %d", len(edited.Reactions), len(wantReactions))
	}

	for i, want := range wantReactions {
		got := edited.Reactions[i]
		if got.Name != want.Name || got.Count != want.Count || len(got.Users) != len(want.Users) {
			t.Errorf("reaction %d = %+v, want %+v", i, got, want)
		}
	}

	bot := msgs[2]
	if bot.User != "" || bot.BotID != "B0PIPELINE" || bot.Username != "CI" {
		t.Errorf("bot message identity = %+v", bot)
	}

	if bot.Subtype != "bot_message" {
		t.Errorf("Subtype = %q, want bot_message", bot.Subtype)
	}

	if bot.Author() != "B0PIPELINE" {
		t.Errorf("Author = %q, want the bot ID", bot.Author())
	}

	join := msgs[3]
	if join.Subtype != "channel_join" {
		t.Errorf("Subtype = %q, want channel_join", join.Subtype)
	}
}

func TestFetchThreadKeepsRawPayload(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(threadFixture))
	}))
	defer srv.Close()

	client, _ := newClient(t, srv)

	msgs, err := client.FetchThread(context.Background(), "C0123", "1788872615.903009")
	if err != nil {
		t.Fatalf("FetchThread: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(msgs[0].Raw, &raw); err != nil {
		t.Fatalf("Raw is not valid JSON: %v", err)
	}

	// reply_count is not mapped onto Message, so it can only come from Raw.
	if raw["reply_count"] != float64(3) {
		t.Errorf("Raw lost fields the mapper ignores: %v", raw["reply_count"])
	}
}

func TestFetchThreadRequestsConfiguredPageLimit(t *testing.T) {
	t.Parallel()

	var gotLimit string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLimit = r.URL.Query().Get("limit")

		w.Write([]byte(`{"ok": true, "messages": []}`))
	}))
	defer srv.Close()

	client, _ := newClient(t, srv, slackapi.WithPageLimit(15))

	if _, err := client.FetchThread(context.Background(), "C0123", "1.1"); err != nil {
		t.Fatalf("FetchThread: %v", err)
	}

	if gotLimit != "15" {
		t.Errorf("limit = %q, want 15", gotLimit)
	}
}

// clientIsAClient keeps HTTPClient assignable to the Client interface the
// rest of the app depends on.
var _ slackapi.Client = (*slackapi.HTTPClient)(nil)

// An app posting under its own user carries bot_profile: Slack labels the
// message with that name, not with the bot user's profile.
func TestFetchThreadReadsBotProfileName(t *testing.T) {
	t.Parallel()

	const fixture = `{"ok":true,"messages":[
      {"ts":"1.0","user":"U0APP","bot_id":"B0APP","bot_profile":{"name":"Maia (TAM)"},"text":"hi"}
    ]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(fixture))
	}))
	defer srv.Close()

	client, _ := newClient(t, srv)

	msgs, err := client.FetchThread(context.Background(), "C0123", "1.0")
	if err != nil {
		t.Fatalf("FetchThread: %v", err)
	}

	if msgs[0].BotName != "Maia (TAM)" {
		t.Errorf("BotName = %q, want the bot_profile name", msgs[0].BotName)
	}

	if msgs[0].Label() != "Maia (TAM)" {
		t.Errorf("Label = %q, want the bot_profile name", msgs[0].Label())
	}
}

func TestMessageLabelPrefersUsername(t *testing.T) {
	t.Parallel()

	m := slackapi.Message{Username: "Maia (TAM)", BotName: "Pylon"}

	if m.Label() != "Maia (TAM)" {
		t.Errorf("Label = %q, want the username the bot posted under", m.Label())
	}

	if (slackapi.Message{}).Label() != "" {
		t.Error("an ordinary message must carry no label")
	}
}
