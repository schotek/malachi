// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package jira

import (
	"net/url"
	"strings"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// Notification mail. A Jira site tells its users of every change by
// e-mail, and those messages arrive in the user's mail accounts:
// "[JIRA] (ITSD-42) Printer on the 2nd floor" from jira@acme.atlassian.net,
// or, from an automation rule, "IT Service Desk: Printer (ITSD-42)".
// MatchNotification recognises one by its sender and the issue key in its
// subject, for core, which then refreshes the issue and may hide the
// message (JiraConfig.NotificationMail, docs/api.md §4.1).
//
// Both inputs are written by whoever sent the message (docs/security.md
// §4.1):
// the matcher reads a bounded part of them, in one pass, compares bytes
// and ASCII letters only (a letter of another script that looks like one
// is another letter), and what it answers is no more than "this message
// says it is about that issue". What follows from that is decided
// elsewhere, and needs the issue itself.

const (
	// maxNotificationSubject is how many bytes of a subject are read: a
	// key further on is not found.
	maxNotificationSubject = 1024
	// maxNotificationFrom is how many senders a notification may have;
	// the site's have one.
	maxNotificationFrom = 4
	// maxSenderBytes bounds one address (RFC 5321: 64 + 1 + 255).
	maxSenderBytes = 320
	// An issue key in a subject: a space key of 2–10 capitals and digits
	// that starts with a capital, a hyphen, a number of 1–9 digits without
	// a leading zero.
	maxSubjectSpaceKey = 10
	maxSubjectNumber   = 9
)

// OfflineDays is the account's window in days: JiraConfig.OfflineDays, 0
// meaning api.DefaultJiraOfflineDays, at most api.MaxJiraOfflineDays.
func OfflineDays(cfg api.JiraConfig) int { return windowDays(cfg) }

// NotificationSenders is who the site's notification mail comes from:
// JiraConfig.NotificationSenders, each "addr@host" (that address) or
// "@host" (any address of that host), lower-cased; when the list is empty,
// "@<site host>" for a cloud site and nobody for a Data Center one, whose
// mail goes out through the organisation's own server under an address
// only its administrators know. Entries that are neither form are left
// out.
func NotificationSenders(cfg api.JiraConfig) []string {
	if len(cfg.NotificationSenders) == 0 {
		if cfg.Deployment != api.JiraCloud {
			return nil
		}
		u, err := url.Parse(cfg.SiteURL)
		if err != nil || u.Hostname() == "" {
			return nil
		}
		s, ok := normaliseSender("@" + u.Hostname())
		if !ok {
			return nil
		}
		return []string{s}
	}
	out := make([]string, 0, len(cfg.NotificationSenders))
	for _, entry := range cfg.NotificationSenders {
		if len(out) == api.MaxJiraListEntries {
			break
		}
		if s, ok := normaliseSender(entry); ok {
			out = append(out, s)
		}
	}
	return out
}

// MatchNotification reports whether a mail message with these senders
// and this subject is a notification of the account's site about an issue
// of one of its selected spaces, and the issue's key.
//
// Every sender of the message (at least one, at most maxNotificationFrom)
// must be among NotificationSenders(cfg), compared without regard to
// ASCII case and to one trailing dot of the host; a display name never
// counts, a host matches itself only (no subdomains). The subject must
// hold, within its first maxNotificationSubject bytes, an issue key in
// parentheses or square brackets ("(ITSD-42)", "[ITSD-42]") whose space
// key is, to the letter, the key of a selected space: that of cfg.Spaces
// as the site lists it now (spaces, the account's stored ones), else as
// the configuration has it. The first such key wins; keys of other spaces
// are passed over. An account whose NotificationMail is "ignore" matches
// nothing.
func MatchNotification(cfg api.JiraConfig, spaces []store.IssueSpace, from []api.Address, subject string) (key string, ok bool) {
	if cfg.NotificationMail == api.NotificationMailIgnore {
		return "", false
	}
	if len(from) == 0 || len(from) > maxNotificationFrom {
		return "", false
	}
	senders := NotificationSenders(cfg)
	if len(senders) == 0 {
		return "", false
	}
	for _, a := range from {
		if !senderAmong(a.Address, senders) {
			return "", false
		}
	}
	selected := selectedSpaceKeys(cfg, spaces)
	if len(selected) == 0 {
		return "", false
	}
	return subjectKey(subject, selected)
}

// normaliseSender brings an address ("addr@host") or a host ("@host") to
// the form it is compared in: ASCII letters lowered, one trailing dot of
// the host dropped. false for anything else: no "@", no host, a space or
// a control character, too long.
func normaliseSender(s string) (string, bool) {
	if len(s) == 0 || len(s) > maxSenderBytes {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] == 0x7f {
			return "", false
		}
	}
	at := strings.LastIndexByte(s, '@')
	if at < 0 {
		return "", false
	}
	host := strings.TrimSuffix(s[at+1:], ".")
	if host == "" || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || strings.ContainsAny(host, "@<>()[],;:\\\"") {
		return "", false
	}
	return asciiLower(s[:at]) + "@" + asciiLower(host), true
}

