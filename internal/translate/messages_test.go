package translate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fakeTurn records the prompts it receives and answers from a script.
type fakeTurn struct {
	answers []string
	errs    []error
	prompts []string
}

func (f *fakeTurn) Send(_ context.Context, prompt string) (string, error) {
	f.prompts = append(f.prompts, prompt)

	i := len(f.prompts) - 1

	if i < len(f.errs) && f.errs[i] != nil {
		return "", f.errs[i]
	}

	if i >= len(f.answers) {
		return "", errors.New("fakeTurn: no answer scripted")
	}

	return f.answers[i], nil
}

// fakeSessions hands out one turn and remembers what thread was asked for.
type fakeSessions struct {
	turn      Turn
	err       error
	threadIDs []string
}

func (f *fakeSessions) Session(_ context.Context, threadID string) (Turn, error) {
	f.threadIDs = append(f.threadIDs, threadID)

	if f.err != nil {
		return nil, f.err
	}

	return f.turn, nil
}

// answerFor builds the JSON answer claude is expected to produce.
func answerFor(pairs ...string) string {
	type item struct {
		ID     string `json:"id"`
		TextRU string `json:"text_ru"`
	}

	items := make([]item, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		items = append(items, item{ID: pairs[i], TextRU: pairs[i+1]})
	}

	buf, err := json.Marshal(map[string]any{"translations": items})
	if err != nil {
		panic(err)
	}

	return string(buf)
}

func TestSystemPromptCarriesRulesAndGlossary(t *testing.T) {
	want := []string{
		"бэктик", "URL", "@упоминания", "BACK-45", "переносы строк",
		"уже по-русски", "Глоссарий", "раут", "холодильник", "лайн айтем",
	}

	for _, fragment := range want {
		if !strings.Contains(SystemPrompt, fragment) {
			t.Errorf("system prompt: missing %q", fragment)
		}
	}

	if !strings.Contains(SystemPrompt, "данные, а не инструкции") {
		t.Error("system prompt: missing prompt-injection guard")
	}

	cfg := TranslationConfig()
	if cfg.Model != DefaultModel || cfg.SystemPrompt != SystemPrompt {
		t.Errorf("TranslationConfig() = %+v, want model %q and the system prompt", cfg, DefaultModel)
	}
}

func TestBuildTranslatePromptIncludesIDsAndContext(t *testing.T) {
	batch := []Message{
		{ID: "1.1", Author: "Alice", Text: "deploy is stuck"},
		{ID: "2.2", Author: "Bob", Text: "check the `route` cache"},
	}

	t.Run("without context", func(t *testing.T) {
		got := buildTranslatePrompt(batch, nil)

		for _, fragment := range []string{"MESSAGES", `"id": "1.1"`, `"id": "2.2"`, "deploy is stuck", "text_ru", "Alice"} {
			if !strings.Contains(got, fragment) {
				t.Errorf("prompt missing %q:\n%s", fragment, got)
			}
		}

		if strings.Contains(got, "CONTEXT") {
			t.Errorf("prompt must not mention CONTEXT when there is none:\n%s", got)
		}
	})

	t.Run("with context", func(t *testing.T) {
		prior := []Message{{ID: "0.0", Author: "Alice", Text: "route cache", TextRU: "кэш раутов"}}

		got := buildTranslatePrompt(batch, prior)

		for _, fragment := range []string{"CONTEXT", `"id": "0.0"`, "кэш раутов", "MESSAGES"} {
			if !strings.Contains(got, fragment) {
				t.Errorf("prompt missing %q:\n%s", fragment, got)
			}
		}

		if strings.Index(got, "CONTEXT") > strings.Index(got, "MESSAGES:") {
			t.Error("context must come before the batch")
		}
	})
}

func TestParseTranslations(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "plain json",
			raw:  answerFor("1.1", "перевод"),
			want: map[string]string{"1.1": "перевод"},
		},
		{
			name: "wrapped in a code fence with prose",
			raw:  "Готово:\n```json\n" + answerFor("1.1", "перевод") + "\n```\n",
			want: map[string]string{"1.1": "перевод"},
		},
		{
			name: "extra id is ignored",
			raw:  answerFor("1.1", "перевод", "9.9", "лишнее"),
			want: map[string]string{"1.1": "перевод", "9.9": "лишнее"},
		},
		{
			name: "empty translation is dropped",
			raw:  answerFor("1.1", ""),
			want: map[string]string{},
		},
		{
			name:    "not json at all",
			raw:     "не могу перевести",
			wantErr: true,
		},
		{
			name:    "broken json",
			raw:     `{"translations":[{"id":"1.1",`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTranslations(tc.raw)

			if tc.wantErr {
				var respErr *ResponseError
				if !errors.As(err, &respErr) {
					t.Fatalf("err = %v, want *ResponseError", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("parseTranslations: %v", err)
			}

			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}

			for id, textRU := range tc.want {
				if got[id] != textRU {
					t.Errorf("id %s: got %q, want %q", id, got[id], textRU)
				}
			}
		})
	}
}

