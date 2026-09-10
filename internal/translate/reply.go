package translate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrEmptyReply is returned when there is nothing to translate: the user
// pressed «Перевести» on an empty draft.
var ErrEmptyReply = errors.New("translate: reply text is empty")

// replyResponse is the JSON the model is asked to answer a draft with.
type replyResponse struct {
	TextEN string `json:"text_en"`
	BackRU string `json:"back_ru"`
}

// DraftReply translates a Russian reply into English and translates the
// result back into Russian. The back translation is what the user checks
// before sending: it shows what the English text actually says, not what
// the author meant to write.
//
// The turn runs in the thread's own session, so the reply picks up the
// terminology and the tone of the discussion it answers.
func (t *Translator) DraftReply(ctx context.Context, threadID, ru string) (en, backRU string, err error) {
	ru = strings.TrimSpace(ru)
	if ru == "" {
		return "", "", ErrEmptyReply
	}

	session, err := t.sessions.Session(ctx, threadID)
	if err != nil {
		return "", "", err
	}

	raw, err := session.Send(ctx, buildReplyPrompt(ru))
	if err != nil {
		return "", "", err
	}

	return parseReply(raw)
}

// buildReplyPrompt renders the draft turn: the Russian text plus what has
// to be preserved verbatim and what the answer must look like.
func buildReplyPrompt(ru string) string {
	var b strings.Builder

	b.WriteString("Переведи мой ответ на английский для отправки в этот тред.\n")
	b.WriteString("Регистр — обычная рабочая переписка: живой английский, не подстрочник и не формальное письмо.\n")
	b.WriteString("Оставь без изменений: код, текст в бэктиках, блоки кода, логи, пути, команды,\n")
	b.WriteString("идентификаторы, URL, ключи задач, @упоминания, #каналы и :emoji_codes:.\n")
	b.WriteString("Сохрани списки, нумерацию и переносы строк. Ничего не добавляй и не убирай.\n")
	b.WriteString("Затем переведи получившийся английский текст обратно на русский — буквально, ")
	b.WriteString("чтобы я увидел, что именно отправляю, а не то, что хотел сказать.\n")
	b.WriteString("Обратный перевод делай по английскому тексту, а не по моему исходному.\n")
	b.WriteString(`Ответь одним JSON-объектом вида {"text_en":"<английский>","back_ru":"<обратный перевод>"}`)
	b.WriteString(".\n\nREPLY:\n")
	b.WriteString(encodeJSON(ru))
	b.WriteString("\n")

	return b.String()
}

// parseReply decodes the answer, stripping any prose or code fence the
// model wrapped the JSON in.
func parseReply(raw string) (en, backRU string, err error) {
	body, err := extractJSONObject(raw)
	if err != nil {
		return "", "", err
	}

	var resp replyResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return "", "", &ResponseError{
			Reason: fmt.Sprintf("decoding answer: %v", err),
			Raw:    truncate(raw, 500),
		}
	}

	en = strings.TrimSpace(resp.TextEN)
	backRU = strings.TrimSpace(resp.BackRU)

	switch {
	case en == "":
		return "", "", &ResponseError{Reason: "answer carries no english text", Raw: truncate(raw, 500)}
	case backRU == "":
		return "", "", &ResponseError{Reason: "answer carries no back translation", Raw: truncate(raw, 500)}
	}

	return en, backRU, nil
}
