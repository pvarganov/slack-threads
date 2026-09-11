package app

import (
	"strings"
	"testing"
	"time"

	"github.com/pavelvarganov/slack-threads/internal/slackapi"
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
		t.Errorf("permalink without a workspace or team = %q, want empty", got)
	}
}

// TestThreadPermalinkFallsBackToTeamID covers threads added from an
// app.slack.com/client/... link, which carries a team ID instead of a
// workspace subdomain (see permalink.Link).
func TestThreadPermalinkFallsBackToTeamID(t *testing.T) {
	got := threadPermalink(store.Thread{ChannelID: "C1", ThreadTS: "1.2", TeamID: "T1"})
	want := "https://app.slack.com/client/T1/C1/thread/C1-1.2"

	if got != want {
		t.Errorf("permalink = %q, want %q", got, want)
	}
}

func TestThreadPermalinkPrefersWorkspaceOverTeamID(t *testing.T) {
	got := threadPermalink(store.Thread{ChannelID: "C1", ThreadTS: "1.2", Workspace: "acme", TeamID: "T1"})
	want := "https://acme.slack.com/archives/C1/p12"

	if got != want {
		t.Errorf("permalink = %q, want %q", got, want)
	}
}

func TestAuthorNamePrefersTheHandle(t *testing.T) {
	tests := []struct {
		name string
		user store.User
		raw  string
		want string
	}{
		{name: "handle", user: store.User{DisplayName: "pavel", RealName: "Pavel V"}, want: "pavel"},
		{name: "real name", user: store.User{RealName: "Pavel V"}, want: "Pavel V"},
		{name: "unknown", want: "U9"},
		{
			// The app's own label beats the profile of the bot user
			// behind it: Slack shows exactly this over the message.
			name: "bot_profile beats the profile",
			user: store.User{DisplayName: "davidtam", RealName: "Maia (TAM)"},
			raw:  `{"bot_profile":{"name":"Maia (TAM)"}}`,
			want: "Maia (TAM)",
		},
		{
			name: "username beats bot_profile",
			raw:  `{"username":"Maia (TAM)","bot_profile":{"name":"Pylon"}}`,
			want: "Maia (TAM)",
		},
		{
			// A bot Slack refuses to resolve used to show as a raw ID.
			name: "unresolved bot keeps its label",
			raw:  `{"username":"Maia (TAM)"}`,
			want: "Maia (TAM)",
		},
		{name: "broken payload falls back to the profile", raw: "{oops", user: store.User{DisplayName: "pavel"}, want: "pavel"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := authorName("U9", tc.user, tc.raw); got != tc.want {
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

func TestMessageViewsRenderBlocksForBothLanguages(t *testing.T) {
	msgs := []store.Message{{ID: 1, TS: "1717171717.000100", UserID: "U1", Text: "hi <@U2>\n```go\nx := 1\n```"}}
	translations := map[int64]store.Translation{1: {MessageID: 1, TextRU: "привет `код`"}}
	users := map[string]store.User{
		"U1": {ID: "U1", DisplayName: "alice"},
		"U2": {ID: "U2", RealName: "Bob Smith"},
	}

	got := messageViews(msgs, translations, users)
	if len(got) != 1 {
		t.Fatalf("views = %d, want 1", len(got))
	}

	view := got[0]
	if len(view.Blocks) != 2 {
		t.Fatalf("blocks = %d, want a paragraph and a code block", len(view.Blocks))
	}

	if view.Blocks[1].Kind != slackapi.BlockCode || view.Blocks[1].Lang != "go" {
		t.Errorf("second block = %+v, want a go code block", view.Blocks[1])
	}

	// The mention has to carry the resolved name, not the bare ID: that is
	// the whole point of passing the user cache into the renderer.
	var mention string

	for _, span := range view.Blocks[0].Spans {
		if span.Kind == slackapi.SpanUser {
			mention = span.Text
		}
	}

	if mention != "@Bob Smith" {
		t.Errorf("mention rendered as %q, want the resolved name", mention)
	}

	if len(view.BlocksRU) != 1 || len(view.BlocksRU[0].Spans) == 0 {
		t.Errorf("translation blocks = %+v, want one rendered paragraph", view.BlocksRU)
	}
}

func TestMessageViewsWithoutTranslationHaveNoRussianBlocks(t *testing.T) {
	got := messageViews(
		[]store.Message{{ID: 1, TS: "1717171717.000100", UserID: "U1", Text: "hi"}},
		nil, nil,
	)

	if len(got[0].BlocksRU) != 0 {
		t.Errorf("blocksRu = %+v, want none for an untranslated message", got[0].BlocksRU)
	}

	if got[0].Author != "U1" {
		t.Errorf("author = %q, want the raw ID fallback", got[0].Author)
	}
}

func TestWithRootLineNamesTheThread(t *testing.T) {
	tests := []struct {
		name   string
		thread store.Thread
		rootRU string
		want   string
	}{
		{
			name:   "the generated subject wins",
			thread: store.Thread{ID: 1, Title: "Card payment errors", TitleRU: "Ошибка CVV в Ecommpay"},
			rootRU: "Видим много ошибок оплаты картой",
			want:   "Ошибка CVV в Ecommpay",
		},
		{
			name:   "root line stands in until the subject exists",
			thread: store.Thread{ID: 2, Title: "Card payment errors"},
			rootRU: "Видим много ошибок оплаты картой\nвторой строкой подробности",
			want:   "Видим много ошибок оплаты картой",
		},
		{
			name:   "untranslated thread keeps the Slack line",
			thread: store.Thread{ID: 3, Title: "Deploy is stuck"},
			want:   "Deploy is stuck",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := withRootLine(tc.thread, tc.rootRU).Title; got != tc.want {
				t.Errorf("Title = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFirstLineTrimsMarkdownAndLength(t *testing.T) {
	if got := firstLine("\n\n> **Итог:** всё чинится", 80); got != "**Итог:** всё чинится" {
		t.Errorf("firstLine = %q", got)
	}

	long := strings.Repeat("я", 200)
	got := firstLine(long, 80)

	if len([]rune(got)) != 81 || !strings.HasSuffix(got, "…") {
		t.Errorf("firstLine length = %d, want 80 runes plus the ellipsis", len([]rune(got)))
	}
}
