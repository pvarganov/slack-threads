package sync

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pavelvarganov/slack-threads/internal/permalink"
	"github.com/pavelvarganov/slack-threads/internal/slackapi"
	"github.com/pavelvarganov/slack-threads/internal/store"
	"github.com/pavelvarganov/slack-threads/internal/translate"
)

// ErrEmptyThread is returned when Slack answers with no messages at all:
// the thread was deleted, or the token cannot see it.
var ErrEmptyThread = errors.New("sync: slack returned no messages for the thread")

const (
	// defaultChunk is how many messages one translation request covers. It
	// only bounds progress granularity: the translator batches by size on
	// its own, so a chunk may still be split into several requests.
	defaultChunk = 10
	// defaultProgressBuffer is how many progress events are kept for a UI
	// that is not reading fast enough. Events beyond it are dropped: a
	// stale progress bar must never stall a sync.
	defaultProgressBuffer = 128
	// titleLimit is the length of the generated thread title, in runes.
	titleLimit = 80
)

// Stage names the step a sync is on; the UI shows it verbatim.
type Stage string

const (
	// StageFetching is reading the thread from Slack.
	StageFetching Stage = "fetching"
	// StageTranslating is translating messages; Done/Total count them.
	StageTranslating Stage = "translating"
	// StageSummarizing is rebuilding the "Суть" block.
	StageSummarizing Stage = "summarizing"
	// StageDone is emitted once per thread, after everything is stored.
	StageDone Stage = "done"
)

// Progress is one step report for the UI.
type Progress struct {
	// ThreadID is the local thread the report belongs to; zero while the
	// thread is not stored yet.
	ThreadID int64 `json:"threadId"`
	// Stage is the step being reported.
	Stage Stage `json:"stage"`
	// Done is how many messages are translated so far, for
	// StageTranslating.
	Done int `json:"done,omitempty"`
	// Total is how many messages the stage has to translate.
	Total int `json:"total,omitempty"`
}

