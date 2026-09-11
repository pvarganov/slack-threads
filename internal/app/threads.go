package app

import (
	"context"
	"errors"

	"github.com/pavelvarganov/slack-threads/internal/permalink"
	"github.com/pavelvarganov/slack-threads/internal/slackapi"
	"github.com/pavelvarganov/slack-threads/internal/store"
	syncsvc "github.com/pavelvarganov/slack-threads/internal/sync"
)

// Syncer is the orchestration surface the bindings drive.
// *sync.Service implements it.
type Syncer interface {
	AddThread(ctx context.Context, rawURL string) (syncsvc.Result, error)
	RefreshThread(ctx context.Context, threadID int64) (syncsvc.Result, error)
	RefreshAll(ctx context.Context) ([]syncsvc.ThreadResult, error)
	DraftReply(ctx context.Context, threadID int64, ru string) (store.Draft, error)
	GetDraft(ctx context.Context, threadID int64) (store.Draft, error)
	SendReply(ctx context.Context, threadID int64, en string) (syncsvc.Sent, error)
	Progress() <-chan syncsvc.Progress
	// CloseThread shuts down the claude session of one thread, e.g. when
	// the thread is deleted.
	CloseThread(threadID int64) error
	// Close shuts down every claude session, e.g. when a token change
	// replaces the service with a freshly wired one.
	Close() error
}

// Storage is the read side the bindings need on top of the sync service,
// plus the two operations that never touch Slack: delete and archive.
// *store.Store implements it.
type Storage interface {
	ListThreads(ctx context.Context, includeArchived bool) ([]store.Thread, error)
	GetThread(ctx context.Context, id int64) (store.Thread, error)
	GetThreadByKey(ctx context.Context, channelID, threadTS string) (store.Thread, error)
	ListMessages(ctx context.Context, threadID int64) ([]store.Message, error)
	ListTranslations(ctx context.Context, threadID int64) (map[int64]store.Translation, error)
	GetSummary(ctx context.Context, threadID int64) (store.Summary, error)
	GetUsers(ctx context.Context, ids []string) (map[string]store.User, error)
	RootTranslations(ctx context.Context) (map[int64]string, error)
	SetThreadArchived(ctx context.Context, id int64, archived bool) error
	DeleteThread(ctx context.Context, id int64) error
}

// AddThread starts tracking the thread a Slack permalink points at and
// returns it fully rendered, ready to be shown.
func (a *App) AddThread(rawURL string) (ThreadView, error) {
	sync := a.syncer()
	if sync == nil {
		return ThreadView{}, errNotReady()
	}

	ctx := a.context()

	// A permalink that resolves to an already-tracked thread must be
	// locked under its threadKey, the same key RefreshThread and
	// DeleteThread use, or a concurrent call on that thread would race
	// with this one instead of failing fast with ErrBusy.
	key := urlKey(rawURL)
	if a.store != nil {
		if link, err := permalink.Parse(rawURL); err == nil {
			if existing, err := a.store.GetThreadByKey(ctx, link.ChannelID, link.ThreadTS); err == nil {
				key = threadKey(existing.ID)
			}
		}
	}

	if !a.locks.acquire(key) {
		return ThreadView{}, userError(ErrBusy)
	}
	defer a.locks.release(key)

	res, err := sync.AddThread(ctx, rawURL)
	if err != nil {
		return ThreadView{}, userError(err)
	}

	return a.threadView(ctx, res.Thread.ID)
}

// ListThreads returns every tracked thread, archived ones included: the
// list decides itself how to show them.
func (a *App) ListThreads() ([]ThreadItem, error) {
	if a.store == nil {
		return nil, errNotReady()
	}

	ctx := a.context()

	threads, err := a.store.ListThreads(ctx, true)
	if err != nil {
		return nil, userError(err)
	}

	roots, err := a.store.RootTranslations(ctx)
	if err != nil {
		return nil, userError(err)
	}

	out := make([]ThreadItem, 0, len(threads))
	for _, t := range threads {
		out = append(out, withRootLine(t, roots[t.ID]))
	}

	return out, nil
}

// GetThread returns a stored thread with its summary, messages and draft.
// Nothing here talks to Slack: it is the local copy.
func (a *App) GetThread(id int64) (ThreadView, error) {
	if a.store == nil {
		return ThreadView{}, errNotReady()
	}

	return a.threadView(a.context(), id)
}

// RefreshThread re-reads one thread from Slack and translates the delta.
func (a *App) RefreshThread(id int64) (ThreadView, error) {
	sync := a.syncer()
	if sync == nil {
		return ThreadView{}, errNotReady()
	}

	key := threadKey(id)
	if !a.locks.acquire(key) {
		return ThreadView{}, userError(ErrBusy)
	}
	defer a.locks.release(key)

	ctx := a.context()

	if _, err := sync.RefreshThread(ctx, id); err != nil {
		return ThreadView{}, userError(err)
	}

	return a.threadView(ctx, id)
}

