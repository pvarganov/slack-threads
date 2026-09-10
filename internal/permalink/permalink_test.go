package permalink_test

import (
	"errors"
	"testing"

	"github.com/pavelvarganov/slack-threads/internal/permalink"
)

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want permalink.Link
	}{
		{
			name: "root message",
			raw:  "https://overgearcom.slack.com/archives/C024BE91L/p1788872615903009",
			want: permalink.Link{
				Workspace: "overgearcom",
				ChannelID: "C024BE91L",
				ThreadTS:  "1788872615.903009",
				FocusTS:   "1788872615.903009",
			},
		},
		{
			name: "reply with thread_ts and cid in query",
			raw:  "https://overgearcom.slack.com/archives/C024BE91L/p1788872700123456?thread_ts=1788872615.903009&cid=C024BE91L",
			want: permalink.Link{
				Workspace: "overgearcom",
				ChannelID: "C024BE91L",
				ThreadTS:  "1788872615.903009",
				FocusTS:   "1788872700.123456",
			},
		},
		{
			name: "private group",
			raw:  "https://overgearcom.slack.com/archives/GQWERTY12/p1788872615903009",
			want: permalink.Link{
				Workspace: "overgearcom",
				ChannelID: "GQWERTY12",
				ThreadTS:  "1788872615.903009",
				FocusTS:   "1788872615.903009",
			},
		},
		{
			name: "direct message",
			raw:  "https://overgearcom.slack.com/archives/D01AB2CD3/p1788872615903009",
			want: permalink.Link{
				Workspace: "overgearcom",
				ChannelID: "D01AB2CD3",
				ThreadTS:  "1788872615.903009",
				FocusTS:   "1788872615.903009",
			},
		},
		{
			name: "client thread link",
			raw:  "https://app.slack.com/client/T024BE7LD/C024BE91L/thread/C024BE91L-1788872615.903009",
			want: permalink.Link{
				TeamID:    "T024BE7LD",
				ChannelID: "C024BE91L",
				ThreadTS:  "1788872615.903009",
				FocusTS:   "1788872615.903009",
			},
		},
		{
			name: "trailing slash and no scheme",
			raw:  "  overgearcom.slack.com/archives/c024be91l/p1788872615903009/  ",
			want: permalink.Link{
				Workspace: "overgearcom",
				ChannelID: "C024BE91L",
				ThreadTS:  "1788872615.903009",
				FocusTS:   "1788872615.903009",
			},
		},
		{
			name: "extra query parameters are ignored",
			raw:  "https://overgearcom.slack.com/archives/C024BE91L/p1788872615903009?web=1&utm_source=mail",
			want: permalink.Link{
				Workspace: "overgearcom",
				ChannelID: "C024BE91L",
				ThreadTS:  "1788872615.903009",
				FocusTS:   "1788872615.903009",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := permalink.Parse(tt.raw)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tt.raw, err)
			}

			if got != tt.want {
				t.Fatalf("Parse(%q) = %+v, want %+v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		wantErr error
	}{
		{name: "empty", raw: "", wantErr: permalink.ErrEmpty},
		{name: "blank", raw: "   \t ", wantErr: permalink.ErrEmpty},
		{name: "garbage", raw: "just some text", wantErr: permalink.ErrNotSlack},
		{name: "other host", raw: "https://example.com/archives/C024BE91L/p1788872615903009", wantErr: permalink.ErrNotSlack},
		{name: "lookalike host", raw: "https://notslack.com/archives/C1/p1788872615903009", wantErr: permalink.ErrNotSlack},
		{name: "unsupported path", raw: "https://overgearcom.slack.com/files/U1/F1/report.pdf", wantErr: permalink.ErrUnsupportedShape},
		{name: "root path", raw: "https://overgearcom.slack.com/", wantErr: permalink.ErrUnsupportedShape},
		{name: "channel archive link", raw: "https://overgearcom.slack.com/archives/C024BE91L", wantErr: permalink.ErrChannelOnly},
		{name: "client channel link", raw: "https://app.slack.com/client/T024BE7LD/C024BE91L", wantErr: permalink.ErrChannelOnly},
		{name: "bad channel id", raw: "https://overgearcom.slack.com/archives/XYZ/p1788872615903009", wantErr: permalink.ErrBadChannelID},
		{name: "bad ts segment", raw: "https://overgearcom.slack.com/archives/C024BE91L/1788872615903009", wantErr: permalink.ErrBadTimestamp},
		{name: "short ts segment", raw: "https://overgearcom.slack.com/archives/C024BE91L/p17888726", wantErr: permalink.ErrBadTimestamp},
		{name: "bad thread_ts", raw: "https://overgearcom.slack.com/archives/C024BE91L/p1788872615903009?thread_ts=nope", wantErr: permalink.ErrBadTimestamp},
		{name: "bad client thread segment", raw: "https://app.slack.com/client/T024BE7LD/C024BE91L/thread/C024BE91L", wantErr: permalink.ErrBadTimestamp},
		{name: "bad team id", raw: "https://app.slack.com/client/nope/C024BE91L/thread/C024BE91L-1788872615.903009", wantErr: permalink.ErrInvalid},
		{name: "cid contradicts path", raw: "https://overgearcom.slack.com/archives/C024BE91L/p1788872615903009?cid=C999XXX", wantErr: permalink.ErrInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := permalink.Parse(tt.raw)
			if err == nil {
				t.Fatalf("Parse(%q) = nil error, want %v", tt.raw, tt.wantErr)
			}

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Parse(%q) error = %v, want %v", tt.raw, err, tt.wantErr)
			}

			if !errors.Is(err, permalink.ErrInvalid) {
				t.Fatalf("Parse(%q) error %v does not wrap ErrInvalid", tt.raw, err)
			}
		})
	}
}