// senderAmong reports whether the address is one of the senders
// (normalised).
func senderAmong(address string, senders []string) bool {
	addr, ok := normaliseSender(address)
	if !ok || strings.HasPrefix(addr, "@") {
		return false
	}
	host := addr[strings.LastIndexByte(addr, '@'):]
	for _, s := range senders {
		if s == addr || s == host {
			return true
		}
	}
	return false
}

// asciiLower lowers the ASCII letters of s and nothing else.
func asciiLower(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// selectedSpaceKeys are the keys of the selected spaces: the site's
// current key of each space of the configuration it lists, the
// configuration's for the others.
func selectedSpaceKeys(cfg api.JiraConfig, spaces []store.IssueSpace) map[string]bool {
	listed := make(map[string]string, len(spaces))
	for _, sp := range spaces {
		listed[sp.SpaceID] = sp.Key
	}
	out := make(map[string]bool, len(cfg.Spaces))
	for i, ref := range cfg.Spaces {
		if i == api.MaxJiraSpaces {
			break
		}
		key := ref.Key
		if k := listed[ref.ID]; k != "" {
			key = k
		}
		if isSubjectSpaceKey(key) {
			out[key] = true
		}
	}
	return out
}

// isSubjectSpaceKey reports a space key a subject can name.
func isSubjectSpaceKey(key string) bool {
	if len(key) < 2 || len(key) > maxSubjectSpaceKey || key[0] < 'A' || key[0] > 'Z' {
		return false
	}
	for i := 1; i < len(key); i++ {
		if c := key[i]; (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// subjectKey finds the first issue key of a selected space that the
// subject holds between parentheses or square brackets. One pass: at
// every opening bracket at most a key's length is read.
func subjectKey(subject string, selected map[string]bool) (string, bool) {
	if len(subject) > maxNotificationSubject {
		subject = subject[:maxNotificationSubject]
	}
	for i := 0; i < len(subject); i++ {
		var closing byte
		switch subject[i] {
		case '(':
			closing = ')'
		case '[':
			closing = ']'
		default:
			continue
		}
		rest := subject[i+1:]
		space, n := issueKeyAt(rest)
		if n == 0 || n >= len(rest) || rest[n] != closing {
			continue
		}
		if selected[rest[:space]] {
			return rest[:n], true
		}
	}
	return "", false
}

// issueKeyAt reads an issue key at the start of s: the length of its
// space key and of the whole key, 0 when s does not start with one. A
// key followed by another digit is none (its number is too long).
func issueKeyAt(s string) (space, n int) {
	if len(s) == 0 || s[0] < 'A' || s[0] > 'Z' {
		return 0, 0
	}
	i := 1
	for i < len(s) && i <= maxSubjectSpaceKey && ((s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	if i < 2 || i > maxSubjectSpaceKey || i >= len(s) || s[i] != '-' {
		return 0, 0
	}
	space = i
	i++
	start := i
	if i >= len(s) || s[i] < '1' || s[i] > '9' {
		return 0, 0
	}
	for i < len(s) && i-start <= maxSubjectNumber && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i-start > maxSubjectNumber {
		return 0, 0
	}
	return space, i
}