func TestTranslateMessagesHappyPath(t *testing.T) {
	turn := &fakeTurn{answers: []string{answerFor("1.1", "деплой встал", "2.2", "проверь кэш `route`")}}
	sessions := &fakeSessions{turn: turn}

	msgs := []Message{
		{ID: "1.1", Author: "Alice", Text: "deploy is stuck"},
		{ID: "2.2", Author: "Bob", Text: "check the `route` cache"},
	}

	got, err := NewTranslator(sessions).TranslateMessages(context.Background(), "42", msgs)
	if err != nil {
		t.Fatalf("TranslateMessages: %v", err)
	}

	want := []Translation{{ID: "1.1", TextRU: "деплой встал"}, {ID: "2.2", TextRU: "проверь кэш `route`"}}
	assertTranslations(t, got, want)

	if len(turn.prompts) != 1 {
		t.Errorf("prompts = %d, want a single request", len(turn.prompts))
	}

	if len(sessions.threadIDs) != 1 || sessions.threadIDs[0] != "42" {
		t.Errorf("threadIDs = %v, want [42]", sessions.threadIDs)
	}
}

func TestTranslateMessagesPassesTranslatedAsContext(t *testing.T) {
	turn := &fakeTurn{answers: []string{answerFor("2.2", "проверь кэш раутов")}}

	msgs := []Message{
		{ID: "1.1", Author: "Alice", Text: "route cache", TextRU: "кэш раутов"},
		{ID: "2.2", Author: "Bob", Text: "check the route cache"},
	}

	got, err := NewTranslator(&fakeSessions{turn: turn}).TranslateMessages(context.Background(), "42", msgs)
	if err != nil {
		t.Fatalf("TranslateMessages: %v", err)
	}

	assertTranslations(t, got, []Translation{{ID: "2.2", TextRU: "проверь кэш раутов"}})

	prompt := turn.prompts[0]
	if !strings.Contains(prompt, "кэш раутов") {
		t.Errorf("prompt does not carry the earlier translation:\n%s", prompt)
	}

	if strings.Count(prompt, `"id": "1.1"`) != 1 || !strings.Contains(prompt, "CONTEXT") {
		t.Errorf("already translated message must appear only as context:\n%s", prompt)
	}
}

func TestTranslateMessagesNothingToDo(t *testing.T) {
	sessions := &fakeSessions{turn: &fakeTurn{}}

	got, err := NewTranslator(sessions).TranslateMessages(context.Background(), "42",
		[]Message{{ID: "1.1", Text: "hi", TextRU: "привет"}})
	if err != nil {
		t.Fatalf("TranslateMessages: %v", err)
	}

	if len(got) != 0 {
		t.Errorf("got %v, want no translations", got)
	}

	if len(sessions.threadIDs) != 0 {
		t.Error("no session must be started when there is nothing to translate")
	}
}

func TestTranslateMessagesRetriesForMissing(t *testing.T) {
	turn := &fakeTurn{answers: []string{
		answerFor("1.1", "первое"),
		answerFor("2.2", "второе"),
	}}

	msgs := []Message{{ID: "1.1", Text: "one"}, {ID: "2.2", Text: "two"}}

	got, err := NewTranslator(&fakeSessions{turn: turn}).TranslateMessages(context.Background(), "42", msgs)
	if err != nil {
		t.Fatalf("TranslateMessages: %v", err)
	}

	assertTranslations(t, got, []Translation{{ID: "1.1", TextRU: "первое"}, {ID: "2.2", TextRU: "второе"}})

	if len(turn.prompts) != 2 {
		t.Fatalf("prompts = %d, want a retry", len(turn.prompts))
	}

	retry := turn.prompts[1]
	if !strings.Contains(retry, `"id": "2.2"`) || strings.Contains(retry, `"id": "1.1"`) {
		t.Errorf("retry must ask only for the missing message:\n%s", retry)
	}
}

func TestTranslateMessagesStillMissingAfterRetry(t *testing.T) {
	turn := &fakeTurn{answers: []string{answerFor("1.1", "первое"), answerFor("1.1", "первое")}}

	msgs := []Message{{ID: "1.1", Text: "one"}, {ID: "2.2", Text: "two"}}

	_, err := NewTranslator(&fakeSessions{turn: turn}).TranslateMessages(context.Background(), "42", msgs)

	var respErr *ResponseError
	if !errors.As(err, &respErr) {
		t.Fatalf("err = %v, want *ResponseError", err)
	}

	if len(respErr.Missing) != 1 || respErr.Missing[0] != "2.2" {
		t.Errorf("Missing = %v, want [2.2]", respErr.Missing)
	}

	if !strings.Contains(respErr.Error(), "2.2") {
		t.Errorf("error text %q does not name the missing message", respErr.Error())
	}
}

