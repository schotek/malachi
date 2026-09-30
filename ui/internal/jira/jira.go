// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package jira is the view logic of issue-tracker accounts (kind jira,
// docs/api.md): what the sidebar, the message list, the reading pane, the
// account assistant and the comment window show for Jira spaces, issues,
// comments and status or assignee changes, and which link opens an issue.
//
// The daemon does the work (synchronisation, sanitising, comments); this
// package only turns its API values into texts and small view models. It
// is pure (no GTK, no gettext, no cgo: the caller passes a Translator) so
// that its rules are tested without a display and every client ports it
// one to one: macOS MalachiCore/Jira first, later Windows
// Malachi.Core/Jira; GTK uses it with an adapter over ui/internal/i18n.
//
// Every string of an issue except its key and URL is display text from
// the site and hostile input: the package cleans it (Clean) and the
// clients show it as plain text only. Brand names (Jira, Jira Cloud, Jira
// Data Center, id.atlassian.com) are not translated.
package jira

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Translator translates a msgid of the malachi domain: T a plain one, N
// a plural form for n, C one disambiguated by a context. GTK passes an
// adapter over i18n.T, i18n.N and i18n.C; the tests pass one that returns
// the msgid (the singular for n == 1).
type Translator interface {
	T(msgid string) string
	N(msgid, plural string, n int) string
	C(context, msgid string) string
}

// KindBadge is the capsule next to a Jira account's name in the sidebar.
// A brand name, never translated.
const KindBadge = "JIRA"

// Brand names of the deployments, never translated.
const (
	CloudName      = "Jira Cloud"
	DataCenterName = "Jira Data Center"
)

// emptyValue stands for a missing side of a change ("—", an em dash).
const emptyValue = "—"

// maxText caps a cleaned display string, in bytes.
const maxText = 512

// StatusStyle is how a status pill is coloured, by the status's category.
// Its value is the GTK style class; the other clients map it to their
// colours (grey, blue, green; StatusPlain has no colour).
type StatusStyle string

// The styles.
const (
	StatusPlain      StatusStyle = ""
	StatusTodo       StatusStyle = "status-todo"
	StatusInProgress StatusStyle = "status-in-progress"
	StatusDone       StatusStyle = "status-done"
)

// StyleOf is the style of a status category; an unknown or empty category
// is StatusPlain (the category is an open enum).
func StyleOf(c api.IssueStatusCategory) StatusStyle {
	switch c {
	case api.StatusCategoryTodo:
		return StatusTodo
	case api.StatusCategoryInProgress:
		return StatusInProgress
	case api.StatusCategoryDone:
		return StatusDone
	}
	return StatusPlain
}

