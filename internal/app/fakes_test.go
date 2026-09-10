package app

import (
	"context"
	"errors"
	"sync"

	"github.com/pavelvarganov/slack-threads/internal/store"
	syncsvc "github.com/pavelvarganov/slack-threads/internal/sync"
)

// fakeSyncer stands in for the sync service: it records calls and returns
// whatever a test set up.
type fakeSyncer struct {
	mu sync.Mutex

	addURL     string
	addResult  syncsvc.Result
	addErr     error
	addStarted chan struct{}
	addRelease chan struct{}

	refreshed  []int64
	refreshErr error

	all    []syncsvc.ThreadResult
	allErr error

	draft    store.Draft
	draftErr error
	draftRU  string

	stored   store.Draft
	storeErr error

	sent    syncsvc.Sent
	sentErr error
	sentEN  string

	progress chan syncsvc.Progress

	closedThreads []int64
	closeCalls    int
}

func newFakeSyncer() *fakeSyncer {
	return &fakeSyncer{progress: make(chan syncsvc.Progress, 4)}
}

// gate parks the first call until the test releases it; every later call
// runs straight through, which is what the parallel-refresh tests need.
func (f *fakeSyncer) gate() {
	f.mu.Lock()
	started, release := f.addStarted, f.addRelease
	f.addStarted, f.addRelease = nil, nil
	f.mu.Unlock()

	if started != nil {
		close(started)
	}

	if release != nil {
		<-release
	}
}

func (f *fakeSyncer) AddThread(_ context.Context, rawURL string) (syncsvc.Result, error) {
	f.mu.Lock()
	f.addURL = rawURL
	f.mu.Unlock()

	f.gate()

	return f.addResult, f.addErr
}

func (f *fakeSyncer) RefreshThread(_ context.Context, threadID int64) (syncsvc.Result, error) {
	f.mu.Lock()
	f.refreshed = append(f.refreshed, threadID)
	f.mu.Unlock()

	f.gate()

	return syncsvc.Result{}, f.refreshErr
}

func (f *fakeSyncer) RefreshAll(_ context.Context) ([]syncsvc.ThreadResult, error) {
	f.gate()

	return f.all, f.allErr
}

func (f *fakeSyncer) DraftReply(_ context.Context, threadID int64, ru string) (store.Draft, error) {
	f.draftRU = ru
	if f.draftErr != nil {
		return store.Draft{}, f.draftErr
	}

	d := f.draft
	d.ThreadID = threadID

	return d, nil
}

func (f *fakeSyncer) GetDraft(_ context.Context, threadID int64) (store.Draft, error) {
	if f.storeErr != nil {
		return store.Draft{}, f.storeErr
	}

	d := f.stored
	d.ThreadID = threadID

	return d, nil
}

func (f *fakeSyncer) SendReply(_ context.Context, threadID int64, en string) (syncsvc.Sent, error) {
	f.sentEN = en
	if f.sentErr != nil {
		return syncsvc.Sent{}, f.sentErr
	}

	s := f.sent
	s.ThreadID = threadID

	return s, nil
}

func (f *fakeSyncer) Progress() <-chan syncsvc.Progress { return f.progress }

func (f *fakeSyncer) CloseThread(threadID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.closedThreads = append(f.closedThreads, threadID)

	return nil
}

func (f *fakeSyncer) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.closeCalls++

	return nil
}

func (f *fakeSyncer) closeCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.closeCalls
}

func (f *fakeSyncer) refreshedIDs() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]int64(nil), f.refreshed...)
}

// fakeStorage is an in-memory stand-in for the store.
type fakeStorage struct {
	threads      map[int64]store.Thread
	order        []int64
	messages     map[int64][]store.Message
	translations map[int64]map[int64]store.Translation
	summaries    map[int64]store.Summary
	users        map[string]store.User

	listErr    error
	getErr     error
	summaryErr error

	deleted  []int64
	archived map[int64]bool
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{
		threads:      map[int64]store.Thread{},
		messages:     map[int64][]store.Message{},
		translations: map[int64]map[int64]store.Translation{},
		summaries:    map[int64]store.Summary{},
		users:        map[string]store.User{},
		archived:     map[int64]bool{},
	}
}

func (s *fakeStorage) add(t store.Thread) {
	s.threads[t.ID] = t
	s.order = append(s.order, t.ID)
}

func (s *fakeStorage) ListThreads(_ context.Context, includeArchived bool) ([]store.Thread, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}

	out := make([]store.Thread, 0, len(s.order))

	for _, id := range s.order {
		t := s.threads[id]
		if t.Archived && !includeArchived {
			continue
		}

		out = append(out, t)
	}

	return out, nil
}

func (s *fakeStorage) GetThread(_ context.Context, id int64) (store.Thread, error) {
	if s.getErr != nil {
		return store.Thread{}, s.getErr
	}

	t, ok := s.threads[id]
	if !ok {
		return store.Thread{}, store.ErrNotFound
	}

	return t, nil
}

func (s *fakeStorage) GetThreadByKey(_ context.Context, channelID, threadTS string) (store.Thread, error) {
	if s.getErr != nil {
		return store.Thread{}, s.getErr
	}

	for _, t := range s.threads {
		if t.ChannelID == channelID && t.ThreadTS == threadTS {
			return t, nil
		}
	}

	return store.Thread{}, store.ErrNotFound
}

func (s *fakeStorage) ListMessages(_ context.Context, threadID int64) ([]store.Message, error) {
	return s.messages[threadID], nil
}

func (s *fakeStorage) ListTranslations(_ context.Context, threadID int64) (map[int64]store.Translation, error) {
	return s.translations[threadID], nil
}

func (s *fakeStorage) GetSummary(_ context.Context, threadID int64) (store.Summary, error) {
	if s.summaryErr != nil {
		return store.Summary{}, s.summaryErr
	}

	sum, ok := s.summaries[threadID]
	if !ok {
		return store.Summary{}, store.ErrNotFound
	}

	return sum, nil
}

func (s *fakeStorage) GetUsers(_ context.Context, ids []string) (map[string]store.User, error) {
	out := make(map[string]store.User, len(ids))

	for _, id := range ids {
		if u, ok := s.users[id]; ok {
			out[id] = u
		}
	}

	return out, nil
}

func (s *fakeStorage) SetThreadArchived(_ context.Context, id int64, archived bool) error {
	if _, ok := s.threads[id]; !ok {
		return store.ErrNotFound
	}

	s.archived[id] = archived

	return nil
}

func (s *fakeStorage) DeleteThread(_ context.Context, id int64) error {
	if _, ok := s.threads[id]; !ok {
		return store.ErrNotFound
	}

	delete(s.threads, id)
	s.deleted = append(s.deleted, id)

	return nil
}

// errStub is a plain error used where the kind does not matter.
var errStub = errors.New("boom")
