package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pavelvarganov/slack-threads/internal/config"
	"github.com/pavelvarganov/slack-threads/internal/permalink"
	"github.com/pavelvarganov/slack-threads/internal/slackapi"
	"github.com/pavelvarganov/slack-threads/internal/store"
	syncsvc "github.com/pavelvarganov/slack-threads/internal/sync"
)

// newTestApp builds an App over the two fakes, with the keychain wired to
// a working token so nothing tries to reach Slack.
func newTestApp(t *testing.T, st *fakeStorage, sy *fakeSyncer) *App {
	t.Helper()

	return New(
		WithTokenStore(&fakeStore{token: "xoxp-good"}),
		WithTokenChecker(okChecker(nil)),
		WithStorage(st),
		WithSyncer(sy),
	)
}

// seedThread stores one thread with a message, its translation and a
// summary, which is what most view assertions need.
func seedThread(st *fakeStorage) {
	st.add(store.Thread{
		ID: 1, ChannelID: "C1", ThreadTS: "1717171717.000200", Workspace: "acme",
		Title: "Deploy", AddedAt: time.Unix(1700000000, 0).UTC(),
	})
	st.messages[1] = []store.Message{
		{
			ID: 10, ThreadID: 1, TS: "1717171717.000200", UserID: "U1",
			Text: "we ship today", RawJSON: `{"reactions":[{"name":"eyes","count":2}]}`,
		},
		{ID: 11, ThreadID: 1, TS: "1717171800.000100", UserID: "B1", Text: "build ok", EditedTS: "1717171900.000000"},
	}
	st.translations[1] = map[int64]store.Translation{
		10: {MessageID: 10, TextRU: "выкатываем сегодня"},
	}
	st.summaries[1] = store.Summary{ThreadID: 1, TextRU: "О релизе"}
	st.users["U1"] = store.User{ID: "U1", DisplayName: "pavel"}
	st.users["B1"] = store.User{ID: "B1", RealName: "CI Bot", IsBot: true}
}

func TestAddThreadReturnsRenderedThread(t *testing.T) {
	st, sy := newFakeStorage(), newFakeSyncer()
	seedThread(st)

	sy.addResult = syncsvc.Result{Thread: store.Thread{ID: 1}}

	a := newTestApp(t, st, sy)

	view, err := a.AddThread("https://acme.slack.com/archives/C1/p1717171717000200")
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	if sy.addURL == "" {
		t.Error("AddThread did not reach the sync service")
	}

	if view.Thread.ID != 1 || view.Thread.Title != "Deploy" {
		t.Errorf("thread = %+v", view.Thread)
	}

	if view.Summary != "О релизе" {
		t.Errorf("summary = %q", view.Summary)
	}

	if len(view.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(view.Messages))
	}

	first := view.Messages[0]
	if first.Author != "pavel" || first.TextRU != "выкатываем сегодня" || first.Text != "we ship today" {
		t.Errorf("first message = %+v", first)
	}

	if len(first.Reactions) != 1 || first.Reactions[0].Name != "eyes" || first.Reactions[0].Count != 2 {
		t.Errorf("reactions = %+v", first.Reactions)
	}

	second := view.Messages[1]
	if second.Author != "CI Bot" || !second.IsBot || !second.Edited {
		t.Errorf("second message = %+v", second)
	}

	if second.TextRU != "" {
		t.Errorf("untranslated message got a translation: %q", second.TextRU)
	}
}

func TestAddThreadReportsDomainErrorInRussian(t *testing.T) {
	st, sy := newFakeStorage(), newFakeSyncer()
	sy.addErr = permalink.ErrInvalid

	_, err := newTestApp(t, st, sy).AddThread("nonsense")
	if err == nil {
		t.Fatal("AddThread accepted a broken link")
	}

	if strings.Contains(err.Error(), "permalink:") {
		t.Errorf("raw error text leaked to the frontend: %q", err)
	}

	if !strings.Contains(err.Error(), "ссылк") {
		t.Errorf("message = %q, want an explanation about the link", err)
	}

	if !errors.Is(err, permalink.ErrInvalid) {
		t.Error("UserError lost the cause")
	}
}

