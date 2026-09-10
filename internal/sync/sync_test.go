package sync_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/pavelvarganov/slack-threads/internal/slackapi"
	"github.com/pavelvarganov/slack-threads/internal/store"
	"github.com/pavelvarganov/slack-threads/internal/sync"
	"github.com/pavelvarganov/slack-threads/internal/translate"
)

const (
	rootTS  = "1700000000.000100"
	replyTS = "1700000000.000200"
	thirdTS = "1700000000.000300"
	// threadURL points at the root message of the fixture thread.
	threadURL = "https://acme.slack.com/archives/C0LOAD/p1700000000000100"
)

// fakeSlack serves recorded threads keyed by "channel/ts" and can be told to
// fail a specific thread.
type fakeSlack struct {
	threads map[string][]slackapi.Message
	users   map[string]slackapi.User
	errs    map[string]error
	fetches int
	// posted records every reply sent through PostMessage.
	posted []postedReply
	// postErr, when set, fails every PostMessage call.
	postErr error
}

// postedReply is one recorded chat.postMessage call.
type postedReply struct {
	channelID string
	threadTS  string
	text      string
}

func (f *fakeSlack) PostMessage(
	_ context.Context, channelID, threadTS, text string,
) (slackapi.Posted, error) {
	if f.postErr != nil {
		return slackapi.Posted{}, f.postErr
	}

	f.posted = append(f.posted, postedReply{channelID: channelID, threadTS: threadTS, text: text})

	return slackapi.Posted{
		Channel:   channelID,
		TS:        "1700000000.000900",
		ThreadTS:  threadTS,
		Permalink: "https://acme.slack.com/archives/" + channelID + "/p1700000000000900",
	}, nil
}

func (f *fakeSlack) FetchThread(_ context.Context, channelID, threadTS string) ([]slackapi.Message, error) {
	f.fetches++

	key := channelID + "/" + threadTS
	if err := f.errs[key]; err != nil {
		return nil, err
	}

	return f.threads[key], nil
}

func (f *fakeSlack) ResolveUsers(_ context.Context, ids []string) (map[string]slackapi.User, error) {
	out := make(map[string]slackapi.User, len(ids))

	for _, id := range ids {
		if u, ok := f.users[id]; ok {
			out[id] = u
		}
	}

	return out, nil
}

// fakeTranslator translates by prefixing the text and records every request
// so the tests can assert what was (and was not) sent to the model.
type fakeTranslator struct {
	requests  [][]translate.Message
	summaries [][]translate.Message
	summary   string
	keys      []string
	err       error
	summErr   error
	// drafts records the Russian replies handed to DraftReply.
	drafts []string
	// draftErr, when set, fails every DraftReply call.
	draftErr error
}

func (f *fakeTranslator) DraftReply(_ context.Context, threadID, ru string) (string, string, error) {
	if f.draftErr != nil {
		return "", "", f.draftErr
	}

	f.drafts = append(f.drafts, ru)
	f.keys = append(f.keys, threadID)

	return "en:" + ru, "back:" + ru, nil
}

func (f *fakeTranslator) TranslateMessages(
	_ context.Context, threadID string, msgs []translate.Message,
) ([]translate.Translation, error) {
	if f.err != nil {
		return nil, f.err
	}

	f.requests = append(f.requests, msgs)
	f.keys = append(f.keys, threadID)

	var out []translate.Translation

	for _, m := range msgs {
		if m.TextRU != "" {
			continue
		}

		out = append(out, translate.Translation{ID: m.ID, TextRU: "ru:" + m.Text})
	}

	return out, nil
}

func (f *fakeTranslator) Summarize(
	_ context.Context, _ string, translated []translate.Message,
) (string, error) {
	if f.summErr != nil {
		return "", f.summErr
	}

	f.summaries = append(f.summaries, translated)

	return f.summary, nil
}

// pending lists the IDs a recorded request actually asked to translate.
func pending(msgs []translate.Message) []string {
	var out []string

	for _, m := range msgs {
		if m.TextRU == "" {
			out = append(out, m.ID)
		}
	}

	return out
}

