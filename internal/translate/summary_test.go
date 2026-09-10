package translate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// summaryAnswer builds the JSON answer claude is expected to produce.
func summaryAnswer(text string) string {
	buf, err := json.Marshal(summaryResponse{Summary: text})
	if err != nil {
		panic(err)
	}

	return string(buf)
}

// longThread returns n translated messages, all long enough to matter.
func longThread(n int) []Message {
	msgs := make([]Message, n)
	for i := range msgs {
		msgs[i] = Message{
			ID:     string(rune('1'+i)) + ".000",
			Author: "Alice",
			Text:   strings.Repeat("deploy is blocked ", 20),
			TextRU: strings.Repeat("деплой заблокирован ", 20),
		}
	}

	return msgs
}

func TestSummaryPromptCarriesTranslationsAndFormat(t *testing.T) {
	turn := &fakeTurn{answers: []string{summaryAnswer("Обсуждают падение деплоя. От меня ждут решения.")}}
	sessions := &fakeSessions{turn: turn}

	msgs := []Message{
		{ID: "1.1", Author: "Alice", Text: "the deploy is stuck", TextRU: "деплой встал"},
		{ID: "2.2", Author: "Bob", Text: "rollback?", TextRU: "откатывать?"},
		{ID: "3.3", Author: "Alice", Text: "@pavel what do you think", TextRU: "@pavel как считаешь"},
	}

	got, err := NewTranslator(sessions).Summarize(context.Background(), "thread-1", msgs)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}

	if got != "Обсуждают падение деплоя. От меня ждут решения." {
		t.Fatalf("summary = %q", got)
	}

	if len(turn.prompts) != 1 {
		t.Fatalf("prompts = %d, want 1", len(turn.prompts))
	}

	prompt := turn.prompts[0]

	for _, want := range []string{
		"Суть", "что ждут от меня", `{"summary":"<текст>"}`, "THREAD:",
		"деплой встал", "откатывать?", "@pavel как считаешь", "Alice", "Bob", "3.3",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt: missing %q\n%s", want, prompt)
		}
	}

	if sessions.threadIDs[0] != "thread-1" {
		t.Fatalf("thread = %q, want thread-1", sessions.threadIDs[0])
	}
}

func TestSummarizeSkipsShortThreads(t *testing.T) {
	tests := []struct {
		name string
		msgs []Message
		want bool
	}{
		{name: "single short message", msgs: []Message{{ID: "1.1", TextRU: "привет"}}, want: false},
		{
			name: "two short messages",
			msgs: []Message{{ID: "1.1", TextRU: "готово?"}, {ID: "2.2", TextRU: "да"}},
			want: false,
		},
		{name: "no messages", msgs: nil, want: false},
		{name: "three short messages", msgs: longThread(3)[:3], want: true},
		{
			name: "two long messages",
			msgs: []Message{
				{ID: "1.1", TextRU: strings.Repeat("длинное объяснение ", 15)},
				{ID: "2.2", TextRU: strings.Repeat("ещё одно длинное ", 15)},
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NeedsSummary(tc.msgs); got != tc.want {
				t.Fatalf("NeedsSummary = %v, want %v", got, tc.want)
			}

			turn := &fakeTurn{answers: []string{summaryAnswer("суть")}}
			sessions := &fakeSessions{turn: turn}

			got, err := NewTranslator(sessions).Summarize(context.Background(), "t", tc.msgs)
			if err != nil {
				t.Fatalf("Summarize: %v", err)
			}

			if tc.want {
				if got == "" {
					t.Fatal("summary is empty, want generated")
				}

				return
			}

			if got != "" {
				t.Fatalf("summary = %q, want empty", got)
			}

			if len(turn.prompts) != 0 {
				t.Fatalf("prompts = %d, want no session use", len(turn.prompts))
			}

			if len(sessions.threadIDs) != 0 {
				t.Fatalf("sessions asked %v, want none", sessions.threadIDs)
			}
		})
	}
}

func TestSummarizeUsesOriginalWhenNotTranslated(t *testing.T) {
	turn := &fakeTurn{answers: []string{summaryAnswer("суть")}}
	msgs := longThread(3)
	msgs[1].TextRU = ""

	if _, err := NewTranslator(&fakeSessions{turn: turn}).Summarize(context.Background(), "t", msgs); err != nil {
		t.Fatalf("Summarize: %v", err)
	}

	if !strings.Contains(turn.prompts[0], "deploy is blocked") {
		t.Fatalf("prompt: untranslated message missing\n%s", turn.prompts[0])
	}
}

