// Package permalink parses and builds Slack message permalinks.
package permalink

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Parse errors. All of them wrap ErrInvalid so callers may check the whole
// family with a single errors.Is.
var (
	// ErrInvalid is the root of every parse error of this package.
	ErrInvalid = errors.New("permalink: invalid slack link")
	// ErrEmpty is returned for an empty or blank-only input.
	ErrEmpty = fmt.Errorf("%w: empty input", ErrInvalid)
	// ErrNotSlack is returned when the URL does not point at slack.com.
	ErrNotSlack = fmt.Errorf("%w: not a slack.com URL", ErrInvalid)
	// ErrUnsupportedShape is returned for a slack.com URL whose path is not a
	// message or channel link (files, canvases, apps, ...).
	ErrUnsupportedShape = fmt.Errorf("%w: unsupported link shape", ErrInvalid)
	// ErrChannelOnly is returned for a link that identifies a conversation but
	// no message inside it.
	ErrChannelOnly = fmt.Errorf("%w: link points to a conversation, not a message", ErrInvalid)
	// ErrBadChannelID is returned when the conversation ID is malformed.
	ErrBadChannelID = fmt.Errorf("%w: malformed conversation ID", ErrInvalid)
	// ErrBadTimestamp is returned when the message timestamp is malformed.
	ErrBadTimestamp = fmt.Errorf("%w: malformed message timestamp", ErrInvalid)
)

// Link is a parsed Slack message permalink.
type Link struct {
	// Workspace is the slack.com subdomain ("overgearcom" for
	// overgearcom.slack.com). It is empty for app.slack.com links, which carry
	// a team ID instead.
	Workspace string
	// TeamID is the T… identifier carried by app.slack.com links. Empty for
	// workspace-subdomain links.
	TeamID string
	// ChannelID is the conversation ID: C… public channel, G… private channel
	// or multi-person DM, D… direct message.
	ChannelID string
	// ThreadTS is the timestamp of the thread root. For a link to a root
	// message it equals FocusTS.
	ThreadTS string
	// FocusTS is the timestamp of the message the link points at.
	FocusTS string
}

// InThread reports whether the link points at a reply rather than at the
// thread root.
func (l Link) InThread() bool {
	return l.FocusTS != l.ThreadTS
}

var (
	channelIDRe = regexp.MustCompile(`^[CGD][A-Z0-9]{2,}$`)
	teamIDRe    = regexp.MustCompile(`^[TE][A-Z0-9]{2,}$`)
	tsRe        = regexp.MustCompile(`^\d{10,}\.\d{6}$`)
	pathTSRe    = regexp.MustCompile(`^p(\d{16,})$`)
)

// Parse turns a Slack permalink into a Link. Supported shapes:
//
//	https://<workspace>.slack.com/archives/<channel>/p<ts>[?thread_ts=…]
//	https://app.slack.com/client/<team>/<channel>/thread/<channel>-<ts>
//
// Links that identify only a conversation return ErrChannelOnly with the
// conversation fields still filled in, so the caller may report which channel
// was recognised.
func Parse(raw string) (Link, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Link{}, ErrEmpty
	}

	if !strings.Contains(raw, "//") {
		raw = "https://" + raw
	}

	// A string that does not even parse as a URL cannot be a Slack link; the
	// caller only needs to know that, not the URL-parser wording.
	u, err := url.Parse(raw)
	if err != nil {
		return Link{}, fmt.Errorf("%w (%s)", ErrNotSlack, err.Error())
	}

	host := strings.ToLower(u.Hostname())
	if host != "slack.com" && !strings.HasSuffix(host, ".slack.com") {
		return Link{}, ErrNotSlack
	}

	segs := splitPath(u.EscapedPath())
	if len(segs) == 0 {
		return Link{}, ErrUnsupportedShape
	}

	switch segs[0] {
	case "archives":
		return parseArchives(host, segs, u.Query())
	case "client":
		return parseClient(segs)
	default:
		return Link{}, ErrUnsupportedShape
	}
}

// parseArchives handles <workspace>.slack.com/archives/<channel>[/p<ts>].
func parseArchives(host string, segs []string, query url.Values) (Link, error) {
	l := Link{Workspace: workspaceFromHost(host)}

	if len(segs) < 2 {
		return l, ErrChannelOnly
	}

	channelID, err := normalizeChannelID(segs[1])
	if err != nil {
		return l, err
	}

	l.ChannelID = channelID

	if len(segs) < 3 {
		return l, ErrChannelOnly
	}

	focusTS, err := tsFromPathSegment(segs[2])
	if err != nil {
		return l, err
	}

	l.FocusTS = focusTS
	l.ThreadTS = focusTS

	if threadTS := query.Get("thread_ts"); threadTS != "" {
		if !tsRe.MatchString(threadTS) {
			return l, fmt.Errorf("%w: thread_ts %q", ErrBadTimestamp, threadTS)
		}

		l.ThreadTS = threadTS
	}

	if cid := query.Get("cid"); cid != "" && !strings.EqualFold(cid, l.ChannelID) {
		return l, fmt.Errorf("%w: cid %q contradicts path channel %q", ErrInvalid, cid, l.ChannelID)
	}

	return l, nil
}

