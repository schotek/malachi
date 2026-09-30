// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package jira

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/schotek/malachi/backend/pkg/api"
)

// identity is the translator of the tests: every msgid is its own
// translation (the singular for n == 1).
type identity struct{}

func (identity) T(msgid string) string { return msgid }

func (identity) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}

func (identity) C(_, msgid string) string { return msgid }

// catalog translates the (context, msgid) pairs it has, like a Czech
// catalogue, and leaves the rest alone.
type catalog map[[2]string]string

func (c catalog) T(msgid string) string { return c.C("", msgid) }

func (c catalog) N(msgid, plural string, n int) string {
	if n == 1 {
		return c.T(msgid)
	}
	return c.T(plural)
}

func (c catalog) C(ctx, msgid string) string {
	if s, ok := c[[2]string{ctx, msgid}]; ok {
		return s
	}
	return msgid
}

var tr = identity{}

// Characters the cleaning must drop, as rune constants: the source stays
// free of invisible characters.
var (
	rlo  = string(rune(0x202E)) // RIGHT-TO-LEFT OVERRIDE
	zwsp = string(rune(0x200B)) // ZERO WIDTH SPACE
	shy  = string(rune(0x00AD)) // SOFT HYPHEN
	lsep = string(rune(0x2028)) // LINE SEPARATOR
	bom  = string(rune(0xFEFF)) // ZERO WIDTH NO-BREAK SPACE
)

