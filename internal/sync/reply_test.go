package sync_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/pavelvarganov/slack-threads/internal/slackapi"
	"github.com/pavelvarganov/slack-threads/internal/store"
	"github.com/pavelvarganov/slack-threads/internal/sync"
)

// addedThread adds the fixture thread and returns its stored form.
func addedThread(t *testing.T, svc *sync.Service, st *store.Store) store.Thread {
	t.Helper()

	if _, err := svc.AddThread(context.Background(), threadURL); err != nil {
		t.Fatalf("AddThread: %v", err)
	}

	thread, err := st.GetThreadByKey(context.Background(), "C0LOAD", rootTS)
	if err != nil {
		t.Fatalf("GetThreadByKey: %v", err)
	}

	return thread
}

func TestDraftReplyTranslatesAndStores(t *testing.T) {
	t.Parallel()

	svc, st, _, tr := newService(t)
	ctx := context.Background()
	thread := addedThread(t, svc, st)

	draft, err := svc.DraftReply(ctx, thread.ID, "  откатил `billing`  ")
	if err != nil {
		t.Fatalf("DraftReply: %v", err)
	}

	want := store.Draft{
		ThreadID: thread.ID,
		TextRU:   "откатил `billing`",
		TextEN:   "en:откатил `billing`",
		BackRU:   "back:откатил `billing`",
	}

	if draft.ThreadID != want.ThreadID || draft.TextRU != want.TextRU ||
		draft.TextEN != want.TextEN || draft.BackRU != want.BackRU {
		t.Fatalf("draft = %+v, want %+v", draft, want)
	}

	stored, err := st.GetDraft(ctx, thread.ID)
	if err != nil {
		t.Fatalf("GetDraft: %v", err)
	}

	if stored.TextEN != want.TextEN || stored.BackRU != want.BackRU {
		t.Fatalf("stored draft = %+v, want %+v", stored, want)
	}

	// The draft runs in the thread's own translator session.
	if got := tr.keys[len(tr.keys)-1]; got != strconv.FormatInt(thread.ID, 10) {
		t.Fatalf("session key = %q, want the thread ID", got)
	}
}

func TestDraftReplyReplacesPreviousDraft(t *testing.T) {
	t.Parallel()

	svc, st, _, _ := newService(t)
	ctx := context.Background()
	thread := addedThread(t, svc, st)

	if _, err := svc.DraftReply(ctx, thread.ID, "первый"); err != nil {
		t.Fatalf("DraftReply: %v", err)
	}

	if _, err := svc.DraftReply(ctx, thread.ID, "второй"); err != nil {
		t.Fatalf("DraftReply: %v", err)
	}

	stored, err := st.GetDraft(ctx, thread.ID)
	if err != nil {
		t.Fatalf("GetDraft: %v", err)
	}

	if stored.TextRU != "второй" {
		t.Fatalf("draft = %+v, want the second one", stored)
	}
}

func TestDraftReplyErrors(t *testing.T) {
	t.Parallel()

	t.Run("empty text", func(t *testing.T) {
		t.Parallel()

		svc, st, _, tr := newService(t)
		thread := addedThread(t, svc, st)

		if _, err := svc.DraftReply(context.Background(), thread.ID, "   "); !errors.Is(err, sync.ErrEmptyReply) {
			t.Fatalf("error = %v, want ErrEmptyReply", err)
		}

		if len(tr.drafts) != 0 {
			t.Fatal("an empty draft reached the translator")
		}
	})

	t.Run("unknown thread", func(t *testing.T) {
		t.Parallel()

		svc, _, _, _ := newService(t)

		if _, err := svc.DraftReply(context.Background(), 404, "текст"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
	})

	t.Run("translator fails", func(t *testing.T) {
		t.Parallel()

		svc, st, _, tr := newService(t)
		thread := addedThread(t, svc, st)
		tr.draftErr = errors.New("claude is down")

		if _, err := svc.DraftReply(context.Background(), thread.ID, "текст"); !errors.Is(err, tr.draftErr) {
			t.Fatalf("error = %v, want the translator error", err)
		}

		if _, err := st.GetDraft(context.Background(), thread.ID); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("GetDraft = %v, want nothing stored after a failed translation", err)
		}
	})
}

