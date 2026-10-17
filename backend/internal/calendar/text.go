// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package calendar

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/idna"

	"github.com/schotek/malachi/backend/internal/board"
	"github.com/schotek/malachi/backend/internal/sanitize"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Length caps of cleaned strings (docs/calendar.md §3.9).
const (
	MaxTitleBytes    = api.MaxEventTitleBytes
	MaxLocationBytes = api.MaxEventLocationBytes
	MaxNameBytes     = 256
	MaxNotesBytes    = api.MaxEventNotesBytes
	MaxUIDBytes      = 255
	// MaxLinkBytes caps one link (Links, VideoCall).
	MaxLinkBytes = 2048
	// MaxLinks caps the links of one event.
	MaxLinks = 50
)

// CleanLine makes a one-line string from a server, an .ics or another
// person safe to show as plain text: valid UTF-8, no control, format,
// bidi, invisible or default-ignorable characters (ZWJ and ZWNJ kept only
// between two other characters, as board.CleanText does), every run of
// whitespace (line breaks and line separators included) one space,
// trimmed, cut to max bytes on a character boundary (max ≤ 0: no cap).
func CleanLine(s string, max int) string {
	return capBytes(strings.Join(strings.Fields(board.CleanText(s)), " "), max)
}

// CleanBlock cleans a multi-line string (notes) like CleanLine but keeps
// "\n": CRLF, CR, NEL, U+2028 and U+2029 become "\n", other whitespace a
// space; each line loses its trailing whitespace (its indentation stays),
// at most one empty line in a row and none at either end; the result is
// cut to max bytes on a character boundary.
func CleanBlock(s string, max int) string {
	var b strings.Builder
	blank := false
	for line := range strings.SplitSeq(board.CleanText(s), "\n") {
		line = strings.TrimRightFunc(line, unicode.IsSpace)
		if line == "" {
			blank = b.Len() > 0
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
			if blank {
				b.WriteByte('\n')
			}
		}
		blank = false
		b.WriteString(line)
		if max > 0 && b.Len() > max {
			break
		}
	}
	return strings.TrimRightFunc(capBytes(b.String(), max), unicode.IsSpace)
}

// capBytes cuts s to at most n bytes on a rune boundary (n ≤ 0: none).
func capBytes(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	i := n
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i]
}

// NotesFromHTML turns an HTML description (a Graph event body) into the
// plain text of Event.Notes: the sanitiser's text rendering of the
// sanitised tree (D11), cleaned with CleanBlock. The sanitiser failing is
// an empty string, never the input.
func NotesFromHTML(html string) string {
	if strings.TrimSpace(html) == "" {
		return ""
	}
	out, err := sanitize.Sanitize(sanitize.Input{HTML: html, Mode: sanitize.ModeView, Policy: api.RemoteBlock})
	if err != nil {
		return ""
	}
	return CleanBlock(out.Text, MaxNotesBytes)
}

