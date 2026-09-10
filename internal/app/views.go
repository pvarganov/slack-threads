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
	ID            int64  `json:"id"`
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
		Title:         t.Title,
		ChannelID:     t.ChannelID,
		Workspace:     t.Workspace,
		Permalink:     threadPermalink(t),
		Archived:      t.Archived,
		NeedsRefresh:  t.NeedsRefresh,
		AddedAt:       formatTime(t.AddedAt),
		LastFetchedAt: formatTime(t.LastFetchedAt),
	}
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
			Author:    authorName(m.UserID, user),
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
func userNames(users map[string]store.User) map[string]string {
	names := make(map[string]string, len(users))
	for id, u := range users {
		names[id] = authorName(id, u)
	}

	return names
}

// authorName prefers the handle Slack shows, then the full name, and falls
// back to the raw ID for an author that was never cached.
func authorName(id string, u store.User) string {
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