// threadMessages is the fixture thread: three messages by two authors.
func threadMessages() []slackapi.Message {
	return []slackapi.Message{
		{TS: rootTS, ThreadTS: rootTS, User: "U1", Text: "Deploy is broken", Raw: []byte(`{"ts":"root"}`)},
		{TS: replyTS, ThreadTS: rootTS, User: "U2", Text: "Which service?", Raw: []byte(`{"ts":"reply"}`)},
		{TS: thirdTS, ThreadTS: rootTS, User: "U1", Text: "The billing one", Raw: []byte(`{"ts":"third"}`)},
	}
}

// newService wires a service over a real SQLite store and the two fakes.
func newService(t *testing.T, opts ...sync.Option) (*sync.Service, *store.Store, *fakeSlack, *fakeTranslator) {
	t.Helper()

	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "threads.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	t.Cleanup(func() { st.Close() })

	slack := &fakeSlack{
		threads: map[string][]slackapi.Message{"C0LOAD/" + rootTS: threadMessages()},
		users: map[string]slackapi.User{
			"U1": {ID: "U1", DisplayName: "alice"},
			"U2": {ID: "U2", DisplayName: "bob"},
		},
		errs: map[string]error{},
	}

	tr := &fakeTranslator{summary: "О чём тред"}

	return sync.New(st, slack, tr, opts...), st, slack, tr
}

// drain reads every progress event buffered so far.
func drain(s *sync.Service) []sync.Progress {
	var out []sync.Progress

	for {
		select {
		case p := <-s.Progress():
			out = append(out, p)
		default:
			return out
		}
	}
}

func TestAddThreadStoresTranslatesAndSummarizes(t *testing.T) {
	t.Parallel()

	svc, st, _, tr := newService(t)
	ctx := context.Background()

	res, err := svc.AddThread(ctx, threadURL)
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	if res.Existed {
		t.Error("Existed = true on the first add")
	}

	if res.Fetched != 3 || res.Inserted != 3 || res.Translated != 3 {
		t.Errorf("Result = %+v, want 3 fetched, inserted and translated", res)
	}

	if !res.SummaryUpdated {
		t.Error("SummaryUpdated = false, want the summary built on the first add")
	}

	thread, err := st.GetThreadByKey(ctx, "C0LOAD", rootTS)
	if err != nil {
		t.Fatalf("GetThreadByKey: %v", err)
	}

	if thread.Workspace != "acme" {
		t.Errorf("Workspace = %q, want %q", thread.Workspace, "acme")
	}

	if thread.Title != "Deploy is broken" {
		t.Errorf("Title = %q, want the root message text", thread.Title)
	}

	if thread.LastFetchedAt.IsZero() {
		t.Error("LastFetchedAt is zero after a successful fetch")
	}

	msgs, err := st.ListMessages(ctx, thread.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	if len(msgs) != 3 || msgs[0].TS != rootTS || msgs[0].RawJSON != `{"ts":"root"}` {
		t.Fatalf("stored messages = %+v, want the fetched three with raw payloads", msgs)
	}

	translations, err := st.ListTranslations(ctx, thread.ID)
	if err != nil {
		t.Fatalf("ListTranslations: %v", err)
	}

	for _, m := range msgs {
		got := translations[m.ID]
		if got.TextRU != "ru:"+m.Text {
			t.Errorf("translation of %s = %q, want %q", m.TS, got.TextRU, "ru:"+m.Text)
		}

		if got.Model != translate.DefaultModel {
			t.Errorf("translation of %s recorded model %q, want %q", m.TS, got.Model, translate.DefaultModel)
		}
	}

	sum, err := st.GetSummary(ctx, thread.ID)
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}

	if sum.TextRU != "О чём тред" {
		t.Errorf("summary = %q, want the generated text", sum.TextRU)
	}

	if len(tr.summaries) != 1 {
		t.Errorf("Summarize called %d times, want once", len(tr.summaries))
	}

	// The translator sees the author names, not the raw Slack IDs.
	if authors := tr.requests[0]; authors[0].Author != "alice" || authors[1].Author != "bob" {
		t.Errorf("request authors = %q/%q, want resolved names", authors[0].Author, authors[1].Author)
	}

	if key := tr.keys[0]; key != fmt.Sprint(thread.ID) {
		t.Errorf("session key = %q, want the local thread ID %d", key, thread.ID)
	}
}