// Links returns the https links of text, in order and without duplicates,
// at most MaxLinks: a link starts at "https://" (any case) and ends at
// whitespace, a quote, an angle bracket or a backslash, without trailing
// punctuation (and without a closing parenthesis it does not open). Only
// what SafeLink accepts is returned, in its normalised form. Nothing else
// (http:, javascript:, data:, file:, tel:, webcal:) is ever a link.
func Links(text string) []string {
	var out []string
	seen := map[string]bool{}
	lower := strings.ToLower(text)
	for i := 0; i < len(lower) && len(out) < MaxLinks; {
		j := strings.Index(lower[i:], "https://")
		if j < 0 {
			break
		}
		start := i + j
		end := start + len("https://")
		for end < len(text) && !linkStop(text[end]) {
			end++
		}
		i = end
		if start > 0 && isWordByte(text[start-1]) {
			continue // "xhttps://": not a link of its own
		}
		cand := trimLinkTail(text[start:end])
		if u, ok := SafeLink(cand); ok && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

func linkStop(c byte) bool {
	return c <= ' ' || c == 0x7f || strings.IndexByte("\"'<>\\`{}|^", c) >= 0
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func trimLinkTail(s string) string {
	for s != "" {
		c := s[len(s)-1]
		switch {
		case strings.IndexByte(".,;:!?*", c) >= 0:
			s = s[:len(s)-1]
		case c == ')' && strings.Count(s, "(") < strings.Count(s, ")"):
			s = s[:len(s)-1]
		case c == ']' && strings.Count(s, "[") < strings.Count(s, "]"):
			s = s[:len(s)-1]
		default:
			return s
		}
	}
	return s
}

// SafeLink checks one URL and returns its normalised form: https only, a
// host, no user information, a host that is ASCII after IDNA (a non-ASCII
// host becomes punycode; one IDNA refuses is refused), at most
// MaxLinkBytes, no control characters or spaces.
func SafeLink(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > MaxLinkBytes || !utf8.ValidString(raw) {
		return "", false
	}
	for _, r := range raw {
		if r <= ' ' || r == 0x7f || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.IsSpace(r) {
			return "", false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Opaque != "" || u.User != nil {
		return "", false
	}
	host := u.Hostname()
	if host == "" || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") && len(host) == 1 {
		return "", false
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil || ascii == "" {
		return "", false
	}
	for i := 0; i < len(ascii); i++ {
		if ascii[i] >= 0x80 {
			return "", false
		}
	}
	u.Scheme = "https"
	if port := u.Port(); port != "" {
		u.Host = ascii + ":" + port
	} else {
		u.Host = ascii
	}
	s := u.String()
	if len(s) > MaxLinkBytes {
		return "", false
	}
	return s, true
}

// videoHosts are the hosts of the video-call providers we name (§3.9);
// a leading "." matches the host itself and every subdomain.
var videoHosts = []struct {
	host     string
	provider api.VideoProvider
}{
	{"teams.microsoft.com", api.VideoProviderTeams},
	{"teams.live.com", api.VideoProviderTeams},
	{".zoom.us", api.VideoProviderZoom},
	{"meet.google.com", api.VideoProviderMeet},
	{".webex.com", api.VideoProviderWebex},
	{"meet.jit.si", api.VideoProviderJitsi},
	{"whereby.com", api.VideoProviderWhereby},
}

// VideoProviderOf returns the provider of a known video-call host, "" for
// any other host.
func VideoProviderOf(host string) api.VideoProvider {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, v := range videoHosts {
		if strings.HasPrefix(v.host, ".") {
			if host == v.host[1:] || strings.HasSuffix(host, v.host) {
				return v.provider
			}
		} else if host == v.host {
			return v.provider
		}
	}
	return ""
}

// DetectVideoCall finds the video call of an event: the source's own
// join URL (Graph onlineMeeting.joinUrl), then the conference URLs
// (CONFERENCE, X-GOOGLE-CONFERENCE, X-MICROSOFT-SKYPETEAMSMEETINGURL) in
// order, each of which is a call when SafeLink accepts it (provider
// "other" for an unknown host); then the links of location and notes,
// which are a call only on a host of the fixed provider list. nil when
// none.
func DetectVideoCall(location, notes string, conference []string, joinURL string) *api.VideoCall {
	for _, c := range append([]string{joinURL}, conference...) {
		if c == "" {
			continue
		}
		if u, ok := SafeLink(c); ok {
			return videoCall(u, true)
		}
	}
	for _, text := range []string{location, notes} {
		for _, l := range Links(text) {
			if vc := videoCall(l, false); vc != nil {
				return vc
			}
		}
	}
	return nil
}

func videoCall(link string, anyHost bool) *api.VideoCall {
	u, err := url.Parse(link)
	if err != nil {
		return nil
	}
	p := VideoProviderOf(u.Hostname())
	if p == "" {
		if !anyHost {
			return nil
		}
		p = api.VideoProviderOther
	}
	return &api.VideoCall{URL: link, Provider: p, Host: strings.ToLower(u.Hostname())}
}