func TestListThreadsIncludesArchived(t *testing.T) {
	st, sy := newFakeStorage(), newFakeSyncer()
	seedThread(st)
	st.add(store.Thread{ID: 2, ChannelID: "C2", ThreadTS: "1.2", Archived: true, NeedsRefresh: true})

	items, err := newTestApp(t, st, sy).ListThreads()
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}

	if !items[1].Archived || !items[1].NeedsRefresh {
		t.Errorf("archived thread = %+v", items[1])
	}

	want := "https://acme.slack.com/archives/C1/p1717171717000200"
	if items[0].Permalink != want {
		t.Errorf("permalink = %q, want %q", items[0].Permalink, want)
	}
}

func TestListThreadsReportsStoreFailure(t *testing.T) {
	st, sy := newFakeStorage(), newFakeSyncer()
	st.listErr = errStub

	if _, err := newTestApp(t, st, sy).ListThreads(); err == nil {
		t.Fatal("ListThreads hid a store failure")
	}
}

func TestGetThreadReturnsDraft(t *testing.T) {
	st, sy := newFakeStorage(), newFakeSyncer()
	seedThread(st)

	sy.stored = store.Draft{TextRU: "привет", TextEN: "hi", BackRU: "привет"}

	view, err := newTestApp(t, st, sy).GetThread(1)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}

	if view.Draft.TextEN != "hi" || view.Draft.ThreadID != 1 {
		t.Errorf("draft = %+v", view.Draft)
	}
}

func TestGetThreadWithoutSummaryStillRenders(t *testing.T) {
	st, sy := newFakeStorage(), newFakeSyncer()
	seedThread(st)
	delete(st.summaries, 1)

	view, err := newTestApp(t, st, sy).GetThread(1)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}

	if view.Summary != "" {
		t.Errorf("summary = %q, want empty", view.Summary)
	}
}

func TestGetThreadUnknownID(t *testing.T) {
	st, sy := newFakeStorage(), newFakeSyncer()

	_, err := newTestApp(t, st, sy).GetThread(42)
	if err == nil {
		t.Fatal("GetThread invented a thread")
	}

	if !strings.Contains(err.Error(), "не найден") {
		t.Errorf("message = %q", err)
	}
}

func TestRefreshThreadSyncsAndReturnsFreshView(t *testing.T) {
	st, sy := newFakeStorage(), newFakeSyncer()
	seedThread(st)

	view, err := newTestApp(t, st, sy).RefreshThread(1)
	if err != nil {
		t.Fatalf("RefreshThread: %v", err)
	}

	if got := sy.refreshedIDs(); len(got) != 1 || got[0] != 1 {
		t.Errorf("refreshed = %v, want [1]", got)
	}

	if view.Thread.ID != 1 {
		t.Errorf("view = %+v", view.Thread)
	}
}

func TestRefreshThreadTranslatesSlackFailure(t *testing.T) {
	st, sy := newFakeStorage(), newFakeSyncer()
	seedThread(st)

	sy.refreshErr = &slackapi.APIError{Method: "conversations.replies", Code: "not_in_channel"}

	_, err := newTestApp(t, st, sy).RefreshThread(1)
	if err == nil {
		t.Fatal("RefreshThread hid the Slack failure")
	}

	if !strings.Contains(err.Error(), "канал") {
		t.Errorf("message = %q, want an explanation about channel membership", err)
	}

	var ue *UserError
	if !errors.As(err, &ue) || !ue.Fixable {
		t.Errorf("error = %#v, want a fixable UserError", err)
	}
}

func TestRefreshAllReportsEveryThread(t *testing.T) {
	st, sy := newFakeStorage(), newFakeSyncer()
	sy.all = []syncsvc.ThreadResult{
		{ThreadID: 1, Result: syncsvc.Result{Thread: store.Thread{ID: 1, Title: "Deploy"}, Inserted: 2, Updated: 1}},
		{ThreadID: 2, Err: slackapi.ErrThreadNotFound},
	}

	out, err := newTestApp(t, st, sy).RefreshAll()
	if err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}

	if len(out) != 2 {
		t.Fatalf("outcomes = %d, want 2", len(out))
	}

	if out[0].Changed != 3 || out[0].Title != "Deploy" || out[0].Error != "" {
		t.Errorf("first outcome = %+v", out[0])
	}

	if !strings.Contains(out[1].Error, "удал") {
		t.Errorf("second outcome error = %q, want the deleted-thread message", out[1].Error)
	}
}

func TestDeleteThread(t *testing.T) {
	st, sy := newFakeStorage(), newFakeSyncer()
	seedThread(st)

	a := newTestApp(t, st, sy)
	if err := a.DeleteThread(1); err != nil {
		t.Fatalf("DeleteThread: %v", err)
	}

	if len(st.deleted) != 1 || st.deleted[0] != 1 {
		t.Errorf("deleted = %v", st.deleted)
	}

	if err := a.DeleteThread(1); err == nil {
		t.Error("deleting a gone thread reported success")
	}
}