func TestAddThreadTwiceDoesNotDuplicate(t *testing.T) {
	t.Parallel()

	svc, st, _, tr := newService(t)
	ctx := context.Background()

	if _, err := svc.AddThread(ctx, threadURL); err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	requestsAfterFirst := len(tr.requests)

	res, err := svc.AddThread(ctx, threadURL)
	if err != nil {
		t.Fatalf("AddThread (again): %v", err)
	}

	if !res.Existed {
		t.Error("Existed = false, want the second add to report a known thread")
	}

	if res.Inserted != 0 || res.Unchanged != 3 || res.Translated != 0 {
		t.Errorf("Result = %+v, want nothing inserted or translated again", res)
	}

	if res.SummaryUpdated {
		t.Error("SummaryUpdated = true, want the summary left alone without a delta")
	}

	if len(tr.requests) != requestsAfterFirst {
		t.Errorf("translator got %d extra requests, want none", len(tr.requests)-requestsAfterFirst)
	}

	threads, err := st.ListThreads(ctx, true)
	if err != nil {
		t.Fatalf("ListThreads: %v", err)
	}

	if len(threads) != 1 {
		t.Fatalf("stored %d threads, want a single one", len(threads))
	}
}

func TestAddThreadRejectsBadLink(t *testing.T) {
	t.Parallel()

	svc, _, slack, _ := newService(t)

	if _, err := svc.AddThread(context.Background(), "https://example.com/not-slack"); err == nil {
		t.Fatal("AddThread accepted a link that is not a Slack permalink")
	}

	if slack.fetches != 0 {
		t.Errorf("Slack was called %d times for an unparsable link, want none", slack.fetches)
	}
}

func TestAddThreadRejectsEmptyThread(t *testing.T) {
	t.Parallel()

	svc, _, slack, _ := newService(t)
	slack.threads["C0LOAD/"+rootTS] = nil

	_, err := svc.AddThread(context.Background(), threadURL)
	if !errors.Is(err, sync.ErrEmptyThread) {
		t.Fatalf("AddThread error = %v, want ErrEmptyThread", err)
	}
}