// RefreshAll refreshes every non-archived thread and reports each one
// separately: a thread that failed carries its message and the rest still
// get refreshed.
func (a *App) RefreshAll() ([]RefreshOutcome, error) {
	sync := a.syncer()
	if sync == nil {
		return nil, errNotReady()
	}

	if !a.locks.acquireAll() {
		return nil, userError(ErrBusy)
	}
	defer a.locks.release(keyAll)

	results, err := sync.RefreshAll(a.context())
	if err != nil {
		return nil, userError(err)
	}

	out := make([]RefreshOutcome, 0, len(results))

	for _, r := range results {
		outcome := RefreshOutcome{
			ThreadID: r.ThreadID,
			Title:    r.Result.Thread.Title,
			Changed:  changedCount(r.Result),
		}

		if r.Err != nil {
			outcome.Error = userError(r.Err).Error()
		}

		out = append(out, outcome)
	}

	return out, nil
}

// DeleteThread forgets a thread completely: messages, translations and
// draft go with it.
func (a *App) DeleteThread(id int64) error {
	if a.store == nil {
		return errNotReady()
	}

	key := threadKey(id)
	if !a.locks.acquire(key) {
		return userError(ErrBusy)
	}
	defer a.locks.release(key)

	if err := a.store.DeleteThread(a.context(), id); err != nil {
		return userError(err)
	}

	if sync := a.syncer(); sync != nil {
		// Best-effort cleanup: the thread is already gone from the store,
		// so a claude process that failed to shut down would only be
		// reaped later by the idle timeout, not surfaced as a failure.
		_ = sync.CloseThread(id)
	}

	return nil
}

// ArchiveThread stops (or resumes) refreshing a thread while keeping
// everything already translated.
func (a *App) ArchiveThread(id int64, archived bool) error {
	if a.store == nil {
		return errNotReady()
	}

	return userError(a.store.SetThreadArchived(a.context(), id, archived))
}

// DraftReply translates a Russian reply into English and stores it as the
// thread's draft, together with the back translation to check it by.
func (a *App) DraftReply(id int64, ru string) (DraftView, error) {
	sync := a.syncer()
	if sync == nil {
		return DraftView{}, errNotReady()
	}

	key := threadKey(id)
	if !a.locks.acquire(key) {
		return DraftView{}, userError(ErrBusy)
	}
	defer a.locks.release(key)

	draft, err := sync.DraftReply(a.context(), id, ru)
	if err != nil {
		return DraftView{}, userError(err)
	}

	return draftView(draft), nil
}

// SendReply posts the English text into the thread. The text comes from
// the UI rather than from the draft, so the user can edit it first.
func (a *App) SendReply(id int64, en string) (SentView, error) {
	sync := a.syncer()
	if sync == nil {
		return SentView{}, errNotReady()
	}

	key := threadKey(id)
	if !a.locks.acquire(key) {
		return SentView{}, userError(ErrBusy)
	}
	defer a.locks.release(key)

	sent, err := sync.SendReply(a.context(), id, en)
	if err != nil {
		return SentView{}, userError(err)
	}

	return SentView{ThreadID: sent.ThreadID, TS: sent.TS, Permalink: sent.Permalink}, nil
}

// threadView assembles the local copy of a thread for the UI.
func (a *App) threadView(ctx context.Context, id int64) (ThreadView, error) {
	thread, err := a.store.GetThread(ctx, id)
	if err != nil {
		return ThreadView{}, userError(err)
	}

	msgs, err := a.store.ListMessages(ctx, id)
	if err != nil {
		return ThreadView{}, userError(err)
	}

	translations, err := a.store.ListTranslations(ctx, id)
	if err != nil {
		return ThreadView{}, userError(err)
	}

	users, err := a.store.GetUsers(ctx, authorIDs(msgs))
	if err != nil {
		return ThreadView{}, userError(err)
	}

	view := ThreadView{
		Thread:   threadItem(thread),
		Messages: messageViews(msgs, translations, users),
		Draft:    DraftView{ThreadID: id},
	}

	// An absent summary or draft is a normal state for a thread that was
	// just added and is not worth failing the whole view over.
	summary, err := a.store.GetSummary(ctx, id)

	switch {
	case err == nil:
		view.Summary = summary.TextRU
		view.SummaryBlocks = slackapi.RenderMrkdwn(summary.TextRU, userNames(users))
	case !errors.Is(err, store.ErrNotFound):
		return ThreadView{}, userError(err)
	}

	if sync := a.syncer(); sync != nil {
		draft, err := sync.GetDraft(ctx, id)
		if err != nil {
			return ThreadView{}, userError(err)
		}

		view.Draft = draftView(draft)
	}

	return view, nil
}

// errNotReady is what a binding returns when the app could not wire up its
// dependencies at startup — the UI shows the message instead of a blank
// screen.
func errNotReady() error {
	return &UserError{
		Message: "Приложение не готово: хранилище или переводчик не запустились. Перезапустите приложение.",
		Fixable: false,
	}
}