func TestArchiveThread(t *testing.T) {
	st, sy := newFakeStorage(), newFakeSyncer()
	seedThread(st)

	a := newTestApp(t, st, sy)
	if err := a.ArchiveThread(1, true); err != nil {
		t.Fatalf("ArchiveThread: %v", err)
	}

	if !st.archived[1] {
		t.Error("thread was not archived")
	}

	if err := a.ArchiveThread(1, false); err != nil || st.archived[1] {
		t.Errorf("unarchive failed: err=%v archived=%v", err, st.archived[1])
	}
}

func TestDraftReply(t *testing.T) {
	st, sy := newFakeStorage(), newFakeSyncer()
	sy.draft = store.Draft{TextRU: "привет", TextEN: "hi there", BackRU: "привет"}

	draft, err := newTestApp(t, st, sy).DraftReply(1, "привет")
	if err != nil {
		t.Fatalf("DraftReply: %v", err)
	}

	if draft.TextEN != "hi there" || draft.ThreadID != 1 {
		t.Errorf("draft = %+v", draft)
	}

	if sy.draftRU != "привет" {
		t.Errorf("russian text reached the service as %q", sy.draftRU)
	}
}

func TestDraftReplyEmptyText(t *testing.T) {
	st, sy := newFakeStorage(), newFakeSyncer()
	sy.draftErr = syncsvc.ErrEmptyReply

	_, err := newTestApp(t, st, sy).DraftReply(1, "  ")
	if err == nil {
		t.Fatal("DraftReply accepted an empty text")
	}

	if !strings.Contains(err.Error(), "пуст") {
		t.Errorf("message = %q", err)
	}
}

func TestSendReply(t *testing.T) {
	st, sy := newFakeStorage(), newFakeSyncer()
	sy.sent = syncsvc.Sent{TS: "1717171999.000100", Permalink: "https://acme.slack.com/archives/C1/p1717171999000100"}

	got, err := newTestApp(t, st, sy).SendReply(1, "hi there")
	if err != nil {
		t.Fatalf("SendReply: %v", err)
	}

	if got.TS != "1717171999.000100" || got.ThreadID != 1 || got.Permalink == "" {
		t.Errorf("sent = %+v", got)
	}

	if sy.sentEN != "hi there" {
		t.Errorf("english text reached the service as %q", sy.sentEN)
	}
}

func TestSendReplyRestrictedChannel(t *testing.T) {
	st, sy := newFakeStorage(), newFakeSyncer()
	sy.sentErr = &slackapi.RestrictedError{APIError: &slackapi.APIError{Method: "chat.postMessage", Code: "slack_connect_file_upload_sharing_blocked"}}

	_, err := newTestApp(t, st, sy).SendReply(1, "hi")
	if err == nil {
		t.Fatal("SendReply hid the refusal")
	}

	if !strings.Contains(err.Error(), "Slack Connect") {
		t.Errorf("message = %q", err)
	}
}

func TestBindingsWithoutDependenciesExplainThemselves(t *testing.T) {
	a := New(WithTokenStore(&fakeStore{}))

	if _, err := a.AddThread("x"); err == nil || !strings.Contains(err.Error(), "не готово") {
		t.Errorf("AddThread = %v", err)
	}

	if _, err := a.ListThreads(); err == nil || !strings.Contains(err.Error(), "не готово") {
		t.Errorf("ListThreads = %v", err)
	}

	if err := a.DeleteThread(1); err == nil {
		t.Error("DeleteThread reported success without a store")
	}

	if _, err := a.SendReply(1, "hi"); err == nil {
		t.Error("SendReply reported success without a sync service")
	}
}

func TestSaveTokenStillWorksAlongsideTheBindings(t *testing.T) {
	st, sy := newFakeStorage(), newFakeSyncer()

	var seen string

	a := New(
		WithTokenStore(&fakeStore{}),
		WithTokenChecker(okChecker(&seen)),
		WithStorage(st),
		WithSyncer(sy),
	)

	status := a.SaveToken("xoxp-new")
	if !status.OK {
		t.Fatalf("SaveToken = %+v", status)
	}

	if seen != "xoxp-new" {
		t.Errorf("checked token = %q", seen)
	}
}

