package slackapi

import (
	"strings"
)

// BlockKind is the shape of a rendered block.
type BlockKind string

const (
	// BlockParagraph is a run of ordinary lines.
	BlockParagraph BlockKind = "paragraph"
	// BlockQuote is a run of lines quoted with "> ".
	BlockQuote BlockKind = "quote"
	// BlockCode is a fenced code block.
	BlockCode BlockKind = "code"
)

// SpanKind is the shape of an inline piece of a block.
type SpanKind string

const (
	// SpanText is plain text with entities already decoded.
	SpanText SpanKind = "text"
	// SpanCode is inline code between single backticks.
	SpanCode SpanKind = "code"
	// SpanLink is a link; Text holds the label, URL the target.
	SpanLink SpanKind = "link"
	// SpanUser is a user mention; ID holds the Slack user ID.
	SpanUser SpanKind = "user"
	// SpanChannel is a channel mention; ID holds the channel ID.
	SpanChannel SpanKind = "channel"
	// SpanBroadcast is @here / @channel / @everyone.
	SpanBroadcast SpanKind = "broadcast"
)

// Span is one inline piece of a block.
type Span struct {
	// Kind tells the frontend how to render the span.
	Kind SpanKind `json:"kind"`
	// Text is what the reader sees.
	Text string `json:"text,omitempty"`
	// URL is the link target, set for SpanLink.
	URL string `json:"url,omitempty"`
	// ID is the mentioned user or channel ID.
	ID string `json:"id,omitempty"`
}

// Block is one paragraph, quote or code block of a rendered message.
type Block struct {
	// Kind tells the frontend how to render the block.
	Kind BlockKind `json:"kind"`
	// Lang is the language tag of a fenced code block, if Slack kept one.
	Lang string `json:"lang,omitempty"`
	// Text is the verbatim body of a code block.
	Text string `json:"text,omitempty"`
	// Spans are the inline pieces of a paragraph or quote.
	Spans []Span `json:"spans,omitempty"`
}

// RenderMrkdwn turns Slack mrkdwn into blocks the frontend can render
// without knowing Slack's syntax. names maps user and channel IDs onto
// labels; unknown IDs fall back to the ID itself.
func RenderMrkdwn(text string, names map[string]string) []Block {
	var blocks []Block

	for _, segment := range splitFences(text) {
		if segment.code {
			blocks = append(blocks, codeBlock(segment.text))

			continue
		}

		blocks = append(blocks, textBlocks(segment.text, names)...)
	}

	return blocks
}

// segment is a slice of the message that is either fenced code or not.
type segment struct {
	text string
	code bool
}

// splitFences cuts the message on triple backticks. An unterminated fence
// runs to the end of the message, which is what Slack itself renders.
func splitFences(text string) []segment {
	const fence = "```"

	var (
		out  []segment
		rest = text
	)

	for {
		start := strings.Index(rest, fence)
		if start < 0 {
			if rest != "" {
				out = append(out, segment{text: rest})
			}

			return out
		}

		if start > 0 {
			out = append(out, segment{text: rest[:start]})
		}

		rest = rest[start+len(fence):]

		end := strings.Index(rest, fence)
		if end < 0 {
			out = append(out, segment{text: rest, code: true})

			return out
		}

		out = append(out, segment{text: rest[:end], code: true})
		rest = rest[end+len(fence):]
	}
}

// codeBlock builds a code block, lifting a language tag off the first line
// when the fence carried one.
func codeBlock(body string) Block {
	body = strings.TrimPrefix(body, "\n")
	body = strings.TrimRight(body, "\n")

	lang := ""

	if head, tail, ok := strings.Cut(body, "\n"); ok {
		if candidate := strings.TrimSpace(head); isLangTag(candidate) {
			lang = candidate
			body = tail
		}
	}

	return Block{Kind: BlockCode, Lang: lang, Text: decodeEntities(body)}
}

// isLangTag reports whether a fence's first line is a language name rather
// than the first line of code.
func isLangTag(s string) bool {
	if s == "" || len(s) > 16 {
		return false
	}

	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '+', r == '-', r == '#', r == '.':
		default:
			return false
		}
	}

	return true
}