func TestGetDraftWithoutOne(t *testing.T) {
	t.Parallel()

	svc, st, _, _ := newService(t)
	thread := addedThread(t, svc, st)

	draft, err := svc.GetDraft(context.Background(), thread.ID)
	if err != nil {
		t.Fatalf("GetDraft: %v", err)
	}

	if draft.ThreadID != thread.ID || draft.TextRU != "" || draft.TextEN != "" {
		t.Fatalf("draft = %+v, want an empty draft for the thread", draft)
	}
}

func TestSendReplyPostsClearsDraftAndFlagsThread(t *testing.T) {
	t.Parallel()

	svc, st, slack, _ := newService(t)
	ctx := context.Background()
	thread := addedThread(t, svc, st)

	if _, err := svc.DraftReply(ctx, thread.ID, "откатил"); err != nil {
		t.Fatalf("DraftReply: %v", err)
	}

	sent, err := svc.SendReply(ctx, thread.ID, "  rolled it back  ")
	if err != nil {
		t.Fatalf("SendReply: %v", err)
	}

	if sent.ThreadID != thread.ID || sent.TS != "1700000000.000900" {
		t.Fatalf("sent = %+v", sent)
	}

	if sent.Permalink == "" {
		t.Error("Permalink is empty, want the link to the sent message")
	}

	if len(slack.posted) != 1 {
		t.Fatalf("posted = %d replies, want 1", len(slack.posted))
	}

	got := slack.posted[0]
	if got.channelID != "C0LOAD" || got.threadTS != rootTS || got.text != "rolled it back" {
		t.Fatalf("posted = %+v", got)
	}

	if _, err := st.GetDraft(ctx, thread.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetDraft after send = %v, want the draft cleared", err)
	}

	stored, err := st.GetThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}

	if !stored.NeedsRefresh {
		t.Error("NeedsRefresh = false, want the thread flagged as out of date")
	}
}

func TestSendReplyKeepsDraftOnSlackError(t *testing.T) {
	t.Parallel()

	svc, st, slack, _ := newService(t)
	ctx := context.Background()
	thread := addedThread(t, svc, st)

	if _, err := svc.DraftReply(ctx, thread.ID, "откатил"); err != nil {
		t.Fatalf("DraftReply: %v", err)
	}

	slack.postErr = &slackapi.RestrictedError{
		APIError: &slackapi.APIError{Method: "chat.postMessage", Code: "slack_connect_external_channel_blocked"},
	}

	_, err := svc.SendReply(ctx, thread.ID, "rolled it back")
	if !errors.Is(err, slackapi.ErrPostRestricted) {
		t.Fatalf("error = %v, want ErrPostRestricted", err)
	}

	if _, err := st.GetDraft(ctx, thread.ID); err != nil {
		t.Fatalf("GetDraft after a failed send: %v, want the draft kept", err)
	}

	stored, err := st.GetThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}

	if stored.NeedsRefresh {
		t.Error("NeedsRefresh = true after a failed send")
	}
}

func TestSendReplyRejectsEmptyText(t *testing.T) {
	t.Parallel()

	svc, st, slack, _ := newService(t)
	thread := addedThread(t, svc, st)

	if _, err := svc.SendReply(context.Background(), thread.ID, " \n"); !errors.Is(err, sync.ErrEmptyReply) {
		t.Fatalf("error = %v, want ErrEmptyReply", err)
	}

	if len(slack.posted) != 0 {
		t.Fatal("an empty reply was sent to Slack")
	}
}

func TestRefreshClearsNeedsRefreshFlag(t *testing.T) {
	t.Parallel()

	svc, st, _, _ := newService(t)
	ctx := context.Background()
	thread := addedThread(t, svc, st)

	if _, err := svc.SendReply(ctx, thread.ID, "rolled it back"); err != nil {
		t.Fatalf("SendReply: %v", err)
	}

	res, err := svc.RefreshThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("RefreshThread: %v", err)
	}

	if res.Thread.NeedsRefresh {
		t.Error("Result thread still carries NeedsRefresh")
	}

	stored, err := st.GetThread(ctx, thread.ID)
	if err != nil {
		t.Fatalf("GetThread: %v", err)
	}

	if stored.NeedsRefresh {
		t.Error("NeedsRefresh = true after a refresh")
	}
}
