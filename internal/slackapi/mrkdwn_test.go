package slackapi_test

import (
	"reflect"
	"testing"

	"github.com/pavelvarganov/slack-threads/internal/slackapi"
)

func TestRenderMrkdwnInline(t *testing.T) {
	t.Parallel()

	names := map[string]string{"U123": "pavel", "C123": "general"}

	tests := []struct {
		name string
		in   string
		want []slackapi.Span
	}{
		{
			name: "plain text",
			in:   "hello there",
			want: []slackapi.Span{{Kind: slackapi.SpanText, Text: "hello there"}},
		},
		{
			name: "labelled link",
			in:   "see <http://example.com/x|the docs> now",
			want: []slackapi.Span{
				{Kind: slackapi.SpanText, Text: "see "},
				{Kind: slackapi.SpanLink, URL: "http://example.com/x", Text: "the docs"},
				{Kind: slackapi.SpanText, Text: " now"},
			},
		},
		{
			name: "bare link",
			in:   "<https://example.com/a?b=1&amp;c=2>",
			want: []slackapi.Span{
				{Kind: slackapi.SpanLink, URL: "https://example.com/a?b=1&c=2", Text: "https://example.com/a?b=1&c=2"},
			},
		},
		{
			name: "mailto link",
			in:   "<mailto:a@b.com|a@b.com>",
			want: []slackapi.Span{
				{Kind: slackapi.SpanLink, URL: "mailto:a@b.com", Text: "a@b.com"},
			},
		},
		{
			name: "known user mention",
			in:   "ping <@U123> please",
			want: []slackapi.Span{
				{Kind: slackapi.SpanText, Text: "ping "},
				{Kind: slackapi.SpanUser, ID: "U123", Text: "@pavel"},
				{Kind: slackapi.SpanText, Text: " please"},
			},
		},
		{
			name: "unknown user mention falls back to id",
			in:   "<@U999>",
			want: []slackapi.Span{{Kind: slackapi.SpanUser, ID: "U999", Text: "@U999"}},
		},
		{
			name: "user mention with inline label",
			in:   "<@U999|bob>",
			want: []slackapi.Span{{Kind: slackapi.SpanUser, ID: "U999", Text: "@bob"}},
		},
		{
			name: "channel mention",
			in:   "in <#C123|general> and <#C123>",
			want: []slackapi.Span{
				{Kind: slackapi.SpanText, Text: "in "},
				{Kind: slackapi.SpanChannel, ID: "C123", Text: "#general"},
				{Kind: slackapi.SpanText, Text: " and "},
				{Kind: slackapi.SpanChannel, ID: "C123", Text: "#general"},
			},
		},
		{
			name: "broadcast",
			in:   "<!here> and <!subteam^S1|@team>",
			want: []slackapi.Span{
				{Kind: slackapi.SpanBroadcast, Text: "@here"},
				{Kind: slackapi.SpanText, Text: " and "},
				{Kind: slackapi.SpanBroadcast, Text: "@@team"},
			},
		},
		{
			name: "html entities",
			in:   "a &amp; b &lt;tag&gt; c",
			want: []slackapi.Span{{Kind: slackapi.SpanText, Text: "a & b <tag> c"}},
		},
		{
			name: "escaped entity is not a mention",
			in:   "&lt;@U123&gt;",
			want: []slackapi.Span{{Kind: slackapi.SpanText, Text: "<@U123>"}},
		},
		{
			name: "inline code",
			in:   "run `go test ./...` first",
			want: []slackapi.Span{
				{Kind: slackapi.SpanText, Text: "run "},
				{Kind: slackapi.SpanCode, Text: "go test ./..."},
				{Kind: slackapi.SpanText, Text: " first"},
			},
		},
		{
			name: "inline code keeps mention syntax verbatim",
			in:   "`<@U123>`",
			want: []slackapi.Span{{Kind: slackapi.SpanCode, Text: "<@U123>"}},
		},
		{
			name: "unterminated backtick stays text",
			in:   "a ` b",
			want: []slackapi.Span{{Kind: slackapi.SpanText, Text: "a ` b"}},
		},
		{
			name: "unterminated angle stays text",
			in:   "5 < 7",
			want: []slackapi.Span{{Kind: slackapi.SpanText, Text: "5 < 7"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			blocks := slackapi.RenderMrkdwn(tc.in, names)
			if len(blocks) != 1 || blocks[0].Kind != slackapi.BlockParagraph {
				t.Fatalf("blocks = %+v, want one paragraph", blocks)
			}

			if !reflect.DeepEqual(blocks[0].Spans, tc.want) {
				t.Fatalf("spans = %+v, want %+v", blocks[0].Spans, tc.want)
			}
		})
	}
}

func TestRenderMrkdwnCodeFence(t *testing.T) {
	t.Parallel()

	in := "before\n```go\nfmt.Println(a &amp; b)\nreturn nil\n```\nafter"

	want := []slackapi.Block{
		{Kind: slackapi.BlockParagraph, Spans: []slackapi.Span{{Kind: slackapi.SpanText, Text: "before"}}},
		{Kind: slackapi.BlockCode, Lang: "go", Text: "fmt.Println(a & b)\nreturn nil"},
		{Kind: slackapi.BlockParagraph, Spans: []slackapi.Span{{Kind: slackapi.SpanText, Text: "after"}}},
	}

	if got := slackapi.RenderMrkdwn(in, nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("blocks = %+v, want %+v", got, want)
	}
}

func TestRenderMrkdwnCodeFenceWithoutLang(t *testing.T) {
	t.Parallel()

	got := slackapi.RenderMrkdwn("```\nline one\nline two\n```", nil)

	want := []slackapi.Block{{Kind: slackapi.BlockCode, Text: "line one\nline two"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("blocks = %+v, want %+v", got, want)
	}
}

func TestRenderMrkdwnUnterminatedFence(t *testing.T) {
	t.Parallel()

	got := slackapi.RenderMrkdwn("oops\n```\nnot closed", nil)

	if len(got) != 2 || got[1].Kind != slackapi.BlockCode || got[1].Text != "not closed" {
		t.Fatalf("blocks = %+v", got)
	}
}

func TestRenderMrkdwnFenceKeepsMarkupVerbatim(t *testing.T) {
	t.Parallel()

	got := slackapi.RenderMrkdwn("```\ncurl <http://x|x> `q`\n```", nil)

	want := []slackapi.Block{{Kind: slackapi.BlockCode, Text: "curl <http://x|x> `q`"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("blocks = %+v, want %+v", got, want)
	}
}

func TestRenderMrkdwnQuotes(t *testing.T) {
	t.Parallel()

	in := "&gt; first quoted\n&gt; second quoted\nreply from <@U1>"

	got := slackapi.RenderMrkdwn(in, map[string]string{"U1": "pavel"})

	want := []slackapi.Block{
		{Kind: slackapi.BlockQuote, Spans: []slackapi.Span{
			{Kind: slackapi.SpanText, Text: "first quoted\nsecond quoted"},
		}},
		{Kind: slackapi.BlockParagraph, Spans: []slackapi.Span{
			{Kind: slackapi.SpanText, Text: "reply from "},
			{Kind: slackapi.SpanUser, ID: "U1", Text: "@pavel"},
		}},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("blocks = %+v, want %+v", got, want)
	}
}

func TestRenderMrkdwnMultilineParagraph(t *testing.T) {
	t.Parallel()

	got := slackapi.RenderMrkdwn("one\ntwo", nil)

	want := []slackapi.Block{{Kind: slackapi.BlockParagraph, Spans: []slackapi.Span{
		{Kind: slackapi.SpanText, Text: "one\ntwo"},
	}}}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("blocks = %+v, want %+v", got, want)
	}
}

func TestRenderMrkdwnEmpty(t *testing.T) {
	t.Parallel()

	if got := slackapi.RenderMrkdwn("", nil); len(got) != 0 {
		t.Fatalf("blocks = %+v, want none", got)
	}

	if got := slackapi.RenderMrkdwn("   \n\n ", nil); len(got) != 0 {
		t.Fatalf("blocks = %+v, want none", got)
	}
}