// textBlocks splits non-code text into paragraphs and quotes. Newlines that
// only separate the text from an adjacent code fence are dropped.
func textBlocks(text string, names map[string]string) []Block {
	text = strings.Trim(text, "\n")

	var (
		out     []Block
		buffer  []string
		inQuote bool
	)

	flush := func() {
		if len(buffer) == 0 {
			return
		}

		kind := BlockParagraph
		if inQuote {
			kind = BlockQuote
		}

		body := strings.Join(buffer, "\n")
		buffer = nil

		if strings.TrimSpace(body) == "" {
			return
		}

		out = append(out, Block{Kind: kind, Spans: renderSpans(body, names)})
	}

	for _, line := range strings.Split(text, "\n") {
		quoted, body := stripQuote(line)

		if quoted != inQuote {
			flush()

			inQuote = quoted
		}

		buffer = append(buffer, body)
	}

	flush()

	return out
}

// stripQuote removes a leading quote marker, in either its raw or its
// HTML-escaped form.
func stripQuote(line string) (bool, string) {
	for _, prefix := range []string{"&gt;", ">"} {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			return true, strings.TrimPrefix(rest, " ")
		}
	}

	return false, line
}

// renderSpans tokenises inline mrkdwn: `code`, <…> entities and plain text.
func renderSpans(text string, names map[string]string) []Span {
	var (
		spans []Span
		plain strings.Builder
	)

	flush := func() {
		if plain.Len() == 0 {
			return
		}

		spans = append(spans, Span{Kind: SpanText, Text: decodeEntities(plain.String())})
		plain.Reset()
	}

	for i := 0; i < len(text); {
		switch text[i] {
		case '`':
			if end := strings.IndexByte(text[i+1:], '`'); end >= 0 {
				flush()

				body := text[i+1 : i+1+end]
				spans = append(spans, Span{Kind: SpanCode, Text: decodeEntities(body)})
				i += end + 2

				continue
			}
		case '<':
			if end := strings.IndexByte(text[i:], '>'); end > 0 {
				flush()

				spans = append(spans, angleSpan(text[i+1:i+end], names))
				i += end + 1

				continue
			}
		}

		plain.WriteByte(text[i])
		i++
	}

	flush()

	return spans
}

// angleSpan renders one <…> construct: a mention, a channel link or a URL.
func angleSpan(body string, names map[string]string) Span {
	target, label, hasLabel := strings.Cut(body, "|")
	label = decodeEntities(label)

	switch {
	case strings.HasPrefix(target, "@"):
		id := target[1:]

		return Span{Kind: SpanUser, ID: id, Text: mentionLabel(id, label, names, "@")}
	case strings.HasPrefix(target, "#"):
		id := target[1:]

		return Span{Kind: SpanChannel, ID: id, Text: mentionLabel(id, label, names, "#")}
	case strings.HasPrefix(target, "!"):
		name := target[1:]
		if hasLabel && label != "" {
			name = label
		}

		if idx := strings.IndexByte(name, '^'); idx >= 0 {
			name = name[:idx]
		}

		return Span{Kind: SpanBroadcast, Text: "@" + name}
	}

	url := decodeEntities(target)
	if !hasLabel || label == "" {
		label = url
	}

	return Span{Kind: SpanLink, URL: url, Text: label}
}

// mentionLabel picks the text of a mention: the inline label Slack supplied,
// then the resolved name, then the bare ID.
func mentionLabel(id, label string, names map[string]string, sigil string) string {
	if label != "" {
		return sigil + label
	}

	if name, ok := names[id]; ok && name != "" {
		return sigil + name
	}

	return sigil + id
}

// entityReplacer decodes the three entities Slack escapes in message text.
// The ampersand goes last so "&amp;lt;" does not turn into "<".
var entityReplacer = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&")

// decodeEntities undoes Slack's escaping.
func decodeEntities(s string) string { return entityReplacer.Replace(s) }
