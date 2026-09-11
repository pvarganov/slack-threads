package app

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/pavelvarganov/slack-threads/internal/slackapi"
	"github.com/pavelvarganov/slack-threads/internal/store"
	syncsvc "github.com/pavelvarganov/slack-threads/internal/sync"
)

// ThreadItem is one row of the thread list in the left column.
type ThreadItem struct {
	ID int64 `json:"id"`
	// Title is the Russian first line of the root message when it has
	// been translated; the original line is the fallback.
	Title         string `json:"title"`
	ChannelID     string `json:"channelId"`
	Workspace     string `json:"workspace"`
	Permalink     string `json:"permalink"`
	Archived      bool   `json:"archived"`
	NeedsRefresh  bool   `json:"needsRefresh"`
	AddedAt       string `json:"addedAt"`
	LastFetchedAt string `json:"lastFetchedAt"`
}

// MessageView is one message in the thread feed: the Slack original and
// its Russian translation side by side.
type MessageView struct {
	ID       int64  `json:"id"`
	TS       string `json:"ts"`
	Time     string `json:"time"`
	Author   string `json:"author"`
	AuthorID string `json:"authorId"`
	IsBot    bool   `json:"isBot"`
	Text     string `json:"text"`
	TextRU   string `json:"textRu"`
	// Blocks is the original text rendered into paragraphs, quotes and
	// code blocks, so the frontend never has to parse Slack mrkdwn.
	Blocks []slackapi.Block `json:"blocks"`
	// BlocksRU is the same rendering of the Russian translation.
	BlocksRU  []slackapi.Block `json:"blocksRu"`
	Edited    bool             `json:"edited"`
	Deleted   bool             `json:"deleted"`
	Reactions []ReactionView   `json:"reactions"`
}

// ReactionView is an emoji reaction shown as-is under a message.
type ReactionView struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// DraftView is the reply being composed for a thread.
type DraftView struct {
	ThreadID int64  `json:"threadId"`
	TextRU   string `json:"textRu"`
	TextEN   string `json:"textEn"`
	BackRU   string `json:"backRu"`
}

// ThreadView is everything the right column needs to render a thread.
type ThreadView struct {
	Thread  ThreadItem `json:"thread"`
	Summary string     `json:"summary"`
	// SummaryBlocks is the «Суть» block rendered the same way messages are.
	SummaryBlocks []slackapi.Block `json:"summaryBlocks"`
	Messages      []MessageView    `json:"messages"`
	Draft         DraftView        `json:"draft"`
}

// RefreshOutcome is one thread's result inside RefreshAll. A thread that
// failed carries Error instead of stopping the whole walk.
type RefreshOutcome struct {
	ThreadID int64  `json:"threadId"`
	Title    string `json:"title"`
	Changed  int    `json:"changed"`
	Error    string `json:"error"`
}

// SentView reports a reply that reached Slack.
type SentView struct {
	ThreadID  int64  `json:"threadId"`
	TS        string `json:"ts"`
	Permalink string `json:"permalink"`
}

// threadItem maps a stored thread onto the list row.
func threadItem(t store.Thread) ThreadItem {
	return ThreadItem{
		ID:            t.ID,
		Title:         firstNonEmpty(t.TitleRU, t.Title),
		ChannelID:     t.ChannelID,
		Workspace:     t.Workspace,
		Permalink:     threadPermalink(t),
		Archived:      t.Archived,
		NeedsRefresh:  t.NeedsRefresh,
		AddedAt:       formatTime(t.AddedAt),
		LastFetchedAt: formatTime(t.LastFetchedAt),
	}
}

// titleLimit keeps the left column readable: a long first line is cut,
// not wrapped over the whole sidebar.
const titleLimit = 80

// withRootLine names the thread in the list. The subject the translator
// wrote wins; until it exists — a thread too short to summarise, or one
// added a moment ago — the first translated line of the root message
// stands in, so a row is never blank.
func withRootLine(t store.Thread, rootRU string) ThreadItem {
	item := threadItem(t)

	if t.TitleRU == "" {
		if title := firstLine(rootRU, titleLimit); title != "" {
			item.Title = title
		}
	}

	return item
}

// firstLine is the first non-empty line of the text, shortened to limit
// runes. Leading quote, heading and bullet markers are dropped: in a
// one-line title they are noise. Emphasis is left alone, or "**Итог:**"
// would lose its opening pair and keep the closing one.
func firstLine(text string, limit int) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#>-• "))
		if line == "" {
			continue
		}

		runes := []rune(line)
		if len(runes) > limit {
			return strings.TrimSpace(string(runes[:limit])) + "…"
		}

		return line
	}

	return ""
}

// firstNonEmpty returns the first argument that is not empty.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}

	return ""
}

// draftView maps a stored draft onto its view.
func draftView(d store.Draft) DraftView {
	return DraftView{ThreadID: d.ThreadID, TextRU: d.TextRU, TextEN: d.TextEN, BackRU: d.BackRU}
}