// blockedSyncer returns a syncer that parks inside its first call until
// the returned release function is called.
func blockedSyncer() (*fakeSyncer, chan struct{}, chan struct{}) {
	sy := newFakeSyncer()
	started := make(chan struct{})
	release := make(chan struct{})
	sy.addStarted = started
	sy.addRelease = release

	return sy, started, release
}

func TestRefreshThreadRejectsAConcurrentRefreshOfTheSameThread(t *testing.T) {
	st := newFakeStorage()
	seedThread(st)

	sy, started, release := blockedSyncer()
	a := newTestApp(t, st, sy)

	done := make(chan error, 1)

	go func() {
		_, err := a.RefreshThread(1)
		done <- err
	}()

	<-started

	if _, err := a.RefreshThread(1); !errors.Is(err, ErrBusy) {
		t.Fatalf("second refresh = %v, want ErrBusy", err)
	}

	close(release)

	if err := <-done; err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	// Once the first refresh is done the thread is free again.
	if _, err := a.RefreshThread(1); err != nil {
		t.Fatalf("third refresh: %v", err)
	}
}

func TestRefreshAllIsExclusiveWithSingleThreadRefreshes(t *testing.T) {
	st := newFakeStorage()
	seedThread(st)

	sy, started, release := blockedSyncer()
	a := newTestApp(t, st, sy)

	done := make(chan error, 1)

	go func() {
		_, err := a.RefreshAll()
		done <- err
	}()

	<-started

	if _, err := a.RefreshThread(1); !errors.Is(err, ErrBusy) {
		t.Errorf("refresh during RefreshAll = %v, want ErrBusy", err)
	}

	if _, err := a.RefreshAll(); !errors.Is(err, ErrBusy) {
		t.Errorf("second RefreshAll = %v, want ErrBusy", err)
	}

	close(release)

	if err := <-done; err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}
}

func TestRefreshOfAnotherThreadRunsInParallel(t *testing.T) {
	st := newFakeStorage()
	seedThread(st)
	st.add(store.Thread{ID: 2, ChannelID: "C2", ThreadTS: "1.2"})

	sy, started, release := blockedSyncer()
	a := newTestApp(t, st, sy)

	done := make(chan error, 1)

	go func() {
		_, err := a.RefreshThread(1)
		done <- err
	}()

	<-started

	if _, err := a.RefreshThread(2); err != nil {
		t.Fatalf("refreshing another thread = %v, want success", err)
	}

	close(release)
	<-done
}

func TestAddThreadRejectsTheSameLinkTwiceAtOnce(t *testing.T) {
	st := newFakeStorage()
	seedThread(st)

	sy, started, release := blockedSyncer()
	sy.addResult = syncsvc.Result{Thread: store.Thread{ID: 1}}
	a := newTestApp(t, st, sy)

	url := "https://acme.slack.com/archives/C1/p1717171717000200"

	done := make(chan error, 1)

	go func() {
		_, err := a.AddThread(url)
		done <- err
	}()

	<-started

	if _, err := a.AddThread(url); !errors.Is(err, ErrBusy) {
		t.Fatalf("second AddThread = %v, want ErrBusy", err)
	}

	close(release)

	if err := <-done; err != nil {
		t.Fatalf("first AddThread: %v", err)
	}
}

func TestProgressEventsReachTheFrontend(t *testing.T) {
	st, sy := newFakeStorage(), newFakeSyncer()

	events := make(chan syncsvc.Progress, 4)

	a := New(
		WithTokenStore(&fakeStore{token: "xoxp-good"}),
		WithTokenChecker(okChecker(nil)),
		WithStorage(st),
		WithEmitter(func(_ context.Context, event string, data ...interface{}) {
			if event != EventProgress {
				t.Errorf("unexpected event %q", event)

				return
			}

			p, ok := data[0].(syncsvc.Progress)
			if !ok {
				t.Errorf("event payload = %T, want sync.Progress", data[0])

				return
			}

			events <- p
		}),
		// The build function is what startup wires; returning the fake
		// exercises the same path production takes.
		withBuilder(func(_ config.Config, _ Storage, _ string) Syncer { return sy }),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a.startup(ctx)

	sy.progress <- syncsvc.Progress{ThreadID: 1, Stage: syncsvc.StageTranslating, Done: 2, Total: 5}

	select {
	case got := <-events:
		if got.ThreadID != 1 || got.Stage != syncsvc.StageTranslating || got.Done != 2 {
			t.Errorf("progress = %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("progress event never reached the frontend")
	}
}