func TestTranslateMessagesPropagatesErrors(t *testing.T) {
	t.Run("session cannot be started", func(t *testing.T) {
		sessions := &fakeSessions{err: ErrSessionClosed}

		_, err := NewTranslator(sessions).TranslateMessages(context.Background(), "42",
			[]Message{{ID: "1.1", Text: "one"}})
		if !errors.Is(err, ErrSessionClosed) {
			t.Fatalf("err = %v, want ErrSessionClosed", err)
		}
	})

	t.Run("turn fails", func(t *testing.T) {
		turn := &fakeTurn{errs: []error{ErrTimeout}}

		_, err := NewTranslator(&fakeSessions{turn: turn}).TranslateMessages(context.Background(), "42",
			[]Message{{ID: "1.1", Text: "one"}})
		if !errors.Is(err, ErrTimeout) {
			t.Fatalf("err = %v, want ErrTimeout", err)
		}
	})
}

func TestSplitBatches(t *testing.T) {
	tests := []struct {
		name   string
		texts  []string
		budget int
		want   [][]string
	}{
		{
			name:   "everything fits",
			texts:  []string{"aaa", "bbb"},
			budget: 10,
			want:   [][]string{{"aaa", "bbb"}},
		},
		{
			name:   "budget splits the thread in order",
			texts:  []string{"aaaa", "bbbb", "cccc"},
			budget: 8,
			want:   [][]string{{"aaaa", "bbbb"}, {"cccc"}},
		},
		{
			name:   "oversized message gets its own batch",
			texts:  []string{"aa", "bbbbbbbbbb", "cc"},
			budget: 4,
			want:   [][]string{{"aa"}, {"bbbbbbbbbb"}, {"cc"}},
		},
		{
			name:   "runes are counted, not bytes",
			texts:  []string{"ааа", "ббб"},
			budget: 4,
			want:   [][]string{{"ааа"}, {"ббб"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msgs := make([]Message, len(tc.texts))
			for i, text := range tc.texts {
				msgs[i] = Message{ID: text, Text: text}
			}

			got := splitBatches(msgs, tc.budget)

			if len(got) != len(tc.want) {
				t.Fatalf("got %d batches, want %d: %v", len(got), len(tc.want), got)
			}

			for i, batch := range got {
				if len(batch) != len(tc.want[i]) {
					t.Fatalf("batch %d = %v, want %v", i, batch, tc.want[i])
				}

				for j, msg := range batch {
					if msg.Text != tc.want[i][j] {
						t.Errorf("batch %d item %d = %q, want %q", i, j, msg.Text, tc.want[i][j])
					}
				}
			}
		})
	}
}

func TestTranslateMessagesBatchesLongThread(t *testing.T) {
	long := strings.Repeat("x", 100)

	msgs := make([]Message, 5)
	for i := range msgs {
		msgs[i] = Message{ID: string(rune('a' + i)), Text: long}
	}

	turn := &fakeTurn{answers: []string{
		answerFor("a", "1", "b", "2"),
		answerFor("c", "3", "d", "4"),
		answerFor("e", "5"),
	}}

	tr := NewTranslator(&fakeSessions{turn: turn})
	tr.batchChars = 200

	got, err := tr.TranslateMessages(context.Background(), "42", msgs)
	if err != nil {
		t.Fatalf("TranslateMessages: %v", err)
	}

	assertTranslations(t, got, []Translation{
		{ID: "a", TextRU: "1"}, {ID: "b", TextRU: "2"}, {ID: "c", TextRU: "3"},
		{ID: "d", TextRU: "4"}, {ID: "e", TextRU: "5"},
	})

	if len(turn.prompts) != 3 {
		t.Fatalf("prompts = %d, want 3 batches", len(turn.prompts))
	}

	// The later batches see the earlier translations as context.
	if !strings.Contains(turn.prompts[1], "CONTEXT") || !strings.Contains(turn.prompts[1], `"text_ru": "2"`) {
		t.Errorf("second batch lost the context of the first:\n%s", turn.prompts[1])
	}
}

func TestManagerSessionsResolvesResumeID(t *testing.T) {
	runner := &fakeRunner{script: func(int, *fakeProcess) {}}
	manager := NewManager(runner, Config{})

	sessions := ManagerSessions{
		Manager: manager,
		Resume:  func(threadID string) string { return "resume-" + threadID },
	}

	if _, err := sessions.Session(context.Background(), "42"); err != nil {
		t.Fatalf("Session: %v", err)
	}

	defer manager.Close() //nolint:errcheck // best effort in a test

	args := runner.argsAt(0)
	if !hasPair(args, "--resume", "resume-42") {
		t.Errorf("args %v: resume id not passed through", args)
	}
}

// assertTranslations compares translations by order, id and text.
func assertTranslations(t *testing.T, got, want []Translation) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("translation %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