func TestSummarizeAnswerErrors(t *testing.T) {
	tests := []struct {
		name   string
		answer string
	}{
		{name: "not json", answer: "тут суть, но без JSON"},
		{name: "broken json", answer: `{"summary": `},
		{name: "empty summary", answer: `{"summary":"   "}`},
		{name: "wrong field", answer: `{"text":"суть"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sessions := &fakeSessions{turn: &fakeTurn{answers: []string{tc.answer}}}

			_, err := NewTranslator(sessions).Summarize(context.Background(), "t", longThread(3))

			var respErr *ResponseError
			if !errors.As(err, &respErr) {
				t.Fatalf("err = %v, want *ResponseError", err)
			}
		})
	}
}

func TestSummarizeAcceptsFencedJSON(t *testing.T) {
	answer := "Вот суть:\n```json\n" + summaryAnswer("Тред про раут заказа.") + "\n```\n"
	sessions := &fakeSessions{turn: &fakeTurn{answers: []string{answer}}}

	got, err := NewTranslator(sessions).Summarize(context.Background(), "t", longThread(3))
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}

	if got != "Тред про раут заказа." {
		t.Fatalf("summary = %q", got)
	}
}

func TestSummarizePropagatesTurnAndSessionErrors(t *testing.T) {
	sessionErr := errors.New("no claude")
	if _, err := NewTranslator(&fakeSessions{err: sessionErr}).
		Summarize(context.Background(), "t", longThread(3)); !errors.Is(err, sessionErr) {
		t.Fatalf("err = %v, want %v", err, sessionErr)
	}

	turnErr := errors.New("turn died")
	sessions := &fakeSessions{turn: &fakeTurn{errs: []error{turnErr}}}

	if _, err := NewTranslator(sessions).
		Summarize(context.Background(), "t", longThread(3)); !errors.Is(err, turnErr) {
		t.Fatalf("err = %v, want %v", err, turnErr)
	}
}

func TestSummaryOutdatedOnlyWhenMessagesChange(t *testing.T) {
	msgs := longThread(3)
	basedOn := SummaryBasedOn(msgs)

	if basedOn == "" {
		t.Fatal("SummaryBasedOn is empty")
	}

	if !strings.HasPrefix(basedOn, "3.000#") {
		t.Fatalf("based_on = %q, want the newest ts as prefix", basedOn)
	}

	if SummaryOutdated(basedOn, msgs) {
		t.Fatal("summary reported outdated for unchanged messages")
	}

	t.Run("no summary yet", func(t *testing.T) {
		if !SummaryOutdated("", msgs) {
			t.Fatal("missing summary reported up to date")
		}
	})

	t.Run("new message", func(t *testing.T) {
		grown := append(longThread(3), Message{ID: "4.000", TextRU: "и ещё вот"})
		if !SummaryOutdated(basedOn, grown) {
			t.Fatal("new message did not outdate the summary")
		}
	})

	t.Run("edited message", func(t *testing.T) {
		edited := longThread(3)
		edited[1].TextRU += " (правка)"

		if !SummaryOutdated(basedOn, edited) {
			t.Fatal("edited message did not outdate the summary")
		}

		if !strings.HasPrefix(SummaryBasedOn(edited), "3.000#") {
			t.Fatal("edit changed the newest ts")
		}
	})

	t.Run("deleted message", func(t *testing.T) {
		if !SummaryOutdated(basedOn, longThread(3)[:2]) {
			t.Fatal("deleted message did not outdate the summary")
		}
	})

	t.Run("thread too short to summarise", func(t *testing.T) {
		short := []Message{{ID: "1.1", TextRU: "ок"}}
		if SummaryOutdated("", short) {
			t.Fatal("short thread wants a summary")
		}
	})
}

func TestSummaryBasedOnEmptyThread(t *testing.T) {
	if got := SummaryBasedOn(nil); got != "" {
		t.Fatalf("SummaryBasedOn(nil) = %q, want empty", got)
	}
}