func TestRefreshThreadTranslatesOnlyTheDelta(t *testing.T) {
	t.Parallel()

	svc, st, slack, tr := newService(t)
	ctx := context.Background()

	res, err := svc.AddThread(ctx, threadURL)
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	threadID := res.Thread.ID
	before := len(tr.requests)

	// One new reply and one edited message; the other two are untouched.
	const newTS = "1700000000.000400"

	updated := threadMessages()
	updated[1].Text = "Which service exactly?"
	updated[1].EditedTS = "1700000000.000450"
	updated = append(updated, slackapi.Message{
		TS: newTS, ThreadTS: rootTS, User: "U2", Text: "Rolling back", Raw: []byte(`{"ts":"new"}`),
	})
	slack.threads["C0LOAD/"+rootTS] = updated

	refreshed, err := svc.RefreshThread(ctx, threadID)
	if err != nil {
		t.Fatalf("RefreshThread: %v", err)
	}

	if refreshed.Inserted != 1 || refreshed.Updated != 1 || refreshed.Unchanged != 2 {
		t.Errorf("Result = %+v, want 1 inserted, 1 updated, 2 unchanged", refreshed)
	}

	if refreshed.Translated != 2 {
		t.Errorf("Translated = %d, want only the new and the edited message", refreshed.Translated)
	}

	if got := len(tr.requests) - before; got != 1 {
		t.Fatalf("translator got %d requests for the delta, want 1", got)
	}

	asked := pending(tr.requests[before])
	if len(asked) != 2 || asked[0] != replyTS || asked[1] != newTS {
		t.Errorf("asked to translate %v, want the edited %s and the new %s", asked, replyTS, newTS)
	}

	// The untouched messages come along as terminology context.
	if total := len(tr.requests[before]); total != 4 {
		t.Errorf("request carried %d messages, want 2 to translate plus 2 as context", total)
	}

	translations, err := st.ListTranslations(ctx, threadID)
	if err != nil {
		t.Fatalf("ListTranslations: %v", err)
	}

	if len(translations) != 4 {
		t.Fatalf("stored %d translations, want one per message", len(translations))
	}

	msgs, err := st.ListMessages(ctx, threadID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	for _, m := range msgs {
		if want := "ru:" + m.Text; translations[m.ID].TextRU != want {
			t.Errorf("translation of %s = %q, want %q", m.TS, translations[m.ID].TextRU, want)
		}
	}

	if !refreshed.SummaryUpdated {
		t.Error("SummaryUpdated = false, want the summary rebuilt after a non-empty delta")
	}
}

func TestRefreshThreadFlagsMessagesDeletedInSlack(t *testing.T) {
	t.Parallel()

	svc, st, slack, _ := newService(t)
	ctx := context.Background()

	res, err := svc.AddThread(ctx, threadURL)
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	slack.threads["C0LOAD/"+rootTS] = threadMessages()[:2]

	refreshed, err := svc.RefreshThread(ctx, res.Thread.ID)
	if err != nil {
		t.Fatalf("RefreshThread: %v", err)
	}

	if refreshed.Deleted != 1 {
		t.Errorf("Deleted = %d, want the message Slack no longer returns flagged", refreshed.Deleted)
	}

	msgs, err := st.ListMessages(ctx, res.Thread.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	if len(msgs) != 3 {
		t.Fatalf("stored %d messages, want the deleted one kept", len(msgs))
	}

	for _, m := range msgs {
		if want := m.TS == thirdTS; m.Deleted != want {
			t.Errorf("message %s: Deleted = %v, want %v", m.TS, m.Deleted, want)
		}
	}
}

func TestRefreshAllWalksLiveThreadsAndIsolatesFailures(t *testing.T) {
	t.Parallel()

	svc, st, slack, _ := newService(t)
	ctx := context.Background()

	ok, err := svc.AddThread(ctx, threadURL)
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	// A thread whose channel Slack refuses.
	broken, err := st.AddThread(ctx, store.Thread{ChannelID: "CBROKEN", ThreadTS: rootTS})
	if err != nil {
		t.Fatalf("AddThread(broken): %v", err)
	}

	slack.errs["CBROKEN/"+rootTS] = slackapi.ErrChannelNotFound

	// An archived thread must not be touched at all.
	archived, err := st.AddThread(ctx, store.Thread{ChannelID: "CARCH", ThreadTS: rootTS})
	if err != nil {
		t.Fatalf("AddThread(archived): %v", err)
	}

	if err := st.SetThreadArchived(ctx, archived.ID, true); err != nil {
		t.Fatalf("SetThreadArchived: %v", err)
	}

	results, err := svc.RefreshAll(ctx)
	if err != nil {
		t.Fatalf("RefreshAll: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("RefreshAll returned %d results, want the two live threads", len(results))
	}

	byID := make(map[int64]sync.ThreadResult, len(results))
	for _, r := range results {
		byID[r.ThreadID] = r
	}

	if _, seen := byID[archived.ID]; seen {
		t.Error("RefreshAll touched the archived thread")
	}

	good, seen := byID[ok.Thread.ID]
	if !seen {
		t.Fatalf("RefreshAll skipped the healthy thread %d", ok.Thread.ID)
	}

	if good.Err != nil {
		t.Errorf("healthy thread failed: %v", good.Err)
	}

	if good.Result.Unchanged != 3 {
		t.Errorf("healthy thread result = %+v, want 3 unchanged messages", good.Result)
	}

	bad, seen := byID[broken.ID]
	if !seen {
		t.Fatalf("RefreshAll skipped the broken thread %d", broken.ID)
	}

	if !errors.Is(bad.Err, slackapi.ErrChannelNotFound) {
		t.Errorf("broken thread error = %v, want channel_not_found", bad.Err)
	}
}

func TestRefreshAllStopsOnCancelledContext(t *testing.T) {
	t.Parallel()

	svc, _, _, _ := newService(t)

	if _, err := svc.AddThread(context.Background(), threadURL); err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := svc.RefreshAll(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("RefreshAll error = %v, want context.Canceled", err)
	}
}

func TestProgressReportsEveryStage(t *testing.T) {
	t.Parallel()

	svc, _, _, _ := newService(t, sync.WithChunkSize(2))
	ctx := context.Background()

	res, err := svc.AddThread(ctx, threadURL)
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	events := drain(svc)

	want := []sync.Progress{
		{ThreadID: res.Thread.ID, Stage: sync.StageFetching},
		{ThreadID: res.Thread.ID, Stage: sync.StageTranslating, Done: 0, Total: 3},
		{ThreadID: res.Thread.ID, Stage: sync.StageTranslating, Done: 2, Total: 3},
		{ThreadID: res.Thread.ID, Stage: sync.StageTranslating, Done: 3, Total: 3},
		{ThreadID: res.Thread.ID, Stage: sync.StageSummarizing},
		{ThreadID: res.Thread.ID, Stage: sync.StageDone},
	}

	if len(events) != len(want) {
		t.Fatalf("progress events = %+v, want %+v", events, want)
	}

	for i, ev := range events {
		if ev != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, ev, want[i])
		}
	}
}

func TestProgressDropsEventsWhenNobodyReads(t *testing.T) {
	t.Parallel()

	// A buffer of one cannot hold the whole run; the sync must not block.
	svc, _, _, _ := newService(t, sync.WithProgressBuffer(1))

	done := make(chan error, 1)

	go func() {
		_, err := svc.AddThread(context.Background(), threadURL)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("AddThread: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("AddThread blocked on the progress channel")
	}

	if events := drain(svc); len(events) != 1 {
		t.Fatalf("progress buffer held %d events, want the single one that fit", len(events))
	}
}

func TestSyncPropagatesTranslatorFailure(t *testing.T) {
	t.Parallel()

	svc, st, _, tr := newService(t)
	tr.err = errors.New("claude died")
	ctx := context.Background()

	if _, err := svc.AddThread(ctx, threadURL); err == nil || !errors.Is(err, tr.err) {
		t.Fatalf("AddThread error = %v, want the translator failure", err)
	}

	// The thread and its messages are stored even though translation
	// failed, so a later refresh only has to translate.
	thread, err := st.GetThreadByKey(ctx, "C0LOAD", rootTS)
	if err != nil {
		t.Fatalf("GetThreadByKey: %v", err)
	}

	pendingMsgs, err := st.UntranslatedMessages(ctx, thread.ID)
	if err != nil {
		t.Fatalf("UntranslatedMessages: %v", err)
	}

	if len(pendingMsgs) != 3 {
		t.Fatalf("%d messages await translation, want all three", len(pendingMsgs))
	}
}

func TestSyncSkipsMessagesWithoutText(t *testing.T) {
	t.Parallel()

	svc, _, slack, tr := newService(t)

	msgs := threadMessages()
	msgs = append(msgs, slackapi.Message{
		TS: "1700000000.000500", ThreadTS: rootTS, User: "U2",
		Subtype: "channel_join", Text: "", Raw: []byte(`{"subtype":"channel_join"}`),
	})
	slack.threads["C0LOAD/"+rootTS] = msgs

	res, err := svc.AddThread(context.Background(), threadURL)
	if err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	if res.Inserted != 4 {
		t.Errorf("Inserted = %d, want every fetched message stored", res.Inserted)
	}

	if res.Translated != 3 {
		t.Errorf("Translated = %d, want the empty join message skipped", res.Translated)
	}

	if got := len(tr.requests[0]); got != 3 {
		t.Errorf("request carried %d messages, want the join message left out", got)
	}
}

func TestSyncStoreSatisfiedByRealStore(t *testing.T) {
	t.Parallel()

	// Compile-time guard: *store.Store must keep implementing sync.Store.
	var _ sync.Store = (*store.Store)(nil)
}

func TestSyncPropagatesSummaryFailure(t *testing.T) {
	t.Parallel()

	svc, st, _, tr := newService(t)
	tr.summErr = errors.New("summary turn failed")
	ctx := context.Background()

	if _, err := svc.AddThread(ctx, threadURL); !errors.Is(err, tr.summErr) {
		t.Fatalf("AddThread error = %v, want the summary failure", err)
	}

	// The translations made before the summary step are kept, so a retry
	// only has to build the summary.
	thread, err := st.GetThreadByKey(ctx, "C0LOAD", rootTS)
	if err != nil {
		t.Fatalf("GetThreadByKey: %v", err)
	}

	translations, err := st.ListTranslations(ctx, thread.ID)
	if err != nil {
		t.Fatalf("ListTranslations: %v", err)
	}

	if len(translations) != 3 {
		t.Fatalf("stored %d translations, want the three made before the summary step", len(translations))
	}
}