// Clean makes display text from the site safe to lay out on one line: it
// drops invalid UTF-8, format characters (Cf: bidirectional overrides such
// as U+202E, zero-width characters, the soft hyphen) and control
// characters, turns line breaks, tabs and the line and paragraph
// separators into spaces, collapses runs of spaces, trims, and caps the
// result at 512 bytes on a character boundary.
func Clean(s string) string {
	var b strings.Builder
	space := false
	for i, w := 0, 0; i < len(s); i += w {
		r, size := utf8.DecodeRuneInString(s[i:])
		w = size
		switch {
		case r == utf8.RuneError && size <= 1:
			continue
		case unicode.IsSpace(r) || unicode.In(r, unicode.Zl, unicode.Zp):
			space = b.Len() > 0
			continue
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	return truncate(b.String(), maxText)
}

// truncate caps s at n bytes on a character boundary, trimming spaces
// after the cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimRight(s[:cut], " ")
}

// DeploymentName is the brand name of a deployment; "Jira" for an unknown
// one.
func DeploymentName(d api.JiraDeployment) string {
	switch d {
	case api.JiraCloud:
		return CloudName
	case api.JiraDataCenter:
		return DataCenterName
	}
	return "Jira"
}

// IsJira reports an account of kind jira.
func IsJira(cfg api.AccountConfig) bool {
	return cfg.Protocol() == api.AccountJira
}

// AlwaysThreaded reports an account whose folders are always listed as
// conversations (thread.list), whatever the "group by conversation"
// setting: a Jira issue is one thread.
func AlwaysThreaded(cfg api.AccountConfig) bool {
	return IsJira(cfg)
}

// SiteHost is the host of a Jira account's site, lower-case and without
// port ("acme.atlassian.net"); "" for another kind of account or a site
// that is not a URL.
func SiteHost(cfg api.AccountConfig) string {
	if !IsJira(cfg) || cfg.Jira == nil {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(cfg.Jira.SiteURL))
	if err != nil {
		return ""
	}
	return Clean(strings.ToLower(u.Hostname()))
}

// AccountLabel is the name the sidebar and the settings show for an
// account: its name; for a Jira account without one the site's host; else
// its e-mail address (model.go accountLabel for mail accounts).
func AccountLabel(cfg api.AccountConfig) string {
	if name := strings.TrimSpace(cfg.Name); name != "" {
		return name
	}
	if host := SiteHost(cfg); host != "" {
		return host
	}
	return strings.TrimSpace(cfg.Email)
}

// VirtualFolderTitle is the localised name of a fixed view of a Jira
// account; "" for no view or one this client does not know (show the
// folder's Name then).
func VirtualFolderTitle(v api.VirtualFolder, tr Translator) string {
	switch v {
	case api.VirtualAssignedToMe:
		// TRANSLATORS: a folder of a Jira account: the issues assigned to the user.
		return tr.C("folder", "Assigned to Me")
	case api.VirtualWatching:
		// TRANSLATORS: a folder of a Jira account: the issues the user watches.
		return tr.C("folder", "Watching")
	case api.VirtualOpen:
		// TRANSLATORS: a folder of a Jira account: the issues that are not closed yet.
		return tr.C("folder", "Open")
	}
	return ""
}

// VirtualRank orders the fixed views in the sidebar: below the account's
// mail role folders, above its spaces. 100 for no view or an unknown one,
// like an ordinary folder.
func VirtualRank(v api.VirtualFolder) int {
	switch v {
	case api.VirtualAssignedToMe:
		return 0
	case api.VirtualWatching:
		return 1
	case api.VirtualOpen:
		return 2
	}
	return 100
}

// VirtualIcon is the icon of a fixed view (a GTK icon name; the other
// clients map it to theirs); "" for no view.
func VirtualIcon(v api.VirtualFolder) string {
	if v == "" {
		return ""
	}
	return "folder-saved-search-symbolic"
}

// CardRow is one line of the issue card's grid: a field and its value.
// Missing marks a placeholder value ("Unassigned", "None"), shown dimmed.
type CardRow struct {
	Label   string
	Value   string
	Missing bool
}

// Card is the issue card above a Jira message in the reading pane: the key
// as a link to the issue, the summary, the status pill, the grid of the
// other fields, and what the message is of the issue.
type Card struct {
	// Key opens URL in the browser (after IsIssueURL); OpenTooltip is the
	// link's tooltip.
	Key, URL, OpenTooltip string
	Summary               string
	// Status is the pill's text ("" hides the pill), StatusLabel its
	// accessible name, StatusStyle its colour.
	Status, StatusLabel string
	StatusStyle         StatusStyle
	// Rows are Assignee, Priority, Type and Reporter, in this order.
	Rows []CardRow
	// Internal marks an internal comment of a service-desk issue: the card
	// shows the badge InternalLabel ("" when not internal).
	Internal      bool
	InternalLabel string
	// Via names the integration that posted the comment for its author
	// ("via Issue Sync"); Edited says the comment was changed after it was
	// posted. "" when not.
	Via, Edited string
}

// IssueCard builds the card of issue; item, when not nil, is the message's
// part of it (MessageSummary.Issue) and adds the internal badge, Via and
// Edited.
func IssueCard(issue api.IssueInfo, item *api.MessageIssue, tr Translator) Card {
	key := Clean(issue.Key)
	c := Card{
		Key: key,
		URL: strings.TrimSpace(issue.URL),
		// TRANSLATORS: tooltip of an issue key such as "ITSD-42".
		OpenTooltip: fmt.Sprintf(tr.T("Open %s in the Browser"), key),
		Summary:     Clean(issue.Summary),
		Status:      Clean(issue.Status),
		// TRANSLATORS: a field of a Jira issue.
		StatusLabel: tr.T("Status"),
		StatusStyle: StyleOf(issue.StatusCategory),
	}
	none := func(v string) CardRow {
		if v = Clean(v); v != "" {
			return CardRow{Value: v}
		}
		// TRANSLATORS: the value of an empty field of a Jira issue (priority, type, reporter).
		return CardRow{Value: tr.C("jira value", "None"), Missing: true}
	}
	assignee := CardRow{Value: Clean(issue.Assignee)}
	if assignee.Value == "" {
		assignee = CardRow{Value: tr.T("Unassigned"), Missing: true}
	}
	// TRANSLATORS: a field of a Jira issue: the person who works on it.
	assignee.Label = tr.T("Assignee")
	priority := none(issue.Priority)
	// TRANSLATORS: a field of a Jira issue.
	priority.Label = tr.T("Priority")
	kind := none(issue.Type)
	// TRANSLATORS: a field of a Jira issue: its type, such as Bug or Task.
	kind.Label = tr.T("Type")
	reporter := none(issue.Reporter)
	// TRANSLATORS: a field of a Jira issue: the person who created it.
	reporter.Label = tr.T("Reporter")
	c.Rows = []CardRow{assignee, priority, kind, reporter}
	if item == nil {
		return c
	}
	if IsInternal(item) {
		c.Internal = true
		c.InternalLabel = InternalLabel(tr)
	}
	if via := Clean(item.Via); via != "" {
		// TRANSLATORS: %s is an integration (a bot) that posted a comment on its author's behalf.
		c.Via = fmt.Sprintf(tr.T("via %s"), via)
	}
	if item.Edited {
		// TRANSLATORS: a comment of a Jira issue was changed after it was posted.
		c.Edited = tr.T("Edited")
	}
	return c
}

// IsInternal reports an internal comment of a service-desk issue.
func IsInternal(item *api.MessageIssue) bool {
	return item != nil && item.Item == api.IssueItemComment && item.Visibility == api.CommentInternal
}

// InternalLabel is the badge of an internal comment.
func InternalLabel(tr Translator) string {
	// TRANSLATORS: badge of a comment only the service-desk team can read.
	return tr.C("jira", "Internal")
}

// IsEvent reports a message that stands for status or assignee changes.
func IsEvent(item *api.MessageIssue) bool {
	return item != nil && item.Item == api.IssueItemEvent
}

// EventLines are the sentences of an event message, one per change it
// knows ("Status: To Do → In Progress"); a change of a field this client
// does not know is skipped (the field is an open enum). An empty side is
// "Unassigned" for the assignee and "—" otherwise.
func EventLines(changes []api.IssueChange, tr Translator) []string {
	var out []string
	for _, ch := range changes {
		from, to := Clean(ch.From), Clean(ch.To)
		switch ch.Field {
		case api.IssueFieldStatus:
			// TRANSLATORS: an issue's status changed; the first %s is the old status, the second the new one.
			out = append(out, fmt.Sprintf(tr.T("Status: %s → %s"), orEmpty(from), orEmpty(to)))
		case api.IssueFieldAssignee:
			if from == "" {
				from = tr.T("Unassigned")
			}
			if to == "" {
				to = tr.T("Unassigned")
			}
			// TRANSLATORS: an issue was assigned to someone else; the first %s is the old assignee, the second the new one.
			out = append(out, fmt.Sprintf(tr.T("Assignee: %s → %s"), from, to))
		}
	}
	return out
}

// EventText is EventLines on one line, for the message list.
func EventText(changes []api.IssueChange, tr Translator) string {
	lines := EventLines(changes, tr)
	if len(lines) == 0 {
		return ""
	}
	// TRANSLATORS: put between two changes of an issue on one line ("Status: A → B; Assignee: C → D").
	return strings.Join(lines, tr.C("change list separator", "; "))
}

func orEmpty(s string) string {
	if s == "" {
		return emptyValue
	}
	return s
}

// IssueRow is what a message list row shows of an issue: the key, the
// summary and the status pill on the subject line, the internal badge,
// and for an event the changes instead of the preview.
type IssueRow struct {
	Key, Summary, Status string
	StatusStyle          StatusStyle
	// Internal marks an internal comment; InternalLabel is its badge (""
	// when not internal).
	Internal      bool
	InternalLabel string
	// Event marks an event row (for a conversation: its latest member is
	// one): EventText replaces the preview, and the row has the secondary
	// style.
	Event     bool
	EventText string
	// Unread is whether the row shows as unread. An event never does,
	// whatever its flags.
	Unread bool
}

// RowIssue is the issue part of a message row; nil for a message of a
// mail account.
func RowIssue(s api.MessageSummary, tr Translator) *IssueRow {
	if s.Issue == nil {
		return nil
	}
	r := newRow(s.Issue.IssueInfo, s.Issue, tr)
	r.Unread = !r.Event && !hasFlag(s.Flags, api.FlagSeen)
	return &r
}

// ThreadRowIssue is the issue part of a conversation row: the thread's
// issue, and the badge or the event text of its latest member. nil for a
// conversation of a mail account.
func ThreadRowIssue(t api.ThreadSummary, tr Translator) *IssueRow {
	info := t.Issue
	if info == nil && t.Latest.Issue != nil {
		info = &t.Latest.Issue.IssueInfo
	}
	if info == nil {
		return nil
	}
	r := newRow(*info, t.Latest.Issue, tr)
	r.Unread = t.UnreadCount > 0
	return &r
}

func newRow(info api.IssueInfo, item *api.MessageIssue, tr Translator) IssueRow {
	r := IssueRow{
		Key:         Clean(info.Key),
		Summary:     Clean(info.Summary),
		Status:      Clean(info.Status),
		StatusStyle: StyleOf(info.StatusCategory),
	}
	if IsInternal(item) {
		r.Internal = true
		r.InternalLabel = InternalLabel(tr)
	}
	if IsEvent(item) {
		r.Event = true
		r.EventText = EventText(item.Changes, tr)
	}
	return r
}

func hasFlag(flags []api.Flag, f api.Flag) bool {
	for _, have := range flags {
		if have == f {
			return true
		}
	}
	return false
}

// AuthBannerText is the sign-in banner of a Jira account for a
// notify.authRequired reason; account is the account's display name. ""
// for another kind of account, and for a reason whose sentence is the mail
// accounts' (a keyring failure).
func AuthBannerText(kind api.AccountKind, reason api.ErrorCode, account string, tr Translator) string {
	if kind != api.AccountJira {
		return ""
	}
	switch reason {
	case api.CodeAuthRequired:
		// TRANSLATORS: banner; %s is an account name.
		return fmt.Sprintf(tr.T("No API token is stored for %s"), account)
	case api.CodeAuthFailed:
		// TRANSLATORS: banner; %s is an account name.
		return fmt.Sprintf(tr.T("The Jira site rejected the token of %s"), account)
	}
	return ""
}

// IsIssueURL reports a link that may be opened as an issue of the site
// siteURL (JiraConfig.SiteURL): an absolute https URL, or http when the
// site itself is http, without user info, whose host is the site's
// (ignoring case and one trailing dot; an internationalised name matches
// its punycode form) on the same port. The raw text must be valid UTF-8
// without spaces, control or format characters and backslashes, and its
// authority without '%' (parsers disagree about those). Anything else is
// refused.
func IsIssueURL(raw, siteURL string) bool {
	site, err := url.Parse(strings.TrimSpace(siteURL))
	if err != nil || (site.Scheme != "https" && site.Scheme != "http") || site.Host == "" {
		return false
	}
	if !utf8.ValidString(raw) {
		return false
	}
	for _, r := range raw {
		if r <= ' ' || r == 0x7f || r == '\\' || unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "https":
	case "http":
		if site.Scheme != "http" {
			return false
		}
	default:
		return false
	}
	prefix := u.Scheme + "://"
	if len(raw) < len(prefix) || !strings.EqualFold(raw[:len(prefix)], prefix) {
		return false
	}
	authority := raw[len(prefix):]
	if end := strings.IndexAny(authority, "/?#"); end >= 0 {
		authority = authority[:end]
	}
	if strings.ContainsAny(authority, "%@") {
		return false
	}
	host, ok := hostKey(u.Hostname())
	if !ok {
		return false
	}
	want, ok := hostKey(site.Hostname())
	if !ok || host != want {
		return false
	}
	port, ok := portOf(u)
	if !ok {
		return false
	}
	sitePort, ok := portOf(site)
	return ok && port == sitePort
}

// portOf is the URL's port, or its scheme's default.
func portOf(u *url.URL) (int, bool) {
	p := u.Port()
	if p == "" {
		if u.Scheme == "http" {
			return 80, true
		}
		return 443, true
	}
	n, err := strconv.Atoi(p)
	if err != nil || n <= 0 || n > 65535 {
		return 0, false
	}
	return n, true
}

// hostKey is the form two host names are compared in: lower case, one
// trailing dot dropped, every non-ASCII label as its punycode "xn--" form.
// An IPv6 literal (url.Hostname drops its brackets) is compared lower
// case. false for an empty name or an empty label.
func hostKey(host string) (string, bool) {
	if strings.Contains(host, ":") {
		return strings.ToLower(host), host != ""
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", false
	}
	labels := strings.Split(strings.ToLower(host), ".")
	for i, l := range labels {
		if l == "" {
			return "", false
		}
		if isASCII(l) {
			continue
		}
		enc, ok := punycode(l)
		if !ok {
			return "", false
		}
		labels[i] = "xn--" + enc
	}
	return strings.Join(labels, "."), true
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// Punycode parameters (RFC 3492 §5).
const (
	pcBase        = 36
	pcTMin        = 1
	pcTMax        = 26
	pcSkew        = 38
	pcDamp        = 700
	pcInitialBias = 72
	pcInitialN    = 128
	pcMaxDelta    = 1 << 30 // far above any label; guards the arithmetic
)

// punycode encodes a label (RFC 3492 §6.3) without the "xn--" prefix; the
// ASCII characters are kept as they are. false on overflow or an invalid
// character.
func punycode(label string) (string, bool) {
	runes := []rune(label)
	var out []byte
	for _, r := range runes {
		if r == utf8.RuneError {
			return "", false
		}
		if r < pcInitialN {
			out = append(out, byte(r))
		}
	}
	basic := len(out)
	handled := basic
	if basic > 0 {
		out = append(out, '-')
	}
	n, delta, bias := rune(pcInitialN), 0, pcInitialBias
	for handled < len(runes) {
		m := rune(unicode.MaxRune + 1)
		for _, r := range runes {
			if r >= n && r < m {
				m = r
			}
		}
		if int(m-n) > (pcMaxDelta-delta)/(handled+1) {
			return "", false
		}
		delta += int(m-n) * (handled + 1)
		n = m
		for _, r := range runes {
			if r < n {
				delta++
				if delta > pcMaxDelta {
					return "", false
				}
			}
			if r != n {
				continue
			}
			q := delta
			for k := pcBase; ; k += pcBase {
				t := k - bias
				if t < pcTMin {
					t = pcTMin
				} else if t > pcTMax {
					t = pcTMax
				}
				if q < t {
					break
				}
				out = append(out, pcDigit(t+(q-t)%(pcBase-t)))
				q = (q - t) / (pcBase - t)
			}
			out = append(out, pcDigit(q))
			bias = pcAdapt(delta, handled+1, handled == basic)
			delta = 0
			handled++
		}
		delta++
		n++
	}
	return string(out), true
}

func pcDigit(d int) byte {
	if d < 26 {
		return byte('a' + d)
	}
	return byte('0' + d - 26)
}

func pcAdapt(delta, points int, first bool) int {
	if first {
		delta /= pcDamp
	} else {
		delta /= 2
	}
	delta += delta / points
	k := 0
	for delta > ((pcBase-pcTMin)*pcTMax)/2 {
		delta /= pcBase - pcTMin
		k += pcBase
	}
	return k + (pcBase-pcTMin+1)*delta/(delta+pcSkew)
}