// messageViews joins messages with their translations and author names.
func messageViews(
	msgs []store.Message, translations map[int64]store.Translation, users map[string]store.User,
) []MessageView {
	out := make([]MessageView, 0, len(msgs))
	names := userNames(users)

	for _, m := range msgs {
		user := users[m.UserID]

		out = append(out, MessageView{
			ID:        m.ID,
			TS:        m.TS,
			Time:      formatTime(tsTime(m.TS)),
			Author:    authorName(m.UserID, user, m.RawJSON),
			AuthorID:  m.UserID,
			IsBot:     user.IsBot,
			Text:      m.Text,
			TextRU:    translations[m.ID].TextRU,
			Blocks:    slackapi.RenderMrkdwn(m.Text, names),
			BlocksRU:  slackapi.RenderMrkdwn(translations[m.ID].TextRU, names),
			Edited:    m.EditedTS != "",
			Deleted:   m.Deleted,
			Reactions: reactions(m.RawJSON),
		})
	}

	return out
}

// userNames maps Slack IDs onto the labels mentions are rendered with.
// A mention shows the profile name, which is what Slack itself renders
// there, so the per-message label plays no part.
func userNames(users map[string]store.User) map[string]string {
	names := make(map[string]string, len(users))
	for id, u := range users {
		names[id] = profileName(id, u)
	}

	return names
}

// authorName is the name over the message. A human keeps their profile
// name even when the payload carries an app label: a message sent with a
// user token of some app (this one included) is shown by Slack as coming
// from the person, and it carries the app's bot_profile all the same. The
// label only wins for a bot author, which posts under its own name while
// the bot user behind it may carry an unrelated display name.
func authorName(id string, u store.User, raw string) string {
	if human(id, u) {
		return profileName(id, u)
	}

	if label := messageLabel(raw); label != "" {
		return label
	}

	return profileName(id, u)
}

// human reports an author known to be a person: a cached profile that is
// not a bot. An author missing from the cache is not assumed either way.
func human(id string, u store.User) bool {
	return id != "" && !u.IsBot && (u.DisplayName != "" || u.RealName != "")
}

// messageLabel reads the name Slack put on the message itself: the
// username a bot posted under, otherwise the app name from bot_profile.
func messageLabel(raw string) string {
	if raw == "" {
		return ""
	}

	var payload struct {
		Username   string `json:"username"`
		BotProfile *struct {
			Name string `json:"name"`
		} `json:"bot_profile"`
	}

	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}

	if payload.Username != "" {
		return payload.Username
	}

	if payload.BotProfile != nil {
		return payload.BotProfile.Name
	}

	return ""
}

// profileName prefers the handle Slack shows, then the full name, and
// falls back to the raw ID for an author that was never cached.
func profileName(id string, u store.User) string {
	switch {
	case u.DisplayName != "":
		return u.DisplayName
	case u.RealName != "":
		return u.RealName
	}

	return id
}

// authorIDs lists the distinct authors of the messages, for one batched
// lookup in the user cache.
func authorIDs(msgs []store.Message) []string {
	seen := make(map[string]struct{}, len(msgs))
	out := make([]string, 0, len(msgs))

	for _, m := range msgs {
		if m.UserID == "" {
			continue
		}

		if _, ok := seen[m.UserID]; ok {
			continue
		}

		seen[m.UserID] = struct{}{}

		out = append(out, m.UserID)
	}

	return out
}

// reactions pulls the emoji reactions out of the stored Slack payload.
// A payload that will not parse simply carries no reactions: the feed must
// render regardless.
func reactions(raw string) []ReactionView {
	if raw == "" {
		return nil
	}

	var payload struct {
		Reactions []struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"reactions"`
	}

	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil
	}

	if len(payload.Reactions) == 0 {
		return nil
	}

	out := make([]ReactionView, 0, len(payload.Reactions))
	for _, r := range payload.Reactions {
		out = append(out, ReactionView{Name: r.Name, Count: r.Count})
	}

	return out
}

// changedCount is how much a sync actually moved, which is what the
// "Обновить все" report shows per thread.
func changedCount(res syncsvc.Result) int {
	return res.Inserted + res.Updated + res.Deleted
}

// threadPermalink rebuilds the Slack link of the thread root, so the UI
// can offer «Открыть в Slack». Threads added from an app.slack.com/client/...
// link carry a team ID instead of a workspace subdomain (see
// permalink.Link), so the link is rebuilt in that same shape for them.
func threadPermalink(t store.Thread) string {
	if t.ChannelID == "" || t.ThreadTS == "" {
		return ""
	}

	switch {
	case t.Workspace != "":
		return "https://" + t.Workspace + ".slack.com/archives/" + t.ChannelID + "/p" + compactTS(t.ThreadTS)
	case t.TeamID != "":
		return "https://app.slack.com/client/" + t.TeamID + "/" + t.ChannelID +
			"/thread/" + t.ChannelID + "-" + t.ThreadTS
	default:
		return ""
	}
}

// compactTS turns "1717171717.000200" into the "p"-form Slack uses in
// permalinks.
func compactTS(ts string) string {
	out := make([]rune, 0, len(ts))

	for _, r := range ts {
		if r != '.' {
			out = append(out, r)
		}
	}

	return string(out)
}

// formatTime renders a timestamp for the UI, leaving a zero time empty.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339)
}

// tsTime converts a Slack timestamp ("1717171717.000200") into a time,
// returning the zero time when it is not a timestamp at all.
func tsTime(ts string) time.Time {
	whole, _, _ := strings.Cut(ts, ".")

	secs, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return time.Time{}
	}

	return time.Unix(secs, 0).UTC()
}