func TestStyleOf(t *testing.T) {
	tests := []struct {
		in   api.IssueStatusCategory
		want StatusStyle
	}{
		{api.StatusCategoryTodo, "status-todo"},
		{api.StatusCategoryInProgress, "status-in-progress"},
		{api.StatusCategoryDone, "status-done"},
		{"", ""},
		{"blocked", ""},
		{"Done", ""},
	}
	for _, tt := range tests {
		if got := StyleOf(tt.in); got != tt.want {
			t.Errorf("StyleOf(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestClean(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "VPN drops every 10 minutes", "VPN drops every 10 minutes"},
		{"trim and collapse", "  a \t\n  b  ", "a b"},
		{"line breaks become spaces", "first\r\nsecond" + lsep + "third", "first second third"},
		{"bidi override dropped", "invoice" + rlo + "fdp.exe", "invoicefdp.exe"},
		{"zero width and soft hyphen dropped", "ad" + zwsp + "min" + shy + "istrator" + bom, "administrator"},
		{"control characters dropped", "a\x00b\x07c\x1bd\x7fe", "abcde"},
		{"invalid UTF-8 dropped", "ok\xff\xfe!", "ok!"},
		{"only invisible", zwsp + rlo + " \n", ""},
		{"czech kept", "Jana Dvořáková", "Jana Dvořáková"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		if got := Clean(tt.in); got != tt.want {
			t.Errorf("%s: Clean(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
	long := Clean(strings.Repeat("č", 600))
	if len(long) > maxText || !utf8.ValidString(long) || len(long) < maxText-1 {
		t.Errorf("Clean of 1200 bytes = %d bytes, valid %v", len(long), utf8.ValidString(long))
	}
	spaced := Clean(strings.Repeat("a", maxText-1) + " bcd")
	if spaced != strings.Repeat("a", maxText-1) {
		t.Errorf("a cut after a space keeps the space: %q", spaced[len(spaced)-3:])
	}
}

func jiraConfig(name, site string) api.AccountConfig {
	return api.AccountConfig{
		Name: name, Email: "jana@acme.example", Kind: api.AccountJira,
		Jira: &api.JiraConfig{SiteURL: site, Deployment: api.JiraCloud},
	}
}

func TestSiteHostAndAccountLabel(t *testing.T) {
	tests := []struct {
		name      string
		cfg       api.AccountConfig
		host, lbl string
	}{
		{"named", jiraConfig("Acme Jira", "https://acme.atlassian.net"), "acme.atlassian.net", "Acme Jira"},
		{"unnamed", jiraConfig(" ", "https://ACME.atlassian.net"), "acme.atlassian.net", "acme.atlassian.net"},
		{"port and path", jiraConfig("", "https://jira.acme.example:8443/jira"), "jira.acme.example", "jira.acme.example"},
		{"not a URL", jiraConfig("", "https://[bad"), "", "jana@acme.example"},
		{"no jira block", api.AccountConfig{Kind: api.AccountJira, Email: "jana@acme.example"}, "", "jana@acme.example"},
		{"mail account", api.AccountConfig{Email: " jana@acme.example ", Jira: &api.JiraConfig{SiteURL: "https://acme.atlassian.net"}}, "", "jana@acme.example"},
		{"mail account with a name", api.AccountConfig{Name: "Work", Email: "jana@acme.example"}, "", "Work"},
	}
	for _, tt := range tests {
		if got := SiteHost(tt.cfg); got != tt.host {
			t.Errorf("%s: SiteHost = %q, want %q", tt.name, got, tt.host)
		}
		if got := AccountLabel(tt.cfg); got != tt.lbl {
			t.Errorf("%s: AccountLabel = %q, want %q", tt.name, got, tt.lbl)
		}
	}
}

func TestKindHelpers(t *testing.T) {
	if !IsJira(jiraConfig("", "")) || IsJira(api.AccountConfig{}) || IsJira(api.AccountConfig{Kind: api.AccountGraph}) {
		t.Error("IsJira")
	}
	if !AlwaysThreaded(jiraConfig("", "")) || AlwaysThreaded(api.AccountConfig{}) {
		t.Error("AlwaysThreaded")
	}
	for d, want := range map[api.JiraDeployment]string{
		api.JiraCloud: "Jira Cloud", api.JiraDataCenter: "Jira Data Center", "": "Jira", "server": "Jira",
	} {
		if got := DeploymentName(d); got != want {
			t.Errorf("DeploymentName(%q) = %q, want %q", d, got, want)
		}
	}
}

func TestVirtualFolders(t *testing.T) {
	cs := catalog{
		{"folder", "Assigned to Me"}: "Přiřazené mně",
		{"folder", "Watching"}:       "Sledované",
		{"folder", "Open"}:           "Neuzavřené",
		{"", "Open"}:                 "Otevřít", // the verb must not be picked
	}
	tests := []struct {
		v          api.VirtualFolder
		en, cs     string
		rank       int
		icon       string
		knownTitle bool
	}{
		{api.VirtualAssignedToMe, "Assigned to Me", "Přiřazené mně", 0, "folder-saved-search-symbolic", true},
		{api.VirtualWatching, "Watching", "Sledované", 1, "folder-saved-search-symbolic", true},
		{api.VirtualOpen, "Open", "Neuzavřené", 2, "folder-saved-search-symbolic", true},
		{"recent", "", "", 100, "folder-saved-search-symbolic", false},
		{"", "", "", 100, "", false},
	}
	for _, tt := range tests {
		if got := VirtualFolderTitle(tt.v, tr); got != tt.en {
			t.Errorf("VirtualFolderTitle(%q) = %q, want %q", tt.v, got, tt.en)
		}
		if got := VirtualFolderTitle(tt.v, cs); got != tt.cs {
			t.Errorf("VirtualFolderTitle(%q) in Czech = %q, want %q", tt.v, got, tt.cs)
		}
		if got := VirtualRank(tt.v); got != tt.rank {
			t.Errorf("VirtualRank(%q) = %d, want %d", tt.v, got, tt.rank)
		}
		if got := VirtualIcon(tt.v); got != tt.icon {
			t.Errorf("VirtualIcon(%q) = %q, want %q", tt.v, got, tt.icon)
		}
	}
}

func fullIssue() api.IssueInfo {
	return api.IssueInfo{
		Key: "ITSD-42", URL: "https://acme.atlassian.net/browse/ITSD-42", Summary: "VPN drops every 10 minutes",
		Status: "In Progress", StatusCategory: api.StatusCategoryInProgress, Type: "Incident", Priority: "High",
		Assignee: "Jana Dvořáková", Reporter: "Petr Novák", AssignedToMe: true,
		CommentVisibilities: []api.CommentVisibility{api.CommentPublic, api.CommentInternal},
	}
}

func TestIssueCard(t *testing.T) {
	c := IssueCard(fullIssue(), nil, tr)
	want := Card{
		Key: "ITSD-42", URL: "https://acme.atlassian.net/browse/ITSD-42", OpenTooltip: "Open ITSD-42 in the Browser",
		Summary: "VPN drops every 10 minutes", Status: "In Progress", StatusLabel: "Status", StatusStyle: StatusInProgress,
		Rows: []CardRow{
			{Label: "Assignee", Value: "Jana Dvořáková"},
			{Label: "Priority", Value: "High"},
			{Label: "Type", Value: "Incident"},
			{Label: "Reporter", Value: "Petr Novák"},
		},
	}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("IssueCard =\n%+v\nwant\n%+v", c, want)
	}

	empty := IssueCard(api.IssueInfo{Key: "WEB-7", Summary: " " + rlo + "Login\nbroken "}, nil, tr)
	if empty.Summary != "Login broken" || empty.Status != "" || empty.StatusStyle != StatusPlain {
		t.Errorf("empty issue: summary %q, status %q, style %q", empty.Summary, empty.Status, empty.StatusStyle)
	}
	wantRows := []CardRow{
		{Label: "Assignee", Value: "Unassigned", Missing: true},
		{Label: "Priority", Value: "None", Missing: true},
		{Label: "Type", Value: "None", Missing: true},
		{Label: "Reporter", Value: "None", Missing: true},
	}
	if !reflect.DeepEqual(empty.Rows, wantRows) {
		t.Errorf("empty rows = %+v", empty.Rows)
	}
	cs := catalog{{"jira value", "None"}: "Nezadáno", {"", "None"}: "Žádné", {"", "Unassigned"}: "Nepřiřazeno"}
	csRows := IssueCard(api.IssueInfo{Key: "WEB-7"}, nil, cs).Rows
	if csRows[0].Value != "Nepřiřazeno" || csRows[1].Value != "Nezadáno" {
		t.Errorf("Czech fallbacks = %+v", csRows)
	}
	if hidden := IssueCard(api.IssueInfo{Key: "WEB-7", Priority: zwsp + " "}, nil, tr).Rows[1]; !hidden.Missing {
		t.Errorf("an invisible priority is missing: %+v", hidden)
	}
}

func TestIssueCardItem(t *testing.T) {
	issue := fullIssue()
	internal := &api.MessageIssue{IssueInfo: issue, Item: api.IssueItemComment, Visibility: api.CommentInternal, Via: "Issue Sync", Edited: true}
	c := IssueCard(issue, internal, tr)
	if !c.Internal || c.InternalLabel != "Internal" || c.Via != "via Issue Sync" || c.Edited != "Edited" {
		t.Errorf("internal comment card: %+v", c)
	}
	public := &api.MessageIssue{IssueInfo: issue, Item: api.IssueItemComment, Visibility: api.CommentPublic}
	if c := IssueCard(issue, public, tr); c.Internal || c.InternalLabel != "" || c.Via != "" || c.Edited != "" {
		t.Errorf("public comment card: %+v", c)
	}
	// Only a comment is internal, whatever the visibility field says.
	desc := &api.MessageIssue{IssueInfo: issue, Item: api.IssueItemDescription, Visibility: api.CommentInternal}
	if c := IssueCard(issue, desc, tr); c.Internal {
		t.Error("a description is never internal")
	}
	if c := IssueCard(issue, &api.MessageIssue{Item: api.IssueItemComment, Via: rlo + zwsp}, tr); c.Via != "" {
		t.Errorf("an invisible Via = %q", c.Via)
	}
}

func TestEventLines(t *testing.T) {
	tests := []struct {
		name    string
		changes []api.IssueChange
		want    []string
	}{
		{"none", nil, nil},
		{"status", []api.IssueChange{{Field: api.IssueFieldStatus, From: "To Do", To: "In Progress"}}, []string{"Status: To Do → In Progress"}},
		{"status without the old value", []api.IssueChange{{Field: api.IssueFieldStatus, To: "Done"}}, []string{"Status: — → Done"}},
		{"status without the new value", []api.IssueChange{{Field: api.IssueFieldStatus, From: "Done"}}, []string{"Status: Done → —"}},
		{"status both empty", []api.IssueChange{{Field: api.IssueFieldStatus}}, []string{"Status: — → —"}},
		{"assigned", []api.IssueChange{{Field: api.IssueFieldAssignee, To: "Jana Dvořáková"}}, []string{"Assignee: Unassigned → Jana Dvořáková"}},
		{"unassigned", []api.IssueChange{{Field: api.IssueFieldAssignee, From: "Jana Dvořáková"}}, []string{"Assignee: Jana Dvořáková → Unassigned"}},
		{"unknown field skipped", []api.IssueChange{{Field: "priority", From: "Low", To: "High"}, {Field: api.IssueFieldAssignee, From: "A", To: "B"}}, []string{"Assignee: A → B"}},
		{"hostile values cleaned", []api.IssueChange{{Field: api.IssueFieldStatus, From: "To\nDo", To: rlo + zwsp}}, []string{"Status: To Do → —"}},
		{"two changes", []api.IssueChange{{Field: api.IssueFieldStatus, From: "A", To: "B"}, {Field: api.IssueFieldAssignee, To: "C"}}, []string{"Status: A → B", "Assignee: Unassigned → C"}},
	}
	for _, tt := range tests {
		if got := EventLines(tt.changes, tr); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: EventLines = %q, want %q", tt.name, got, tt.want)
		}
	}
	two := []api.IssueChange{{Field: api.IssueFieldStatus, From: "A", To: "B"}, {Field: api.IssueFieldAssignee, To: "C"}}
	if got := EventText(two, tr); got != "Status: A → B; Assignee: Unassigned → C" {
		t.Errorf("EventText = %q", got)
	}
	if got := EventText([]api.IssueChange{{Field: "labels"}}, tr); got != "" {
		t.Errorf("EventText of unknown fields = %q", got)
	}
}

func TestRowIssue(t *testing.T) {
	issue := fullIssue()
	if RowIssue(api.MessageSummary{Subject: "Hello"}, tr) != nil {
		t.Error("a mail message has no issue row")
	}
	comment := api.MessageSummary{Issue: &api.MessageIssue{IssueInfo: issue, Item: api.IssueItemComment, Visibility: api.CommentInternal}}
	r := RowIssue(comment, tr)
	want := &IssueRow{
		Key: "ITSD-42", Summary: "VPN drops every 10 minutes", Status: "In Progress", StatusStyle: StatusInProgress,
		Internal: true, InternalLabel: "Internal", Unread: true,
	}
	if !reflect.DeepEqual(r, want) {
		t.Errorf("RowIssue(internal unread comment) = %+v", r)
	}
	comment.Flags = []api.Flag{api.FlagFlagged, api.FlagSeen}
	if r := RowIssue(comment, tr); r.Unread {
		t.Error("a seen comment is read")
	}
	event := api.MessageSummary{Issue: &api.MessageIssue{
		IssueInfo: issue, Item: api.IssueItemEvent,
		Changes: []api.IssueChange{{Field: api.IssueFieldStatus, From: "To Do", To: "In Progress"}},
	}}
	r = RowIssue(event, tr)
	if !r.Event || r.EventText != "Status: To Do → In Progress" || r.Unread || r.Internal {
		t.Errorf("RowIssue(event without seen) = %+v", r)
	}
}

func TestThreadRowIssue(t *testing.T) {
	issue := fullIssue()
	if ThreadRowIssue(api.ThreadSummary{Subject: "Hello"}, tr) != nil {
		t.Error("a mail conversation has no issue row")
	}
	thread := api.ThreadSummary{
		Issue:       &issue,
		UnreadCount: 2,
		Latest: api.MessageSummary{Issue: &api.MessageIssue{
			IssueInfo: api.IssueInfo{Key: "ITSD-42", Summary: "old summary"}, Item: api.IssueItemEvent,
			Changes: []api.IssueChange{{Field: api.IssueFieldAssignee, To: "Jana Dvořáková"}},
		}},
	}
	r := ThreadRowIssue(thread, tr)
	if r.Summary != "VPN drops every 10 minutes" || !r.Event || r.EventText != "Assignee: Unassigned → Jana Dvořáková" || !r.Unread {
		t.Errorf("ThreadRowIssue(latest event, 2 unread) = %+v", r)
	}
	thread.UnreadCount = 0
	thread.Latest.Issue = &api.MessageIssue{IssueInfo: issue, Item: api.IssueItemComment}
	if r := ThreadRowIssue(thread, tr); r.Event || r.Unread || r.EventText != "" {
		t.Errorf("ThreadRowIssue(latest comment, read) = %+v", r)
	}
	thread.Issue = nil
	if r := ThreadRowIssue(thread, tr); r == nil || r.Key != "ITSD-42" {
		t.Errorf("a thread without Issue falls back to its latest member: %+v", r)
	}
}

func TestAuthBannerText(t *testing.T) {
	tests := []struct {
		kind   api.AccountKind
		reason api.ErrorCode
		want   string
	}{
		{api.AccountJira, api.CodeAuthRequired, "No API token is stored for Acme"},
		{api.AccountJira, api.CodeAuthFailed, "The Jira site rejected the token of Acme"},
		{api.AccountJira, api.CodeKeyringError, ""},
		{api.AccountIMAP, api.CodeAuthFailed, ""},
		{"", api.CodeAuthRequired, ""},
	}
	for _, tt := range tests {
		if got := AuthBannerText(tt.kind, tt.reason, "Acme", tr); got != tt.want {
			t.Errorf("AuthBannerText(%q, %v) = %q, want %q", tt.kind, tt.reason, got, tt.want)
		}
	}
}

func TestIsIssueURL(t *testing.T) {
	const cloud = "https://acme.atlassian.net"
	tests := []struct {
		name, raw, site string
		want            bool
	}{
		{"issue", "https://acme.atlassian.net/browse/ITSD-42", cloud, true},
		{"site root", "https://acme.atlassian.net", cloud, true},
		{"query and fragment", "https://acme.atlassian.net/browse/ITSD-42?focusedCommentId=7#comment-7", cloud, true},
		{"host case", "https://ACME.Atlassian.NET/browse/ITSD-42", cloud, true},
		{"scheme case", "HTTPS://acme.atlassian.net/browse/ITSD-42", cloud, true},
		{"site with a trailing slash", "https://acme.atlassian.net/browse/X-1", cloud + "/", true},
		{"site with upper case", "https://acme.atlassian.net/browse/X-1", "https://ACME.atlassian.net", true},
		{"http on an https site", "http://acme.atlassian.net/browse/ITSD-42", cloud, false},
		{"foreign host", "https://evil.example/browse/ITSD-42", cloud, false},
		{"site as a subdomain", "https://acme.atlassian.net.evil.example/browse/ITSD-42", cloud, false},
		{"subdomain of the site", "https://www.acme.atlassian.net/browse/ITSD-42", cloud, false},
		{"parent domain", "https://atlassian.net/browse/ITSD-42", cloud, false},
		{"similar host", "https://acme-atlassian.net/browse/ITSD-42", cloud, false},
		{"javascript", "javascript:alert(1)", cloud, false},
		{"javascript with the host", "javascript://acme.atlassian.net/%0aalert(1)", cloud, false},
		{"data", "data:text/html,<script>alert(1)</script>", cloud, false},
		{"file", "file:///etc/passwd", cloud, false},
		{"mailto", "mailto:jana@acme.atlassian.net", cloud, false},
		{"scheme-relative", "//acme.atlassian.net/browse/ITSD-42", cloud, false},
		{"relative", "/browse/ITSD-42", cloud, false},
		{"opaque", "https:acme.atlassian.net/browse/ITSD-42", cloud, false},
		{"userinfo", "https://jana@acme.atlassian.net/browse/ITSD-42", cloud, false},
		{"userinfo hiding the host", "https://acme.atlassian.net@evil.example/browse/ITSD-42", cloud, false},
		{"userinfo with password", "https://jana:secret@acme.atlassian.net/", cloud, false},
		{"backslash", "https://acme.atlassian.net\\@evil.example/", cloud, false},
		{"backslash in the path", "https://acme.atlassian.net/browse\\ITSD-42", cloud, false},
		{"percent-encoded host", "https://%61cme.atlassian.net/browse/ITSD-42", cloud, false},
		{"percent in the path", "https://acme.atlassian.net/browse/ITSD-42%20x", cloud, true},
		{"space", "https://acme.atlassian.net/browse/ITSD 42", cloud, false},
		{"leading space", " https://acme.atlassian.net/browse/ITSD-42", cloud, false},
		{"newline", "https://acme.atlassian.net/browse/ITSD-42\n", cloud, false},
		{"invalid UTF-8", "https://acme.atlassian.net/browse/\xff", cloud, false},
		{"tab in the host", "https://acme.atlas\tsian.net/", cloud, false},
		{"bidi override", "https://acme.atlassian.net/browse/" + rlo + "24-DSTI", cloud, false},
		{"trailing dot", "https://acme.atlassian.net./browse/ITSD-42", cloud, true},
		{"trailing dot on the site", "https://acme.atlassian.net/browse/ITSD-42", "https://acme.atlassian.net.", true},
		{"two trailing dots", "https://acme.atlassian.net../browse/ITSD-42", cloud, false},
		{"empty label", "https://acme..atlassian.net/browse/ITSD-42", cloud, false},
		{"only a dot", "https://./browse/ITSD-42", cloud, false},
		{"default port", "https://acme.atlassian.net:443/browse/ITSD-42", cloud, true},
		{"other port", "https://acme.atlassian.net:8443/browse/ITSD-42", cloud, false},
		{"empty port", "https://acme.atlassian.net:/browse/ITSD-42", cloud, true},
		{"bad port", "https://acme.atlassian.net:99999/browse/ITSD-42", cloud, false},
		{"zero-padded port", "https://acme.atlassian.net:0443/browse/ITSD-42", cloud, true},
		{"site with a port", "https://jira.acme.example:8443/jira/browse/WEB-1", "https://jira.acme.example:8443/jira", true},
		{"site port missing", "https://jira.acme.example/jira/browse/WEB-1", "https://jira.acme.example:8443/jira", false},
		{"http site", "http://jira.local:8080/browse/WEB-1", "http://jira.local:8080", true},
		{"https on an http site, same port", "https://jira.local:8080/browse/WEB-1", "http://jira.local:8080", true},
		{"https on an http site, default ports", "https://jira.local/browse/WEB-1", "http://jira.local", false},
		{"http site, http default port", "http://jira.local:80/browse/WEB-1", "http://jira.local", true},
		{"IPv6 site", "http://[::1]:8080/browse/WEB-1", "http://[::1]:8080", true},
		{"IPv6 case", "https://[FE80::1]/browse/WEB-1", "https://[fe80::1]", true},
		{"IPv6 zone", "https://[fe80::1%25en0]/browse/WEB-1", "https://[fe80::1]", false},
		{"IDN to punycode site", "https://bücher.example/browse/WEB-1", "https://xn--bcher-kva.example", true},
		{"punycode to IDN site", "https://xn--bcher-kva.example/browse/WEB-1", "https://bücher.example", true},
		{"IDN upper case", "https://BÜCHER.example/browse/WEB-1", "https://bücher.example", true},
		{"IDN homograph", "https://bucher.example/browse/WEB-1", "https://bücher.example", false},
		{"cyrillic a", "https://" + string(rune(0x0430)) + "cme.atlassian.net/browse/ITSD-42", cloud, false},
		{"fullwidth dot", "https://acme" + string(rune(0x3002)) + "atlassian.net/browse/ITSD-42", cloud, false},
		{"empty", "", cloud, false},
		{"empty site", "https://acme.atlassian.net/browse/ITSD-42", "", false},
		{"site not http", "https://acme.atlassian.net/browse/ITSD-42", "ftp://acme.atlassian.net", false},
		{"site without a scheme", "https://acme.atlassian.net/browse/ITSD-42", "acme.atlassian.net", false},
	}
	for _, tt := range tests {
		if got := IsIssueURL(tt.raw, tt.site); got != tt.want {
			t.Errorf("%s: IsIssueURL(%q, %q) = %v, want %v", tt.name, tt.raw, tt.site, got, tt.want)
		}
	}
}

func TestPunycode(t *testing.T) {
	// RFC 3492 §7.1 samples (B and L) and two common names.
	tests := []struct{ in, want string }{
		{"bücher", "bcher-kva"},
		{"münchen", "mnchen-3ya"},
		{"他们为什么不说中文", "ihqwcrb4cv8a8dqg056pqjye"},
		{"Pročprostěnemluvíčesky", "Proprostnemluvesky-uyb24dma41a"},
		{"ü", "tda"},
		{"abc", "abc-"},
	}
	for _, tt := range tests {
		got, ok := punycode(tt.in)
		if !ok || got != tt.want {
			t.Errorf("punycode(%q) = %q, %v; want %q", tt.in, got, ok, tt.want)
		}
	}
	if _, ok := punycode("a\xffb"); ok {
		t.Error("invalid UTF-8 encodes")
	}
}