// parseClient handles app.slack.com/client/<team>/<channel>[/thread/<channel>-<ts>].
func parseClient(segs []string) (Link, error) {
	var l Link

	if len(segs) < 2 {
		return l, ErrUnsupportedShape
	}

	teamID := strings.ToUpper(segs[1])
	if !teamIDRe.MatchString(teamID) {
		return l, fmt.Errorf("%w: team %q", ErrInvalid, segs[1])
	}

	l.TeamID = teamID

	if len(segs) < 3 {
		return l, ErrChannelOnly
	}

	channelID, err := normalizeChannelID(segs[2])
	if err != nil {
		return l, err
	}

	l.ChannelID = channelID

	if len(segs) < 5 || segs[3] != "thread" {
		return l, ErrChannelOnly
	}

	// The thread segment is "<channel>-<ts>"; the channel part repeats the one
	// already parsed, so only the timestamp is taken from it.
	_, ts, ok := strings.Cut(segs[4], "-")
	if !ok || !tsRe.MatchString(ts) {
		return l, fmt.Errorf("%w: thread segment %q", ErrBadTimestamp, segs[4])
	}

	l.ThreadTS = ts
	l.FocusTS = ts

	return l, nil
}

// Build assembles a permalink to a single message.
func Build(workspace, channelID, ts string) (string, error) {
	base, err := buildBase(workspace, channelID, ts)
	if err != nil {
		return "", err
	}

	return base, nil
}

// BuildReply assembles a permalink to a reply inside a thread. Slack needs both
// thread_ts and cid on such links so that opening them focuses the thread.
func BuildReply(workspace, channelID, threadTS, ts string) (string, error) {
	base, err := buildBase(workspace, channelID, ts)
	if err != nil {
		return "", err
	}

	if !tsRe.MatchString(threadTS) {
		return "", fmt.Errorf("%w: thread_ts %q", ErrBadTimestamp, threadTS)
	}

	q := url.Values{"thread_ts": {threadTS}, "cid": {channelID}}

	return base + "?" + q.Encode(), nil
}

func buildBase(workspace, channelID, ts string) (string, error) {
	workspace = strings.ToLower(strings.TrimSpace(workspace))
	if workspace == "" {
		return "", fmt.Errorf("%w: empty workspace", ErrInvalid)
	}

	normalized, err := normalizeChannelID(channelID)
	if err != nil {
		return "", err
	}

	seg, err := pathSegmentFromTS(ts)
	if err != nil {
		return "", err
	}

	return "https://" + workspace + ".slack.com/archives/" + normalized + "/" + seg, nil
}

// tsFromPathSegment converts "p1788872615903009" into "1788872615.903009".
func tsFromPathSegment(seg string) (string, error) {
	m := pathTSRe.FindStringSubmatch(seg)
	if m == nil {
		return "", fmt.Errorf("%w: path segment %q", ErrBadTimestamp, seg)
	}

	digits := m[1]
	split := len(digits) - 6

	return digits[:split] + "." + digits[split:], nil
}

// pathSegmentFromTS converts "1788872615.903009" into "p1788872615903009".
func pathSegmentFromTS(ts string) (string, error) {
	ts = strings.TrimSpace(ts)
	if !tsRe.MatchString(ts) {
		return "", fmt.Errorf("%w: timestamp %q", ErrBadTimestamp, ts)
	}

	return "p" + strings.Replace(ts, ".", "", 1), nil
}

func normalizeChannelID(raw string) (string, error) {
	id := strings.ToUpper(strings.TrimSpace(raw))
	if !channelIDRe.MatchString(id) {
		return "", fmt.Errorf("%w: %q", ErrBadChannelID, raw)
	}

	return id, nil
}

func workspaceFromHost(host string) string {
	sub := strings.TrimSuffix(host, ".slack.com")
	if sub == host || sub == "app" || sub == "www" {
		return ""
	}

	return sub
}

func splitPath(path string) []string {
	parts := strings.Split(path, "/")

	segs := make([]string, 0, len(parts))

	for _, p := range parts {
		if p == "" {
			continue
		}

		if unescaped, err := url.PathUnescape(p); err == nil {
			p = unescaped
		}

		segs = append(segs, p)
	}

	return segs
}
