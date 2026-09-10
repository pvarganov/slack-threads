package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// defaultBatchChars is the soft budget of a single translation request,
// counted in runes of message text. A batch that would exceed it is closed
// and the next message starts a new one; a single message longer than the
// budget goes into a batch of its own.
const defaultBatchChars = 6000

// maxTranslateAttempts is how many turns one batch may take: the first
// request plus one retry asking only for the translations that came back
// missing.
const maxTranslateAttempts = 2

// Message is one Slack message handed to the translator.
type Message struct {
	// ID identifies the message inside the request; the answer is matched
	// back by it. The sync layer passes the Slack ts.
	ID string
	// Author is the display name of the author, used for tone and for
	// resolving "I"/"you" in the discussion.
	Author string
	// Text is the raw mrkdwn text as Slack returned it.
	Text string
	// TextRU, when set, marks the message as already translated: it is
	// passed as terminology context instead of being translated again.
	TextRU string
}

// Translation is the Russian rendering of one message.
type Translation struct {
	// ID is the ID of the translated Message.
	ID string
	// TextRU is the translation.
	TextRU string
}

// promptMessage is the on-the-wire form of a message to translate.
type promptMessage struct {
	ID     string `json:"id"`
	Author string `json:"author"`
	Text   string `json:"text"`
}

// promptContextMessage additionally carries the existing translation.
type promptContextMessage struct {
	ID     string `json:"id"`
	Author string `json:"author"`
	Text   string `json:"text"`
	TextRU string `json:"text_ru"`
}

// translateResponse is the JSON the model is asked to answer with.
type translateResponse struct {
	Translations []struct {
		ID     string `json:"id"`
		TextRU string `json:"text_ru"`
	} `json:"translations"`
}

// Turn is one request/response exchange with a claude session.
type Turn interface {
	Send(ctx context.Context, prompt string) (string, error)
	// SessionID is the claude session id, learned from the first turn, so
	// it can be persisted and reused via --resume.
	SessionID() string
}

// Sessions hands out the long-lived session of a thread. *Manager is
// adapted to it by ManagerSessions.
type Sessions interface {
	Session(ctx context.Context, threadID string) (Turn, error)
	// CloseThread shuts a thread's session down, e.g. when the thread is
	// deleted.
	CloseThread(threadID string) error
	// Close shuts every session down.
	Close() error
}

// ManagerSessions adapts a Manager to Sessions. Resume, when set, returns
// the claude session id stored on the thread, so a restarted app continues
// the same conversation.
type ManagerSessions struct {
	Manager *Manager
	Resume  func(threadID string) string
}

// Session implements Sessions.
func (a ManagerSessions) Session(ctx context.Context, threadID string) (Turn, error) {
	var resumeID string
	if a.Resume != nil {
		resumeID = a.Resume(threadID)
	}

	return a.Manager.Session(ctx, threadID, resumeID)
}

// CloseThread implements Sessions.
func (a ManagerSessions) CloseThread(threadID string) error {
	return a.Manager.CloseThread(threadID)
}

// Close implements Sessions.
func (a ManagerSessions) Close() error {
	return a.Manager.Close()
}

// Translator turns Slack messages into Russian through the per-thread
// claude sessions handed out by Sessions.
type Translator struct {
	sessions Sessions
	// batchChars is the request size budget; zero means defaultBatchChars.
	batchChars int
}

// NewTranslator returns a translator using sessions.
func NewTranslator(sessions Sessions) *Translator {
	return &Translator{sessions: sessions, batchChars: defaultBatchChars}
}

// TranslateMessages translates every message of msgs that has no TextRU
// yet, in batches, and returns the translations in input order. Messages
// that already carry a translation are only passed along as context.
func (t *Translator) TranslateMessages(ctx context.Context, threadID string, msgs []Message) ([]Translation, error) {
	pending, translated := splitTranslated(msgs)
	if len(pending) == 0 {
		return nil, nil
	}

	session, err := t.sessions.Session(ctx, threadID)
	if err != nil {
		return nil, err
	}

	out := make([]Translation, 0, len(pending))
	// Every batch sees the messages translated before it, including the
	// ones translated in this very call, so terms stay consistent.
	seen := translated

	for _, batch := range splitBatches(pending, t.budget()) {
		got, err := translateBatch(ctx, session, batch, seen)
		if err != nil {
			return nil, err
		}

		out = append(out, got...)

		for i, msg := range batch {
			msg.TextRU = got[i].TextRU
			seen = append(seen, msg)
		}
	}

	return out, nil
}

// SessionID returns the thread's claude session id, once a turn has run and
// learned it, so the caller can persist it for --resume across restarts.
func (t *Translator) SessionID(ctx context.Context, threadID string) (string, error) {
	session, err := t.sessions.Session(ctx, threadID)
	if err != nil {
		return "", err
	}

	return session.SessionID(), nil
}

