package sync

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/pavelvarganov/slack-threads/internal/store"
)

// ErrEmptyReply is returned when there is nothing to send.
var ErrEmptyReply = errors.New("sync: reply text is empty")

// Sent reports a reply that reached Slack.
type Sent struct {
	// ThreadID is the thread the reply was posted to.
	ThreadID int64 `json:"threadId"`
	// TS is the timestamp of the new Slack message.
	TS string `json:"ts"`
	// Permalink links to the sent message; empty when Slack accepted the
	// message but would not hand out a link.
	Permalink string `json:"permalink"`
}

// DraftReply translates a Russian reply for a thread and stores it as the
// thread's draft, replacing whatever was drafted before. The returned draft
// carries both the English text that would be sent and the back
// translation the user checks it by.
func (s *Service) DraftReply(ctx context.Context, threadID int64, ru string) (store.Draft, error) {
	ru = strings.TrimSpace(ru)
	if ru == "" {
		return store.Draft{}, ErrEmptyReply
	}

	thread, err := s.store.GetThread(ctx, threadID)
	if err != nil {
		return store.Draft{}, err
	}

	en, backRU, err := s.translator.DraftReply(ctx, threadKey(thread), ru)
	if err != nil {
		return store.Draft{}, err
	}

	if err := s.persistSession(ctx, thread); err != nil {
		return store.Draft{}, err
	}

	draft := store.Draft{ThreadID: threadID, TextRU: ru, TextEN: en, BackRU: backRU}
	if err := s.store.SaveDraft(ctx, draft); err != nil {
		return store.Draft{}, err
	}

	return draft, nil
}

// GetDraft returns the stored draft of a thread, or an empty draft when
// there is none: an absent draft is a normal state, not a failure.
func (s *Service) GetDraft(ctx context.Context, threadID int64) (store.Draft, error) {
	draft, err := s.store.GetDraft(ctx, threadID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Draft{ThreadID: threadID}, nil
	}

	return draft, err
}

// SendReply posts en into the thread. The text is passed in rather than
// read from the draft, so the user can edit the English before sending.
// On success the draft is dropped and the thread is flagged as locally out
// of date: the reply is in Slack but not yet in the local copy.
func (s *Service) SendReply(ctx context.Context, threadID int64, en string) (Sent, error) {
	en = strings.TrimSpace(en)
	if en == "" {
		return Sent{}, ErrEmptyReply
	}

	thread, err := s.store.GetThread(ctx, threadID)
	if err != nil {
		return Sent{}, err
	}

	posted, err := s.slack.PostMessage(ctx, thread.ChannelID, thread.ThreadTS, en)
	if err != nil {
		// The draft stays put: the user will want to retry or edit it.
		return Sent{}, fmt.Errorf("sync: send reply to thread %d: %w", threadID, err)
	}

	if err := s.store.DeleteDraft(ctx, threadID); err != nil {
		return Sent{}, err
	}

	if err := s.store.SetThreadNeedsRefresh(ctx, threadID, true); err != nil {
		return Sent{}, err
	}

	return Sent{ThreadID: threadID, TS: posted.TS, Permalink: posted.Permalink}, nil
}