func TestParseChannelOnlyKeepsConversation(t *testing.T) {
	t.Parallel()

	got, err := permalink.Parse("https://overgearcom.slack.com/archives/C024BE91L")
	if !errors.Is(err, permalink.ErrChannelOnly) {
		t.Fatalf("error = %v, want ErrChannelOnly", err)
	}

	if got.ChannelID != "C024BE91L" || got.Workspace != "overgearcom" {
		t.Fatalf("got %+v, want channel and workspace preserved", got)
	}

	if got.ThreadTS != "" || got.FocusTS != "" {
		t.Fatalf("got %+v, want empty timestamps", got)
	}
}

func TestPathTimestampConversion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want string
	}{
		{raw: "https://overgearcom.slack.com/archives/C1AB/p1788872615903009", want: "1788872615.903009"},
		{raw: "https://overgearcom.slack.com/archives/C1AB/p1600000000000001", want: "1600000000.000001"},
		{raw: "https://overgearcom.slack.com/archives/C1AB/p17888726159030090", want: "17888726159.030090"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			got, err := permalink.Parse(tt.raw)
			if err != nil {
				t.Fatalf("Parse(%q) returned error: %v", tt.raw, err)
			}

			if got.FocusTS != tt.want {
				t.Fatalf("FocusTS = %q, want %q", got.FocusTS, tt.want)
			}
		})
	}
}

func TestLinkInThread(t *testing.T) {
	t.Parallel()

	root, err := permalink.Parse("https://overgearcom.slack.com/archives/C1AB/p1788872615903009")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if root.InThread() {
		t.Fatal("root link reported as in-thread")
	}

	reply, err := permalink.Parse(
		"https://overgearcom.slack.com/archives/C1AB/p1788872700123456?thread_ts=1788872615.903009",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reply.InThread() {
		t.Fatal("reply link reported as root")
	}
}

func TestBuild(t *testing.T) {
	t.Parallel()

	got, err := permalink.Build("overgearcom", "C024BE91L", "1788872615.903009")
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	want := "https://overgearcom.slack.com/archives/C024BE91L/p1788872615903009"
	if got != want {
		t.Fatalf("Build = %q, want %q", got, want)
	}
}

func TestBuildReply(t *testing.T) {
	t.Parallel()

	got, err := permalink.BuildReply("overgearcom", "C024BE91L", "1788872615.903009", "1788872700.123456")
	if err != nil {
		t.Fatalf("BuildReply returned error: %v", err)
	}

	want := "https://overgearcom.slack.com/archives/C024BE91L/p1788872700123456" +
		"?cid=C024BE91L&thread_ts=1788872615.903009"
	if got != want {
		t.Fatalf("BuildReply = %q, want %q", got, want)
	}
}

func TestBuildRoundTrip(t *testing.T) {
	t.Parallel()

	built, err := permalink.BuildReply("overgearcom", "C024BE91L", "1788872615.903009", "1788872700.123456")
	if err != nil {
		t.Fatalf("BuildReply returned error: %v", err)
	}

	got, err := permalink.Parse(built)
	if err != nil {
		t.Fatalf("Parse(%q) returned error: %v", built, err)
	}

	want := permalink.Link{
		Workspace: "overgearcom",
		ChannelID: "C024BE91L",
		ThreadTS:  "1788872615.903009",
		FocusTS:   "1788872700.123456",
	}

	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

func TestBuildErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		workspace string
		channelID string
		ts        string
		wantErr   error
	}{
		{name: "empty workspace", workspace: "", channelID: "C1AB", ts: "1788872615.903009", wantErr: permalink.ErrInvalid},
		{name: "bad channel", workspace: "overgearcom", channelID: "nope", ts: "1788872615.903009", wantErr: permalink.ErrBadChannelID},
		{name: "bad ts", workspace: "overgearcom", channelID: "C1AB", ts: "1788872615903009", wantErr: permalink.ErrBadTimestamp},
		{name: "empty ts", workspace: "overgearcom", channelID: "C1AB", ts: "", wantErr: permalink.ErrBadTimestamp},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := permalink.Build(tt.workspace, tt.channelID, tt.ts); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Build error = %v, want %v", err, tt.wantErr)
			}
		})
	}

	_, err := permalink.BuildReply("overgearcom", "C1AB", "nope", "1788872615.903009")
	if !errors.Is(err, permalink.ErrBadTimestamp) {
		t.Fatalf("BuildReply error = %v, want ErrBadTimestamp", err)
	}
}