// Result reports what one thread sync changed.
type Result struct {
	// Thread is the stored thread as it looks after the sync.
	Thread store.Thread `json:"thread"`
	// Existed is true when AddThread found the thread already tracked and
	// refreshed it instead of adding a duplicate.
	Existed bool `json:"existed"`
	// Fetched is how many messages Slack returned.
	Fetched int `json:"fetched"`
	// Inserted, Updated and Unchanged mirror store.UpsertStats.
	Inserted  int `json:"inserted"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	// Deleted is how many stored messages Slack no longer returns and were
	// flagged as deleted by this sync.
	Deleted int `json:"deleted"`
	// Translated is how many messages were translated.
	Translated int `json:"translated"`
	// SummaryUpdated is true when the "Суть" block was rebuilt.
	SummaryUpdated bool `json:"summaryUpdated"`
}

// ThreadResult is the outcome of one thread inside RefreshAll. A thread that
// failed carries the error and an otherwise empty Result: one broken thread
// must not stop the rest.
type ThreadResult struct {
	// ThreadID is the thread the entry is about.
	ThreadID int64 `json:"threadId"`
	// Result is what the sync changed; zero when Err is set.
	Result Result `json:"result"`
	// Err is why the thread failed, nil on success.
	Err error `json:"-"`
}

// Store is the storage surface the orchestration needs. *store.Store
// implements it.
type Store interface {
	AddThread(ctx context.Context, t store.Thread) (store.Thread, error)
	GetThread(ctx context.Context, id int64) (store.Thread, error)
	GetThreadByKey(ctx context.Context, channelID, threadTS string) (store.Thread, error)
	ListThreads(ctx context.Context, includeArchived bool) ([]store.Thread, error)
	UpsertMessages(ctx context.Context, threadID int64, msgs []store.Message) (store.UpsertStats, error)
	MarkMessagesDeleted(ctx context.Context, threadID int64, presentTS []string) (int, error)
	ListMessages(ctx context.Context, threadID int64) ([]store.Message, error)
	ListTranslations(ctx context.Context, threadID int64) (map[int64]store.Translation, error)
	SaveTranslations(ctx context.Context, translations []store.Translation) error
	GetSummary(ctx context.Context, threadID int64) (store.Summary, error)
	SaveSummary(ctx context.Context, sum store.Summary) error
	SetThreadFetched(ctx context.Context, id int64, at time.Time) error
	SetThreadTitle(ctx context.Context, id int64, title string) error
	SetThreadTitleRU(ctx context.Context, id int64, title string) error
	SetThreadNeedsRefresh(ctx context.Context, id int64, needs bool) error
	SetThreadSession(ctx context.Context, id int64, sessionID string) error
	GetDraft(ctx context.Context, threadID int64) (store.Draft, error)
	SaveDraft(ctx context.Context, d store.Draft) error
	DeleteDraft(ctx context.Context, threadID int64) error
}

// Translator is the translation surface the orchestration needs.
// *translate.Translator implements it.
type Translator interface {
	TranslateMessages(ctx context.Context, threadID string, msgs []translate.Message) ([]translate.Translation, error)
	Summarize(ctx context.Context, threadID string, translated []translate.Message) (translate.Summary, error)
	DraftReply(ctx context.Context, threadID, ru string) (en, backRU string, err error)
	// SessionID returns the thread's claude session id, once known.
	SessionID(ctx context.Context, threadID string) (string, error)
	// CloseThread shuts a thread's claude session down.
	CloseThread(threadID string) error
	// Close shuts every claude session down.
	Close() error
}

// Service adds and refreshes threads on top of the store, the Slack client
// and the translator.
type Service struct {
	store      Store
	slack      slackapi.Client
	translator Translator
	model      string
	chunk      int
	progress   chan Progress
	// now supplies the fetch timestamp; tests replace it.
	now func() time.Time
}

// Option customises a Service.
type Option func(*Service)

// WithChunkSize sets how many messages one translation request covers.
func WithChunkSize(n int) Option {
	return func(s *Service) {
		if n > 0 {
			s.chunk = n
		}
	}
}

// WithProgressBuffer sets the capacity of the progress channel.
func WithProgressBuffer(n int) Option {
	return func(s *Service) {
		if n > 0 {
			s.progress = make(chan Progress, n)
		}
	}
}

// WithModel records which model produced the stored translations.
func WithModel(model string) Option {
	return func(s *Service) { s.model = model }
}

// WithClock replaces the source of the fetch timestamp.
func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

// New builds a Service.
func New(st Store, slack slackapi.Client, tr Translator, opts ...Option) *Service {
	s := &Service{
		store:      st,
		slack:      slack,
		translator: tr,
		model:      translate.DefaultModel,
		chunk:      defaultChunk,
		progress:   make(chan Progress, defaultProgressBuffer),
		now:        func() time.Time { return time.Now().UTC() },
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Progress returns the channel the sync reports its steps on. Sends never
// block: an event that does not fit the buffer is dropped, so a UI that
// stopped reading only misses intermediate updates.
func (s *Service) Progress() <-chan Progress {
	return s.progress
}

// CloseThread shuts down the claude session of one thread, e.g. when the
// thread is deleted, instead of leaving it to the idle timeout.
func (s *Service) CloseThread(threadID int64) error {
	return s.translator.CloseThread(strconv.FormatInt(threadID, 10))
}

// Close shuts down every claude session the service owns, e.g. when a token
// change replaces it with a freshly wired one.
func (s *Service) Close() error {
	return s.translator.Close()
}

// emit publishes one progress event, dropping it if nobody keeps up.
func (s *Service) emit(p Progress) {
	select {
	case s.progress <- p:
	default:
	}
}

// AddThread starts tracking the thread a Slack permalink points at: the
// thread is stored, read from Slack, translated and summarised. Adding a
// thread that is already tracked refreshes it instead of creating a
// duplicate; the returned Result then has Existed set.
func (s *Service) AddThread(ctx context.Context, rawURL string) (Result, error) {
	link, err := permalink.Parse(rawURL)
	if err != nil {
		return Result{}, fmt.Errorf("sync: add thread: %w", err)
	}

	existing, err := s.store.GetThreadByKey(ctx, link.ChannelID, link.ThreadTS)

	switch {
	case err == nil:
		res, err := s.syncThread(ctx, existing)
		res.Existed = true

		return res, err
	case !errors.Is(err, store.ErrNotFound):
		return Result{}, err
	}

	thread, err := s.store.AddThread(ctx, store.Thread{
		ChannelID: link.ChannelID,
		ThreadTS:  link.ThreadTS,
		Workspace: link.Workspace,
		TeamID:    link.TeamID,
	})
	if err != nil {
		return Result{}, err
	}

	return s.syncThread(ctx, thread)
}

// RefreshThread re-reads a tracked thread and translates only what changed.
func (s *Service) RefreshThread(ctx context.Context, threadID int64) (Result, error) {
	thread, err := s.store.GetThread(ctx, threadID)
	if err != nil {
		return Result{}, err
	}

	return s.syncThread(ctx, thread)
}

// RefreshAll refreshes every thread that is not archived, in list order. A
// failing thread is reported in its own entry and the walk continues; the
// error return is reserved for a failure to even list the threads.
func (s *Service) RefreshAll(ctx context.Context) ([]ThreadResult, error) {
	threads, err := s.store.ListThreads(ctx, false)
	if err != nil {
		return nil, err
	}

	out := make([]ThreadResult, 0, len(threads))

	for _, thread := range threads {
		// A cancelled context stops the walk: every remaining thread
		// would fail the same way.
		if err := ctx.Err(); err != nil {
			return out, err
		}

		res, err := s.syncThread(ctx, thread)
		out = append(out, ThreadResult{ThreadID: thread.ID, Result: res, Err: err})
	}

	return out, nil
}

// syncThread runs the whole pipeline for one stored thread: fetch, store,
// translate the delta, rebuild the summary.
func (s *Service) syncThread(ctx context.Context, thread store.Thread) (Result, error) {
	res := Result{Thread: thread}

	s.emit(Progress{ThreadID: thread.ID, Stage: StageFetching})

	fetched, err := s.slack.FetchThread(ctx, thread.ChannelID, thread.ThreadTS)
	if err != nil {
		return res, err
	}

	if len(fetched) == 0 {
		return res, fmt.Errorf("%w: %s/%s", ErrEmptyThread, thread.ChannelID, thread.ThreadTS)
	}

	res.Fetched = len(fetched)

	names, err := s.resolveNames(ctx, fetched)
	if err != nil {
		return res, err
	}

	stats, err := s.store.UpsertMessages(ctx, thread.ID, storeMessages(thread.ID, fetched))
	if err != nil {
		return res, err
	}

	res.Inserted, res.Updated, res.Unchanged = stats.Inserted, stats.Updated, stats.Unchanged

	res.Deleted, err = s.store.MarkMessagesDeleted(ctx, thread.ID, timestamps(fetched))
	if err != nil {
		return res, err
	}

	fetchedAt := s.now()

	if err := s.store.SetThreadFetched(ctx, thread.ID, fetchedAt); err != nil {
		return res, err
	}

	thread.LastFetchedAt = fetchedAt

	// Whatever the sync just read is the current state of the thread, so a
	// pending "we posted a reply" flag is settled.
	if thread.NeedsRefresh {
		if err := s.store.SetThreadNeedsRefresh(ctx, thread.ID, false); err != nil {
			return res, err
		}

		thread.NeedsRefresh = false
	}

	if thread.Title == "" {
		thread.Title = threadTitle(fetched[0], names)

		if err := s.store.SetThreadTitle(ctx, thread.ID, thread.Title); err != nil {
			res.Thread = thread
			return res, err
		}
	}

	res.Thread = thread

	msgs, err := s.translateThread(ctx, thread, names, &res)
	if err != nil {
		return res, err
	}

	if err := s.updateSummary(ctx, thread, msgs, &res); err != nil {
		return res, err
	}

	s.emit(Progress{ThreadID: thread.ID, Stage: StageDone})

	return res, nil
}

// resolveNames maps the author IDs of the fetched messages onto display
// names, filling in the bot names Slack ships inline with the message.
func (s *Service) resolveNames(ctx context.Context, msgs []slackapi.Message) (map[string]string, error) {
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.Author())
	}

	users, err := s.slack.ResolveUsers(ctx, ids)
	if err != nil {
		return nil, err
	}

	names := make(map[string]string, len(users))
	for id, u := range users {
		names[id] = u.Name()
	}

	// The name Slack puts on the message wins over the cached profile:
	// an app posts under its own label, and users.info may report an
	// unrelated display name for the app's bot user. It also covers the
	// bots Slack refuses to resolve at all.
	for _, m := range msgs {
		if label := m.Label(); label != "" {
			names[m.Author()] = label
		}
	}

	return names, nil
}

// translateThread translates every stored message that has no up-to-date
// translation and returns the whole thread as translator messages, so the
// summary step can reuse it.
func (s *Service) translateThread(
	ctx context.Context, thread store.Thread, names map[string]string, res *Result,
) ([]translate.Message, error) {
	stored, err := s.store.ListMessages(ctx, thread.ID)
	if err != nil {
		return nil, err
	}

	translations, err := s.store.ListTranslations(ctx, thread.ID)
	if err != nil {
		return nil, err
	}

	msgs, ids := translatorMessages(stored, translations, names)

	pending := 0

	for _, m := range msgs {
		if m.TextRU == "" {
			pending++
		}
	}

	if pending == 0 {
		return msgs, nil
	}

	key := threadKey(thread)
	done := 0

	s.emit(Progress{ThreadID: thread.ID, Stage: StageTranslating, Done: done, Total: pending})

	// Each request carries the messages translated so far as terminology
	// context, including the ones translated a moment ago in this loop.
	for _, chunk := range pendingChunks(msgs, s.chunk) {
		got, err := s.translator.TranslateMessages(ctx, key, contextFor(msgs, chunk))
		if err != nil {
			return nil, err
		}

		saved, err := s.saveTranslations(ctx, got, ids)
		if err != nil {
			return nil, err
		}

		applyTranslations(msgs, got)

		res.Translated += saved
		done += len(chunk)

		s.emit(Progress{ThreadID: thread.ID, Stage: StageTranslating, Done: done, Total: pending})
	}

	if err := s.persistSession(ctx, thread); err != nil {
		return nil, err
	}

	return msgs, nil
}

// persistSession stores the thread's claude session id once the translator
// has learned it, so a restarted app, or a fresh session after an idle
// reap, can pick the same conversation back up via --resume.
func (s *Service) persistSession(ctx context.Context, thread store.Thread) error {
	id, err := s.translator.SessionID(ctx, threadKey(thread))
	if err != nil {
		return err
	}

	if id == "" || id == thread.ClaudeSessionID {
		return nil
	}

	return s.store.SetThreadSession(ctx, thread.ID, id)
}

// saveTranslations stores the translations of one request, skipping ids the
// model invented, and reports how many rows were written.
func (s *Service) saveTranslations(
	ctx context.Context, got []translate.Translation, ids map[string]int64,
) (int, error) {
	rows := make([]store.Translation, 0, len(got))

	for _, tr := range got {
		id, ok := ids[tr.ID]
		if !ok {
			continue
		}

		rows = append(rows, store.Translation{MessageID: id, TextRU: tr.TextRU, Model: s.model})
	}

	if err := s.store.SaveTranslations(ctx, rows); err != nil {
		return 0, err
	}

	return len(rows), nil
}

// updateSummary rebuilds the "Суть" block when the thread changed enough for
// the stored one to be outdated.
func (s *Service) updateSummary(
	ctx context.Context, thread store.Thread, msgs []translate.Message, res *Result,
) error {
	sum, err := s.store.GetSummary(ctx, thread.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}

	// Тред, у которого «Суть» свежая, а темы ещё нет, тоже идёт на этот
	// ход: так тему получают треды, добавленные до её появления, и те,
	// где модель её не прислала.
	needsTitle := thread.TitleRU == "" && translate.NeedsSummary(msgs)

	if !translate.SummaryOutdated(sum.BasedOnTS, msgs) && !needsTitle {
		return nil
	}

	s.emit(Progress{ThreadID: thread.ID, Stage: StageSummarizing})

	sum2, err := s.translator.Summarize(ctx, threadKey(thread), msgs)
	if err != nil {
		return err
	}

	if err := s.persistSession(ctx, thread); err != nil {
		return err
	}

	if sum2.TextRU == "" {
		return nil
	}

	if err := s.store.SaveSummary(ctx, store.Summary{
		ThreadID:  thread.ID,
		TextRU:    sum2.TextRU,
		BasedOnTS: translate.SummaryBasedOn(msgs),
	}); err != nil {
		return err
	}

	// The subject comes out of the same turn; it renames the thread in
	// the list from the root message's first line to what the thread is
	// actually about. A turn that answered without one still writes a
	// title — the Russian root line — so the thread is not re-summarised
	// on every refresh just to ask again.
	title := sum2.Title
	if title == "" {
		title = rootLine(msgs)
	}

	if title != "" && title != thread.TitleRU {
		if err := s.store.SetThreadTitleRU(ctx, thread.ID, title); err != nil {
			return err
		}
	}

	res.SummaryUpdated = true

	return nil
}

// titleFallbackLimit caps the stand-in subject built from the root line.
const titleFallbackLimit = 60

// rootLine is the first line of the root message's translation, used as
// the thread's subject when the model did not write one.
func rootLine(msgs []translate.Message) string {
	if len(msgs) == 0 {
		return ""
	}

	text := msgs[0].TextRU
	if text == "" {
		text = msgs[0].Text
	}

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		runes := []rune(line)
		if len(runes) > titleFallbackLimit {
			return strings.TrimSpace(string(runes[:titleFallbackLimit])) + "…"
		}

		return line
	}

	return ""
}

// threadKey is the translator's session key for a thread. The local ID is
// stable across renames and re-imports of the same conversation.
func threadKey(t store.Thread) string {
	return strconv.FormatInt(t.ID, 10)
}

// storeMessages maps fetched Slack messages onto storage rows.
func storeMessages(threadID int64, msgs []slackapi.Message) []store.Message {
	out := make([]store.Message, 0, len(msgs))

	for _, m := range msgs {
		out = append(out, store.Message{
			ThreadID: threadID,
			TS:       m.TS,
			UserID:   m.Author(),
			Text:     m.Text,
			RawJSON:  string(m.Raw),
			EditedTS: m.EditedTS,
		})
	}

	return out
}

// timestamps lists the timestamps Slack returned, which is what tells the
// store which stored messages are gone.
func timestamps(msgs []slackapi.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.TS)
	}

	return out
}

// translatorMessages turns stored messages into translator messages and maps
// each message ts back onto its local ID. Messages with no text (joins,
// topic changes) and deleted messages that were never translated are left
// out: there is nothing to translate and nothing to keep as context.
func translatorMessages(
	stored []store.Message, translations map[int64]store.Translation, names map[string]string,
) ([]translate.Message, map[string]int64) {
	msgs := make([]translate.Message, 0, len(stored))
	ids := make(map[string]int64, len(stored))

	for _, m := range stored {
		textRU := translations[m.ID].TextRU

		if strings.TrimSpace(m.Text) == "" {
			continue
		}

		if m.Deleted && textRU == "" {
			continue
		}

		author := names[m.UserID]
		if author == "" {
			author = m.UserID
		}

		msgs = append(msgs, translate.Message{ID: m.TS, Author: author, Text: m.Text, TextRU: textRU})

		if !m.Deleted {
			ids[m.TS] = m.ID
		}
	}

	return msgs, ids
}

// pendingChunks groups the untranslated messages into requests of at most
// size messages each, keeping thread order.
func pendingChunks(msgs []translate.Message, size int) [][]translate.Message {
	if size <= 0 {
		size = defaultChunk
	}

	var (
		out   [][]translate.Message
		chunk []translate.Message
	)

	for _, m := range msgs {
		if m.TextRU != "" {
			continue
		}

		chunk = append(chunk, m)

		if len(chunk) == size {
			out = append(out, chunk)
			chunk = nil
		}
	}

	if len(chunk) > 0 {
		out = append(out, chunk)
	}

	return out
}

// contextLimit is how many translated messages travel with a request as
// terminology context. The claude session already holds the whole thread
// in its history, so this block is a reminder of the recent wording, not
// the thread itself. Without a cap every request of a long thread would
// carry all of its predecessors: the hundredth message would be
// translated with ninety-nine messages and their translations in front
// of it, and each request would be slower than the last.
const contextLimit = 12

// contextFor builds one request: the most recent translated messages as
// terminology context, followed by the chunk to translate.
func contextFor(msgs, chunk []translate.Message) []translate.Message {
	translated := make([]translate.Message, 0, len(msgs))

	for _, m := range msgs {
		if m.TextRU != "" {
			translated = append(translated, m)
		}
	}

	if len(translated) > contextLimit {
		translated = translated[len(translated)-contextLimit:]
	}

	out := make([]translate.Message, 0, len(translated)+len(chunk))
	out = append(out, translated...)

	return append(out, chunk...)
}

// applyTranslations writes the fresh translations back into the thread
// slice, so later requests see them as context.
func applyTranslations(msgs []translate.Message, got []translate.Translation) {
	byID := make(map[string]string, len(got))
	for _, tr := range got {
		byID[tr.ID] = tr.TextRU
	}

	for i := range msgs {
		if text, ok := byID[msgs[i].ID]; ok && msgs[i].TextRU == "" {
			msgs[i].TextRU = text
		}
	}
}

// threadTitle builds the label shown in the thread list from the root
// message: its first rendered line, shortened to titleLimit runes.
func threadTitle(root slackapi.Message, names map[string]string) string {
	title := ""

	for _, block := range slackapi.RenderMrkdwn(root.Text, names) {
		title = strings.TrimSpace(blockText(block))
		if title != "" {
			break
		}
	}

	if title == "" {
		title = names[root.Author()]
	}

	if line, _, ok := strings.Cut(title, "\n"); ok {
		title = strings.TrimSpace(line)
	}

	runes := []rune(title)
	if len(runes) > titleLimit {
		title = strings.TrimSpace(string(runes[:titleLimit])) + "…"
	}

	return title
}

// blockText flattens a rendered block back into plain text.
func blockText(b slackapi.Block) string {
	if b.Kind == slackapi.BlockCode {
		return b.Text
	}

	var sb strings.Builder
	for _, span := range b.Spans {
		sb.WriteString(span.Text)
	}

	return sb.String()
}