// CloseThread shuts a thread's claude session down, e.g. when the thread is
// deleted.
func (t *Translator) CloseThread(threadID string) error {
	return t.sessions.CloseThread(threadID)
}

// Close shuts every session down, e.g. when the translator is being
// replaced by a freshly wired one.
func (t *Translator) Close() error {
	return t.sessions.Close()
}

func (t *Translator) budget() int {
	if t.batchChars <= 0 {
		return defaultBatchChars
	}

	return t.batchChars
}

// translateBatch runs one batch, retrying once for the translations that
// came back missing.
func translateBatch(ctx context.Context, session Turn, batch, prior []Message) ([]Translation, error) {
	got := make(map[string]string, len(batch))
	want := batch

	for attempt := 1; ; attempt++ {
		raw, err := session.Send(ctx, buildTranslatePrompt(want, prior))
		if err != nil {
			return nil, err
		}

		parsed, err := parseTranslations(raw)
		if err != nil {
			return nil, err
		}

		for id, textRU := range parsed {
			got[id] = textRU
		}

		want = missingMessages(batch, got)
		if len(want) == 0 {
			break
		}

		if attempt >= maxTranslateAttempts {
			return nil, &ResponseError{
				Reason:  "claude did not translate every message",
				Missing: messageIDs(want),
				Raw:     truncate(raw, 500),
			}
		}
	}

	out := make([]Translation, len(batch))
	for i, msg := range batch {
		out[i] = Translation{ID: msg.ID, TextRU: got[msg.ID]}
	}

	return out, nil
}

// splitTranslated separates the messages still needing a translation from
// the ones already translated.
func splitTranslated(msgs []Message) (pending, translated []Message) {
	for _, msg := range msgs {
		if msg.TextRU == "" {
			pending = append(pending, msg)

			continue
		}

		translated = append(translated, msg)
	}

	return pending, translated
}

// splitBatches groups messages into requests of at most budget runes of
// text, preserving order. Messages larger than the budget get their own
// batch rather than being split.
func splitBatches(msgs []Message, budget int) [][]Message {
	var (
		batches [][]Message
		current []Message
		size    int
	)

	for _, msg := range msgs {
		cost := len([]rune(msg.Text))

		if len(current) > 0 && size+cost > budget {
			batches = append(batches, current)
			current, size = nil, 0
		}

		current = append(current, msg)
		size += cost
	}

	if len(current) > 0 {
		batches = append(batches, current)
	}

	return batches
}

// parseTranslations decodes the answer into id → translation. The model is
// asked for bare JSON but sometimes wraps it in a code fence, so the outer
// JSON object is located first. Unknown ids are ignored: the caller matches
// by the ids it asked for.
func parseTranslations(raw string) (map[string]string, error) {
	body, err := extractJSONObject(raw)
	if err != nil {
		return nil, err
	}

	var resp translateResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil, &ResponseError{
			Reason: fmt.Sprintf("decoding answer: %v", err),
			Raw:    truncate(raw, 500),
		}
	}

	out := make(map[string]string, len(resp.Translations))

	for _, tr := range resp.Translations {
		if tr.ID == "" || tr.TextRU == "" {
			continue
		}

		out[tr.ID] = tr.TextRU
	}

	return out, nil
}

// extractJSONObject returns the outermost {...} of the answer, stripping
// any prose or code fence around it.
func extractJSONObject(raw string) (string, error) {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")

	if start < 0 || end < start {
		return "", &ResponseError{Reason: "answer is not JSON", Raw: truncate(raw, 500)}
	}

	return raw[start : end+1], nil
}

// missingMessages returns the messages of batch that got no translation.
func missingMessages(batch []Message, got map[string]string) []Message {
	var missing []Message

	for _, msg := range batch {
		if _, ok := got[msg.ID]; !ok {
			missing = append(missing, msg)
		}
	}

	return missing
}

func messageIDs(msgs []Message) []string {
	ids := make([]string, len(msgs))
	for i, msg := range msgs {
		ids[i] = msg.ID
	}

	return ids
}

// encodeBatch renders the messages to translate as indented JSON.
func encodeBatch(msgs []Message) string {
	payload := make([]promptMessage, len(msgs))
	for i, msg := range msgs {
		payload[i] = promptMessage{ID: msg.ID, Author: msg.Author, Text: msg.Text}
	}

	return encodeJSON(payload)
}

// encodeContext renders the already translated messages as indented JSON.
func encodeContext(msgs []Message) string {
	payload := make([]promptContextMessage, len(msgs))
	for i, msg := range msgs {
		// Same fields in the same order; only the JSON tags differ.
		payload[i] = promptContextMessage(msg)
	}

	return encodeJSON(payload)
}

// encodeJSON marshals a prompt payload; the types involved cannot fail to
// encode, so an error can only be a programming mistake.
func encodeJSON(v any) string {
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("encoding error: %v", err)
	}

	return string(buf)
}
