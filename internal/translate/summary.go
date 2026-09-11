package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
)

// summaryMinMessages and summaryMinChars define when a thread is too small
// to be worth summarising: a couple of short messages are already the
// summary of themselves.
const (
	summaryMinMessages = 3
	summaryMinChars    = 400
)

// summaryResponse is the JSON the model is asked to answer with.
type summaryResponse struct {
	Summary string `json:"summary"`
	Title   string `json:"title"`
}

// Summary is what one summarising turn produces: the «Суть» block and the
// thread's subject line for the list.
type Summary struct {
	// TextRU is the «Суть» block, 2-5 sentences.
	TextRU string
	// Title is the subject of the thread, the way an email subject names
	// a conversation: a few words, no trailing period.
	Title string
}

// titleLimit caps the subject line. The model is asked for a few words;
// this is the guard against an answer that ignores the instruction.
const titleLimit = 60

// summaryMessage is the on-the-wire form of a translated message.
type summaryMessage struct {
	ID     string `json:"id"`
	Author string `json:"author"`
	TextRU string `json:"text_ru"`
}

// Summarize builds the "Суть" block of a thread from its translated
// messages — what the thread is about and what is expected from the user —
// and the subject line the thread list is named by. Both come out of one
// turn: the model has just read the thread anyway. A thread of one or two
// short messages gets neither, and the claude session is not touched.
func (t *Translator) Summarize(ctx context.Context, threadID string, translated []Message) (Summary, error) {
	if !NeedsSummary(translated) {
		return Summary{}, nil
	}

	session, err := t.sessions.Session(ctx, threadID)
	if err != nil {
		return Summary{}, err
	}

	raw, err := session.Send(ctx, buildSummaryPrompt(translated))
	if err != nil {
		return Summary{}, err
	}

	return parseSummary(raw)
}

// NeedsSummary reports whether a thread is big enough to summarise. Short
// threads of one or two messages are shown as they are.
func NeedsSummary(msgs []Message) bool {
	if len(msgs) >= summaryMinMessages {
		return true
	}

	size := 0
	for _, msg := range msgs {
		size += len([]rune(summaryText(msg)))
	}

	return size >= summaryMinChars
}

// SummaryBasedOn returns the fingerprint of the messages a summary was
// built from. It is stored in summaries.based_on_ts: the timestamp of the
// newest message plus a hash of the texts, so that both a new message and
// an edit of an old one make the summary outdated.
func SummaryBasedOn(msgs []Message) string {
	if len(msgs) == 0 {
		return ""
	}

	newest := ""
	sum := fnv.New64a()

	for _, msg := range msgs {
		if msg.ID > newest {
			newest = msg.ID
		}

		// Length prefixes keep the hash unambiguous: no concatenation of
		// two messages can look like a different pair.
		text := summaryText(msg)
		// Hashing to a hash.Hash cannot fail, so the error is ignored.
		_, _ = fmt.Fprintf(sum, "%d:%s|%d:%s|", len(msg.ID), msg.ID, len(text), text)
	}

	return fmt.Sprintf("%s#%016x", newest, sum.Sum64())
}

// SummaryOutdated reports whether the stored summary has to be rebuilt for
// msgs. basedOnTS is what SummaryBasedOn returned when the summary was
// written; an empty value means there is no summary yet.
func SummaryOutdated(basedOnTS string, msgs []Message) bool {
	if !NeedsSummary(msgs) {
		return false
	}

	return basedOnTS != SummaryBasedOn(msgs)
}

// summaryText is the text a summary is built from: the translation when
// there is one, the original otherwise (Russian messages are kept as is).
func summaryText(msg Message) string {
	if msg.TextRU != "" {
		return msg.TextRU
	}

	return msg.Text
}

// buildSummaryPrompt renders the summary turn: every translated message of
// the thread plus what the answer has to look like.
func buildSummaryPrompt(msgs []Message) string {
	var b strings.Builder

	b.WriteString("Составь блок «Суть» для этого треда по сообщениям из THREAD.\n")
	b.WriteString("Напиши по-русски, 2–5 предложений, без markdown-заголовков и без списка сообщений:\n")
	b.WriteString("- о чём тред и к чему обсуждение пришло;\n")
	b.WriteString("- что ждут от меня: вопрос, решение, действие — или прямо скажи, что от меня ничего не ждут.\n")
	b.WriteString("Не пересказывай каждое сообщение и ничего не выдумывай: только то, что есть в THREAD.\n\n")
	b.WriteString("Ещё придумай заголовок треда — как тема письма: по-русски, 2–6 слов, ")
	b.WriteString("именительный падеж, без точки в конце и без кавычек. Он должен называть предмет ")
	b.WriteString("обсуждения, а не пересказывать его: «Ошибка CVV в Ecommpay», «Оплата картой падает ")
	b.WriteString("на 3-D Secure». Сохраняй имена систем, продуктов и ошибок как есть.\n")
	b.WriteString(`Ответь одним JSON-объектом вида {"summary":"<текст>","title":"<заголовок>"}`)
	b.WriteString(".\n\nTHREAD:\n")
	b.WriteString(encodeSummary(msgs))
	b.WriteString("\n")

	return b.String()
}

// encodeSummary renders the translated messages as indented JSON.
func encodeSummary(msgs []Message) string {
	payload := make([]summaryMessage, len(msgs))
	for i, msg := range msgs {
		payload[i] = summaryMessage{ID: msg.ID, Author: msg.Author, TextRU: summaryText(msg)}
	}

	return encodeJSON(payload)
}

// parseSummary decodes the answer, stripping any prose or code fence the
// model wrapped the JSON in.
func parseSummary(raw string) (Summary, error) {
	body, err := extractJSONObject(raw)
	if err != nil {
		return Summary{}, err
	}

	var resp summaryResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return Summary{}, &ResponseError{
			Reason: fmt.Sprintf("decoding answer: %v", err),
			Raw:    truncate(raw, 500),
		}
	}

	text := strings.TrimSpace(resp.Summary)
	if text == "" {
		return Summary{}, &ResponseError{Reason: "answer carries no summary", Raw: truncate(raw, 500)}
	}

	// A missing title is not worth failing the turn over: the list falls
	// back to the first line of the thread.
	return Summary{TextRU: text, Title: cleanTitle(resp.Title)}, nil
}

// cleanTitle strips the decorations a subject line must not carry and
// caps its length.
func cleanTitle(title string) string {
	// Точка может стоять и внутри кавычек, и снаружи, поэтому чистим
	// в цикле, пока строка меняется.
	for {
		trimmed := strings.TrimRight(strings.Trim(strings.TrimSpace(title), "\"«»'`"), ".")
		if trimmed == title {
			break
		}

		title = trimmed
	}

	title = strings.Join(strings.Fields(title), " ")

	runes := []rune(title)
	if len(runes) > titleLimit {
		title = strings.TrimSpace(string(runes[:titleLimit])) + "…"
	}

	return title
}
