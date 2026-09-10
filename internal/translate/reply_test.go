package translate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// replyAnswer builds the JSON answer claude is expected to produce.
func replyAnswer(en, backRU string) string {
	buf, err := json.Marshal(replyResponse{TextEN: en, BackRU: backRU})
	if err != nil {
		panic(err)
	}

	return string(buf)
}

func TestDraftReplyReturnsEnglishAndBackTranslation(t *testing.T) {
	turn := &fakeTurn{answers: []string{replyAnswer(
		"@ivan I rolled `billing` back to the previous tag, see BACK-45 :eyes:",
		"@ivan я откатил `billing` на предыдущий тег, см. BACK-45 :eyes:",
	)}}
	sessions := &fakeSessions{turn: turn}

	ru := "@ivan откатил `billing` на предыдущий тег, см. BACK-45 :eyes:"

	en, backRU, err := NewTranslator(sessions).DraftReply(context.Background(), "thread-7", ru)
	if err != nil {
		t.Fatalf("DraftReply: %v", err)
	}

	if !strings.Contains(en, "rolled `billing` back") {
		t.Errorf("english = %q", en)
	}

	if !strings.Contains(backRU, "откатил") {
		t.Errorf("back translation = %q", backRU)
	}

	// Code spans, identifiers, mentions and emoji must survive both ways.
	for _, want := range []string{"@ivan", "`billing`", "BACK-45", ":eyes:"} {
		if !strings.Contains(en, want) {
			t.Errorf("english lost %q: %s", want, en)
		}

		if !strings.Contains(backRU, want) {
			t.Errorf("back translation lost %q: %s", want, backRU)
		}
	}

	if sessions.threadIDs[0] != "thread-7" {
		t.Fatalf("thread = %q, want thread-7", sessions.threadIDs[0])
	}
}

func TestDraftReplyPromptStatesRulesAndFormat(t *testing.T) {
	turn := &fakeTurn{answers: []string{replyAnswer("ping", "пинг")}}

	if _, _, err := NewTranslator(&fakeSessions{turn: turn}).
		DraftReply(context.Background(), "thread-1", "пинг\n- код `x`"); err != nil {
		t.Fatalf("DraftReply: %v", err)
	}

	prompt := turn.prompts[0]

	for _, want := range []string{
		"на английский", "обратно на русский", "@упоминания", ":emoji_codes:", "бэктиках",
		`{"text_en":"<английский>","back_ru":"<обратный перевод>"}`, "REPLY:",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt: missing %q\n%s", want, prompt)
		}
	}

	// The draft is passed as JSON, so newlines and quotes cannot break the
	// prompt apart.
	if !strings.Contains(prompt, `"пинг\n- код `+"`x`"+`"`) {
		t.Errorf("prompt: draft not JSON-encoded\n%s", prompt)
	}
}

func TestDraftReplyRejectsEmptyText(t *testing.T) {
	sessions := &fakeSessions{turn: &fakeTurn{}}

	for _, ru := range []string{"", "   \n\t"} {
		_, _, err := NewTranslator(sessions).DraftReply(context.Background(), "thread-1", ru)
		if !errors.Is(err, ErrEmptyReply) {
			t.Fatalf("DraftReply(%q) error = %v, want ErrEmptyReply", ru, err)
		}
	}

	if len(sessions.threadIDs) != 0 {
		t.Fatalf("session started for an empty draft")
	}
}

func TestDraftReplyBadAnswers(t *testing.T) {
	tests := []struct {
		name   string
		answer string
	}{
		{name: "not json", answer: "готово, отправляй"},
		{name: "no english", answer: replyAnswer("", "пинг")},
		{name: "no back translation", answer: replyAnswer("ping", "")},
		{name: "broken json", answer: `{"text_en": "ping", `},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessions := &fakeSessions{turn: &fakeTurn{answers: []string{tt.answer}}}

			_, _, err := NewTranslator(sessions).DraftReply(context.Background(), "thread-1", "пинг")

			var respErr *ResponseError
			if !errors.As(err, &respErr) {
				t.Fatalf("error = %v, want *ResponseError", err)
			}
		})
	}
}

func TestDraftReplyUnwrapsCodeFence(t *testing.T) {
	answer := "Вот перевод:\n```json\n" + replyAnswer("ping", "пинг") + "\n```\n"
	sessions := &fakeSessions{turn: &fakeTurn{answers: []string{answer}}}

	en, backRU, err := NewTranslator(sessions).DraftReply(context.Background(), "thread-1", "пинг")
	if err != nil {
		t.Fatalf("DraftReply: %v", err)
	}

	if en != "ping" || backRU != "пинг" {
		t.Fatalf("got (%q, %q), want (ping, пинг)", en, backRU)
	}
}

func TestDraftReplyPropagatesSessionErrors(t *testing.T) {
	wantErr := errors.New("no claude")

	_, _, err := NewTranslator(&fakeSessions{err: wantErr}).
		DraftReply(context.Background(), "thread-1", "пинг")
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}

	turnErr := &TurnError{Subtype: "error_during_execution"}
	sessions := &fakeSessions{turn: &fakeTurn{errs: []error{turnErr}}}

	_, _, err = NewTranslator(sessions).DraftReply(context.Background(), "thread-1", "пинг")
	if !errors.Is(err, error(turnErr)) {
		t.Fatalf("error = %v, want the turn error", err)
	}
}
