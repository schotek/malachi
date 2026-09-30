// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package jira

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Invisible characters of the hostile cases, as rune constants so that no
// tool rewrites them.
const (
	bidiOverride = rune(0x202E) // RIGHT-TO-LEFT OVERRIDE
	zeroWidth    = rune(0x200B) // ZERO WIDTH SPACE
)

// cloudAccount is a stored Jira Cloud account with every setting at its
// default.
func cloudAccount() api.AccountConfig {
	return api.AccountConfig{
		Name: "Acme", Email: "jana@acme.example", Kind: api.AccountJira,
		Jira: &api.JiraConfig{
			SiteURL: "https://acme.atlassian.net", Deployment: api.JiraCloud,
			CloudID: "0b9e3d2c-1a2b-4c3d-8e9f-001122334455", Login: "jana@acme.example",
			Spaces: []api.SpaceRef{{ID: "10001", Key: "ITSD", Name: "IT Service Desk"}, {ID: "10002", Key: "WEB", Name: "Website"}},
		},
	}
}

// dataCenterAccount is a stored Data Center account.
func dataCenterAccount() api.AccountConfig {
	return api.AccountConfig{
		Name: "Acme Jira", Email: "jana@acme.example", Kind: api.AccountJira,
		Jira: &api.JiraConfig{
			SiteURL: "https://jira.acme.example/jira", Deployment: api.JiraDataCenter,
			Spaces: []api.SpaceRef{{ID: "20001", Key: "OPS", Name: "Operations"}},
		},
	}
}

var siteSpaces = []api.Space{
	{ID: "10001", Key: "ITSD", Name: "IT Service Desk", ServiceDesk: true, Issues: -1},
	{ID: "10003", Key: "MOB", Name: "Mobile", Issues: -1},
	{ID: "10002", Key: "WEB", Name: "Website", Issues: -1},
}

var siteStatuses = []api.IssueStatus{
	{ID: "1", Name: "Open", Category: api.StatusCategoryTodo},
	{ID: "3", Name: "In Progress", Category: api.StatusCategoryInProgress},
	{ID: "5", Name: "Resolved", Category: api.StatusCategoryDone},
	{ID: "6", Name: "Done", Category: api.StatusCategoryDone},
	{ID: "10010", Name: "Done", Category: api.StatusCategoryDone},
	{ID: "10020", Name: "Waiting for Customer", Category: "waiting"},
	{ID: "10021", Name: "Done", Category: api.StatusCategoryInProgress},
}

func TestSettingsTexts(t *testing.T) {
	s := SettingsTexts(tr)
	v := reflect.ValueOf(s)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).String() == "" {
			t.Errorf("SettingsStrings.%s is empty", v.Type().Field(i).Name)
		}
	}
	if s.Title != "Jira Account" || s.SpacesTitle != "Spaces" || s.ShowEvents != "Show Status and Assignee Changes" ||
		s.ReplaceToken != "Replace Token…" || s.FoldersTitle != "Folders" || s.Saving != "Saving the account" {
		t.Errorf("SettingsTexts = %+v", s)
	}
	cs := catalog{{"jira", "Spaces"}: "Prostory", {"", "Spaces"}: "Mezery", {"", "Bot Accounts"}: "Účty botů"}
	got := SettingsTexts(cs)
	if got.SpacesTitle != "Prostory" || got.BotNames != "Účty botů" {
		t.Errorf("SettingsTexts in Czech = %q, %q", got.SpacesTitle, got.BotNames)
	}
}

func TestSettingsSite(t *testing.T) {
	cloud := cloudAccount()
	got := SettingsSite(cloud, nil, tr)
	want := SiteInfo{Address: "https://acme.atlassian.net", Deployment: "Jira Cloud", User: "jana@acme.example", TokenLabel: "API Token"}
	if got != want {
		t.Errorf("before the listing = %+v", got)
	}
	got = SettingsSite(cloud, &api.SiteUser{Name: "Jana Dvořáková", Email: "jana@acme.example"}, tr)
	want.User, want.UserDetail = "Jana Dvořáková", "jana@acme.example"
	if got != want {
		t.Errorf("with the user = %+v", got)
	}

	dc := dataCenterAccount()
	got = SettingsSite(dc, &api.SiteUser{Name: "jdvorakova"}, tr)
	want = SiteInfo{
		Address: "https://jira.acme.example/jira", Deployment: "Jira Data Center", User: "jdvorakova",
		UserDetail: "jana@acme.example", TokenLabel: "Personal Access Token",
	}
	if got != want {
		t.Errorf("data center = %+v", got)
	}
	// A user without a name is the address; a name that is the address has
	// no detail.
	if got = SettingsSite(dc, &api.SiteUser{}, tr); got.User != "jana@acme.example" || got.UserDetail != "" {
		t.Errorf("a nameless user = %+v", got)
	}
	if got = SettingsSite(dc, &api.SiteUser{Name: "jana@acme.example", Email: "jana@acme.example"}, tr); got.UserDetail != "" {
		t.Errorf("a name that is the address = %+v", got)
	}
	// Hostile display text is cleaned.
	hostile := &api.SiteUser{Name: "Jana" + string(bidiOverride) + "\nDvořáková\x00", Email: "a@b.example\r\nX: y"}
	if got = SettingsSite(dc, hostile, tr); got.User != "Jana Dvořáková" || got.UserDetail != "a@b.example X: y" {
		t.Errorf("hostile user = %+q", got)
	}
	// An account of another kind has no site.
	mail := api.AccountConfig{Email: "jana@acme.example"}
	if got = SettingsSite(mail, nil, tr); got.Address != "" || got.Deployment != "Jira" || got.User != "jana@acme.example" {
		t.Errorf("a mail account = %+v", got)
	}
}

func TestSettingsSpaceRows(t *testing.T) {
	stored := cloudAccount().Jira.Spaces
	rows := SettingsSpaceRows(stored, siteSpaces, tr)
	want := []SpaceRow{
		{ID: "10001", Title: "ITSD – IT Service Desk", ServiceDesk: true},
		{ID: "10003", Title: "MOB – Mobile"},
		{ID: "10002", Title: "WEB – Website"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows = %+v", rows)
	}
	// A stored space the listing lacks stays, after the listed ones.
	gone := append([]api.SpaceRef{{ID: "10009", Key: "OLD", Name: "Archive"}}, stored...)
	rows = SettingsSpaceRows(gone, siteSpaces, tr)
	if len(rows) != 4 || rows[3] != (SpaceRow{ID: "10009", Title: "OLD – Archive"}) {
		t.Errorf("rows with a space that is gone = %+v", rows)
	}
	// Without a listing the stored spaces are the rows.
	rows = SettingsSpaceRows(stored, nil, tr)
	want = []SpaceRow{{ID: "10001", Title: "ITSD – IT Service Desk"}, {ID: "10002", Title: "WEB – Website"}}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows without a listing = %+v", rows)
	}
	if rows = SettingsSpaceRows(nil, nil, tr); len(rows) != 0 {
		t.Errorf("no spaces at all = %+v", rows)
	}
	// A stored space listed twice is one row.
	twice := []api.SpaceRef{{ID: "7", Key: "A"}, {ID: "7", Key: "A"}}
	if rows = SettingsSpaceRows(twice, nil, tr); len(rows) != 1 {
		t.Errorf("a space stored twice = %+v", rows)
	}
}

func TestSetSpaceSelected(t *testing.T) {
	stored := append([]api.SpaceRef{{ID: "10009", Key: "OLD", Name: "Archive"}}, cloudAccount().Jira.Spaces...)
	ids := func(refs []api.SpaceRef) string {
		var out []string
		for _, r := range refs {
			out = append(out, r.ID)
		}
		return strings.Join(out, ",")
	}
	// Ticking one: the order of the rows, the listed ones first.
	got := SetSpaceSelected(stored, stored, siteSpaces, "10003", true)
	if ids(got) != "10001,10003,10002,10009" {
		t.Errorf("after ticking = %s", ids(got))
	}
	// The key and the name come from the listing.
	renamed := []api.Space{{ID: "10001", Key: "HELP", Name: "Help Desk"}}
	got = SetSpaceSelected(stored, stored, renamed, "10002", false)
	if ids(got) != "10001,10009" || got[0] != (api.SpaceRef{ID: "10001", Key: "HELP", Name: "Help Desk"}) {
		t.Errorf("after unticking = %+v", got)
	}
	// Unticking the last one leaves nothing; an unknown id changes nothing.
	one := []api.SpaceRef{{ID: "10001", Key: "ITSD"}}
	if got = SetSpaceSelected(one, one, nil, "10001", false); len(got) != 0 {
		t.Errorf("after unticking the last = %+v", got)
	}
	if got = SetSpaceSelected(one, one, nil, "nope", true); ids(got) != "10001" {
		t.Errorf("after ticking an unknown space = %+v", got)
	}
}

func TestFolders(t *testing.T) {
	if !reflect.DeepEqual(VirtualFolders, []api.VirtualFolder{api.VirtualAssignedToMe, api.VirtualWatching, api.VirtualOpen}) {
		t.Errorf("VirtualFolders = %v", VirtualFolders)
	}
	for _, v := range VirtualFolders {
		if !FolderShown(nil, v) {
			t.Errorf("%q is hidden by default", v)
		}
	}
	disabled := SetFolderShown(nil, api.VirtualOpen, false)
	disabled = SetFolderShown(disabled, api.VirtualAssignedToMe, false)
	if !reflect.DeepEqual(disabled, []api.VirtualFolder{api.VirtualAssignedToMe, api.VirtualOpen}) {
		t.Errorf("disabled = %v", disabled)
	}
	if FolderShown(disabled, api.VirtualOpen) || !FolderShown(disabled, api.VirtualWatching) {
		t.Errorf("FolderShown of %v", disabled)
	}
	// Hiding twice, and an unknown or repeated stored view, leave one entry
	// per known view.
	disabled = SetFolderShown([]api.VirtualFolder{"open", "open", "archive"}, api.VirtualOpen, false)
	if !reflect.DeepEqual(disabled, []api.VirtualFolder{api.VirtualOpen}) {
		t.Errorf("disabled after repeating = %v", disabled)
	}
	if disabled = SetFolderShown(disabled, api.VirtualOpen, true); disabled != nil {
		t.Errorf("everything shown = %v, want nil", disabled)
	}
}

func TestNotificationModes(t *testing.T) {
	labels := NotificationModeLabels(tr)
	want := []string{"Check the Issue at Once", "Check the Issue and Hide the E-mail", "Do Nothing"}
	if !reflect.DeepEqual(labels, want) || len(labels) != len(NotificationModes) {
		t.Errorf("labels = %q", labels)
	}
	cases := map[api.NotificationMailMode]int{"": 0, "sync": 0, "hide": 1, "ignore": 2, "later": 0}
	for mode, index := range cases {
		if got := IndexOfNotificationMode(mode); got != index {
			t.Errorf("IndexOfNotificationMode(%q) = %d", mode, got)
		}
	}
	if got := NotificationHint(api.NotificationMailHide, tr); got != "Hidden e-mails stay in your mailbox and come back when you turn this off" {
		t.Errorf("hint of hide = %q", got)
	}
	for _, mode := range []api.NotificationMailMode{"", api.NotificationMailSync, api.NotificationMailIgnore} {
		if got := NotificationHint(mode, tr); got != "" {
			t.Errorf("hint of %q = %q", mode, got)
		}
	}
	if !SendersEditable("") || !SendersEditable(api.NotificationMailHide) || SendersEditable(api.NotificationMailIgnore) {
		t.Error("SendersEditable")
	}
}

func TestDefaultSenders(t *testing.T) {
	if got := DefaultSenders(cloudAccount()); got != "@acme.atlassian.net" {
		t.Errorf("cloud = %q", got)
	}
	if got := DefaultSenders(dataCenterAccount()); got != "" {
		t.Errorf("data center = %q", got)
	}
	upper := cloudAccount()
	upper.Jira.SiteURL = "https://ACME.Atlassian.NET:8443/"
	if got := DefaultSenders(upper); got != "@acme.atlassian.net" {
		t.Errorf("upper case with a port = %q", got)
	}
	broken := cloudAccount()
	broken.Jira.SiteURL = "::"
	if got := DefaultSenders(broken); got != "" {
		t.Errorf("no host = %q", got)
	}
	if got := DefaultSenders(api.AccountConfig{Email: "a@b.example"}); got != "" {
		t.Errorf("a mail account = %q", got)
	}
}

// groupsText is the picker on one line: "Title[style]: Name*(ids) Name(ids); …",
// a star for a selected choice.
func groupsText(groups []StatusGroup) string {
	var out []string
	for _, g := range groups {
		var choices []string
		for _, c := range g.Choices {
			mark := ""
			if c.Selected {
				mark = "*"
			}
			choices = append(choices, fmt.Sprintf("%s%s(%s)", c.Name, mark, strings.Join(c.IDs, ",")))
		}
		out = append(out, fmt.Sprintf("%s[%s]: %s", g.Title, g.Style, strings.Join(choices, " ")))
	}
	return strings.Join(out, "; ")
}

func TestStatusGroups(t *testing.T) {
	// The default: the statuses of the category done; a name is one choice
	// per category, with every id of it.
	got := groupsText(StatusGroups(siteStatuses, nil, tr))
	want := "To Do[status-todo]: Open(1); " +
		"In Progress[status-in-progress]: In Progress(3) Done(10021); " +
		"Done[status-done]: Resolved*(5) Done*(6,10010); " +
		"Other[]: Waiting for Customer(10020)"
	if got != want {
		t.Errorf("default\n got %s\nwant %s", got, want)
	}
	// Stored statuses: a choice is selected when one of its ids is stored;
	// a stored status the site lacks is listed among the others.
	closed := []api.StatusRef{{ID: "10010", Name: "Done"}, {ID: "10020"}, {ID: "777", Name: "Cancelled"}, {ID: "778"}}
	got = groupsText(StatusGroups(siteStatuses, closed, tr))
	want = "To Do[status-todo]: Open(1); " +
		"In Progress[status-in-progress]: In Progress(3) Done(10021); " +
		"Done[status-done]: Resolved(5) Done*(6,10010); " +
		"Other[]: Waiting for Customer*(10020) Cancelled*(777) 778*(778)"
	if got != want {
		t.Errorf("stored\n got %s\nwant %s", got, want)
	}
	// Without a listing only the stored ones are known.
	got = groupsText(StatusGroups(nil, closed[:1], tr))
	if got != "Other[]: Done*(10010)" {
		t.Errorf("without a listing = %s", got)
	}
	if groups := StatusGroups(nil, nil, tr); len(groups) != 0 {
		t.Errorf("nothing known = %+v", groups)
	}
	// Hostile names are cleaned; a status without an id or listed twice is
	// left out; a nameless one shows its id.
	hostile := []api.IssueStatus{
		{ID: "1", Name: "Do" + string(zeroWidth) + "ne\n" + string(bidiOverride) + "now", Category: api.StatusCategoryDone},
		{ID: "", Name: "Nameless", Category: api.StatusCategoryDone},
		{ID: "1", Name: "Again", Category: api.StatusCategoryTodo},
		{ID: "2", Name: " \t ", Category: api.StatusCategoryTodo},
	}
	got = groupsText(StatusGroups(hostile, nil, tr))
	if got != "To Do[status-todo]: 2(2); Done[status-done]: Done now*(1)" {
		t.Errorf("hostile = %q", got)
	}
	cs := catalog{{"status category", "Done"}: "Hotovo", {"", "Done"}: "Dokončeno"}
	if groups := StatusGroups(siteStatuses[2:3], nil, cs); len(groups) != 1 || groups[0].Title != "Hotovo" {
		t.Errorf("Czech title = %+v", groups)
	}
}

func TestSetStatusSelected(t *testing.T) {
	ids := func(refs []api.StatusRef) string {
		var out []string
		for _, r := range refs {
			out = append(out, r.ID)
		}
		return strings.Join(out, ",")
	}
	choice := func(closed []api.StatusRef, name string) StatusChoice {
		for _, g := range StatusGroups(siteStatuses, closed, tr) {
			for _, c := range g.Choices {
				if c.Name == name {
					return c
				}
			}
		}
		t.Fatalf("no choice %q", name)
		return StatusChoice{}
	}

	if got := DefaultClosedStatuses(siteStatuses); ids(got) != "5,6,10010" || got[1].Name != "Done" {
		t.Errorf("DefaultClosedStatuses = %+v", got)
	}
	// From the default: ticking adds to the statuses of the category done.
	closed := SetStatusSelected(siteStatuses, nil, choice(nil, "Waiting for Customer"), true)
	if ids(closed) != "5,6,10010,10020" || closed[3].Name != "Waiting for Customer" {
		t.Errorf("after ticking = %+v", closed)
	}
	// Unticking a name takes every status of it.
	closed = SetStatusSelected(siteStatuses, closed, choice(closed, "Resolved"), false)
	if ids(closed) != "6,10010,10020" {
		t.Errorf("after unticking Resolved = %s", ids(closed))
	}
	// Back at the statuses of the category done: the default, nil.
	closed = SetStatusSelected(siteStatuses, closed, choice(closed, "Resolved"), true)
	closed = SetStatusSelected(siteStatuses, closed, choice(closed, "Waiting for Customer"), false)
	if closed != nil {
		t.Errorf("the default again = %+v, want nil", closed)
	}
	// One stored id of a name: ticking stores the others too.
	closed = SetStatusSelected(siteStatuses, []api.StatusRef{{ID: "10010"}, {ID: "1"}}, StatusChoice{Name: "Done", IDs: []string{"6", "10010"}}, true)
	if ids(closed) != "10010,1,6" {
		t.Errorf("after ticking a name stored in part = %s", ids(closed))
	}
	// Nothing left is the default.
	closed = SetStatusSelected(siteStatuses, []api.StatusRef{{ID: "1"}}, StatusChoice{Name: "Open", IDs: []string{"1"}}, false)
	if closed != nil {
		t.Errorf("nothing left = %+v, want nil", closed)
	}
	// Without a listing the stored ones can still be unticked.
	stored := []api.StatusRef{{ID: "777", Name: "Cancelled"}, {ID: "5", Name: "Resolved"}}
	closed = SetStatusSelected(nil, stored, StatusChoice{Name: "Cancelled", IDs: []string{"777"}}, false)
	if ids(closed) != "5" {
		t.Errorf("without a listing = %s", ids(closed))
	}
	// A choice the site does not list keeps its name.
	closed = SetStatusSelected(nil, stored[1:], StatusChoice{Name: "Cancelled", IDs: []string{"777"}}, true)
	if ids(closed) != "5,777" || closed[1].Name != "Cancelled" {
		t.Errorf("a choice that is not listed = %+v", closed)
	}
}

func TestStatusesProblem(t *testing.T) {
	var many []api.StatusRef
	for i := 0; i < api.MaxJiraStatuses; i++ {
		many = append(many, api.StatusRef{ID: fmt.Sprint(i)})
	}
	if got := StatusesProblem(many, tr); got != "" {
		t.Errorf("at the limit = %q", got)
	}
	many = append(many, api.StatusRef{ID: "x"})
	if got := StatusesProblem(many, tr); got != "Select at most 64 statuses" {
		t.Errorf("over the limit = %q", got)
	}
	if got := StatusesProblem(nil, tr); got != "" {
		t.Errorf("none = %q", got)
	}
}

func TestNormaliseList(t *testing.T) {
	tests := []struct {
		kind ListKind
		in   []string
		want []string
	}{
		{ListBotNames, nil, nil},
		{ListBotNames, []string{"", "  "}, nil},
		{ListBotNames, []string{" Issue Sync – Synchronization for Jira ", "issue sync - synchronization  for jira", "Deploy Bot"},
			[]string{"Issue Sync – Synchronization for Jira", "Deploy Bot"}},
		{ListMetadataFilters, []string{`^a$`, ` ^a$ `, `^A$`, ""}, []string{`^a$`, `^A$`}},
		{ListAuthorPrefixes, []string{"ACME", "acme", " Acme s.r.o. "}, []string{"ACME", "Acme s.r.o."}},
		{ListSenders, []string{" Jira@Acme.Example ", "jira@acme.example", "@ACME.example"}, []string{"jira@acme.example", "@acme.example"}},
	}
	for _, tt := range tests {
		if got := NormaliseList(tt.kind, tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("NormaliseList(%d, %q) = %q, want %q", tt.kind, tt.in, got, tt.want)
		}
	}
}

// patternCases are patterns and what regexp/syntax says of them ("" for a
// valid one). The port's checker is tested with the same table.
var patternCases = []struct{ pattern, reason string }{
	{`^Remote comment create date:.*$`, ""},
	{`abc`, ""},
	{`a|b`, ""},
	{`(a|b)*c+?`, ""},
	{`příliš žluťoučký`, ""},
	{`[a-z]+`, ""},
	{`[]a]`, ""},
	{`[^]a]`, ""},
	{`[a-]`, ""},
	{`[-a]`, ""},
	{`[a\-z]`, ""},
	{`[á-ž]`, ""},
	{`[ž-á]`, "invalid character class range"},
	{`[z-a]`, "invalid character class range"},
	{`[a`, "missing closing ]"},
	{`[`, "missing closing ]"},
	{`[]`, "missing closing ]"},
	{`[^]`, "missing closing ]"},
	{`[a-`, "missing closing ]"},
	{`[a\`, "trailing backslash at end of expression"},
	{`[a-\d]`, "invalid escape sequence"},
	{`[\d-z]`, ""},
	{`[\b]`, "invalid escape sequence"},
	{`[\x41-\x5a]`, ""},
	{`[a-z&&[^b]]`, ""},
	{`[[:alpha:]]`, ""},
	{`[[:^alpha:]]`, ""},
	{`[[:word:][:xdigit:]]`, ""},
	{`[[:foo:]]`, "invalid character class range"},
	{`[[:alpha:]`, "missing closing ]"},
	{`[[=a=]]`, ""},
	{`\d+\s\w\D\S\W`, ""},
	{`\pL+`, ""},
	{`\PL`, ""},
	{`\p{Greek}`, ""},
	{`\p{^Greek}`, ""},
	{`\p{Lu}`, ""},
	{`[\pL\d]`, ""},
	{`\p{Foo}`, "invalid character class range"},
	{`[\p{Foo}]`, "invalid character class range"},
	{`\p{L`, "invalid character class range"},
	{`\pX`, "invalid character class range"},
	{`(?i)abc`, ""},
	{`(?i:abc)`, ""},
	{`(?i-s:abc)`, ""},
	{`(?U)a*`, ""},
	{`(?m)^a$`, ""},
	{`(?s).`, ""},
	{`(?i)(?-i)`, ""},
	{`(?P<name>a)`, ""},
	{`(?<name>a)`, ""},
	{`(?P<na me>a)`, "invalid named capture"},
	{`(?P<>a)`, "invalid named capture"},
	{`(?P<n`, "invalid named capture"},
	{`(?<=a)`, "invalid named capture"},
	{`(?<!a)`, "invalid named capture"},
	{`(?=a)`, "invalid or unsupported Perl syntax"},
	{`(?!a)`, "invalid or unsupported Perl syntax"},
	{`(?>a)`, "invalid or unsupported Perl syntax"},
	{`(?#c)`, "invalid or unsupported Perl syntax"},
	{`(?'n'a)`, "invalid or unsupported Perl syntax"},
	{`(?x)a`, "invalid or unsupported Perl syntax"},
	{`(?i`, "invalid or unsupported Perl syntax"},
	{`(?`, "invalid or unsupported Perl syntax"},
	{`(?-)`, "invalid or unsupported Perl syntax"},
	{`(?i-)`, "invalid or unsupported Perl syntax"},
	{`(?--i)`, "invalid or unsupported Perl syntax"},
	{`(`, "missing closing )"},
	{`(a`, "missing closing )"},
	{`((a)`, "missing closing )"},
	{`)`, "unexpected )"},
	{`a)`, "unexpected )"},
	{`(a))`, "unexpected )"},
	{`()`, ""},
	{`(?:)`, ""},
	{`(a|b|)`, ""},
	{`a||b`, ""},
	{`|`, ""},
	{`*`, "missing argument to repetition operator"},
	{`+a`, "missing argument to repetition operator"},
	{`?`, "missing argument to repetition operator"},
	{`{2}`, "missing argument to repetition operator"},
	{`|*`, "missing argument to repetition operator"},
	{`a|*`, "missing argument to repetition operator"},
	{`(*)`, "missing argument to repetition operator"},
	{`(?i)*`, "missing argument to repetition operator"},
	{`(|a)*`, ""},
	{`(?:)*`, ""},
	{`^*`, ""},
	{`$*`, ""},
	{`\b*`, ""},
	{`a**`, "invalid nested repetition operator"},
	{`a*+`, "invalid nested repetition operator"},
	{`a++`, "invalid nested repetition operator"},
	{`a???`, "invalid nested repetition operator"},
	{`a{2}{3}`, "invalid nested repetition operator"},
	{`a{2}*`, "invalid nested repetition operator"},
	{`a{2}+`, "invalid nested repetition operator"},
	{`a*?`, ""},
	{`a??`, ""},
	{`a{2,3}?`, ""},
	{`a{2}`, ""},
	{`a{2,}`, ""},
	{`a{2,3}`, ""},
	{`a{0}`, ""},
	{`a{01}`, ""},
	{`a{,3}`, ""},
	{`a{}`, ""},
	{`a{2`, ""},
	{`x{2}{`, ""},
	{`a{1000}`, ""},
	{`a{1001}`, "invalid repeat count"},
	{`a{3,2}`, "invalid repeat count"},
	{`a{100000000000}`, "invalid repeat count"},
	{`(a{2}){3}`, ""},
	{`(a{100}){10}`, ""},
	{`(a{100}){11}`, "invalid repeat count"},
	{`((a{10}){10}){10}`, ""},
	{`((a{10}){10}){11}`, "invalid repeat count"},
	{`(a{1000}){0}`, ""},
	{`(a{1000}b{1000}c{1000}d{1000}){1000}`, "expression too large"},
	{`\`, "trailing backslash at end of expression"},
	{`a\`, "trailing backslash at end of expression"},
	{`\.`, ""},
	{`\_`, ""},
	{`\-`, ""},
	{`\q`, "invalid escape sequence"},
	{`\e`, "invalid escape sequence"},
	{`\h`, "invalid escape sequence"},
	{`\1`, "invalid escape sequence"},
	{`\8`, "invalid escape sequence"},
	{`\12`, ""},
	{`\0`, ""},
	{`\07`, ""},
	{`\x41`, ""},
	{`\x4`, "invalid escape sequence"},
	{`\x`, "invalid escape sequence"},
	{`\xg1`, "invalid escape sequence"},
	{`\x{41}`, ""},
	{`\x{10FFFF}`, ""},
	{`\x{110000}`, "invalid escape sequence"},
	{`\x{}`, "invalid escape sequence"},
	{`\x{4g}`, "invalid escape sequence"},
	{`\a\f\n\r\t\v`, ""},
	{`\A\z`, ""},
	{`\b\B`, ""},
	{`\Z`, "invalid escape sequence"},
	{`\C`, "invalid escape sequence"},
	{`\G`, "invalid escape sequence"},
	{`\cA`, "invalid escape sequence"},
	{`\k<n>`, "invalid escape sequence"},
	{`\N{x}`, "invalid escape sequence"},
	{`\E`, "invalid escape sequence"},
	{`\Q.*\E`, ""},
	{`\Q.*`, ""},
	{`\Qa\Eb*`, ""},
	{`\Q(\E)`, "unexpected )"},
	{`[\Q]\E]`, "invalid escape sequence"},
}

func TestPatternError(t *testing.T) {
	for _, tt := range patternCases {
		if got := PatternError(tt.pattern); got != tt.reason {
			t.Errorf("PatternError(%q) = %q, want %q", tt.pattern, got, tt.reason)
		}
	}
	if got := PatternError(SuggestedMetadataFilter); got != "" {
		t.Errorf("the suggested pattern = %q", got)
	}
	// A pattern as long as an entry may be, of groups nested as deep as
	// that allows.
	deep := strings.Repeat("(", api.MaxJiraPatternBytes/2) + strings.Repeat(")", api.MaxJiraPatternBytes/2)
	if got := PatternError(deep); got != "" {
		t.Errorf("deep nesting = %q", got)
	}
	if got := PatternError(deep[1:]); got != "unexpected )" {
		t.Errorf("deep nesting, one too many = %q", got)
	}
}

func TestCheckEntry(t *testing.T) {
	const (
		tooLong   = "This entry is too long"
		control   = "This entry contains control characters"
		duplicate = "This entry is already in the list"
		full      = "The list holds at most 32 entries"
		sender    = "Enter an address, or a domain such as @example.org"
		short     = "A bot name needs at least 3 characters"
	)
	var thirtyTwo []string
	for i := 0; i < api.MaxJiraListEntries; i++ {
		thirtyTwo = append(thirtyTwo, fmt.Sprintf("entry %d", i))
	}
	exact := strings.Repeat("a", api.MaxJiraPatternBytes)
	tests := []struct {
		kind    ListKind
		in      string
		have    []string
		entry   string
		problem string
	}{
		// Nothing typed is nothing to add, and no problem.
		{ListBotNames, "", nil, "", ""},
		{ListMetadataFilters, " \t ", nil, "", ""},

		{ListBotNames, "  Issue Sync – Synchronization for Jira ", nil, "Issue Sync – Synchronization for Jira", ""},
		{ListBotNames, "Bot", nil, "Bot", ""},
		{ListBotNames, "Žů", nil, "", short},
		{ListBotNames, "a" + string(zeroWidth) + string(zeroWidth) + "b", nil, "", short},
		{ListBotNames, "issue sync - synchronization for jira", []string{SuggestedBotName}, "", duplicate},
		{ListBotNames, "Deploy\tBot", nil, "", control},
		{ListBotNames, "Deploy Bot\x00", nil, "", control},
		{ListBotNames, exact, nil, exact, ""},
		{ListBotNames, exact + "a", nil, "", tooLong},
		{ListBotNames, strings.Repeat("ž", api.MaxJiraPatternBytes/2+1), nil, "", tooLong},
		{ListBotNames, "One More", thirtyTwo, "", full},
		{ListBotNames, "Entry 3", thirtyTwo, "", duplicate},

		{ListMetadataFilters, ` ^Remote comment create date:.*$ `, nil, `^Remote comment create date:.*$`, ""},
		{ListMetadataFilters, `^a$`, []string{`^A$`}, `^a$`, ""},
		{ListMetadataFilters, `^a$`, []string{` ^a$`}, "", duplicate},
		{ListMetadataFilters, `(a`, nil, "", "This pattern is not valid: missing closing )"},
		{ListMetadataFilters, `(?=a)`, nil, "", "This pattern is not valid: invalid or unsupported Perl syntax"},
		{ListMetadataFilters, `a{1001}`, nil, "", "This pattern is not valid: invalid repeat count"},
		{ListMetadataFilters, "a\nb", nil, "", control},
		{ListMetadataFilters, "x", thirtyTwo, "", full},

		{ListAuthorPrefixes, " ACME ", nil, "ACME", ""},
		{ListAuthorPrefixes, "a", nil, "a", ""},
		{ListAuthorPrefixes, "acme", []string{"ACME"}, "", duplicate},
		{ListAuthorPrefixes, string(zeroWidth), nil, "", control},

		{ListSenders, " Jira@Acme.Example ", nil, "jira@acme.example", ""},
		{ListSenders, "@Acme.Example", nil, "@acme.example", ""},
		{ListSenders, "@localhost", nil, "@localhost", ""},
		{ListSenders, "JIRA@acme.example", []string{"jira@acme.example"}, "", duplicate},
		{ListSenders, "acme.example", nil, "", sender},
		{ListSenders, "@", nil, "", sender},
		{ListSenders, "@acme..example", nil, "", sender},
		{ListSenders, "@-acme.example", nil, "", sender},
		{ListSenders, "@acme_.example", nil, "", sender},
		{ListSenders, "@acme.example/path", nil, "", sender},
		{ListSenders, "Jira <jira@acme.example>", nil, "", sender},
		{ListSenders, "jira@acme.example, x@acme.example", nil, "", sender},
		{ListSenders, "jira@", nil, "", sender},
		{ListSenders, "@" + strings.Repeat("a", 64) + ".example", nil, "", sender},
	}
	for _, tt := range tests {
		entry, problem := CheckEntry(tt.kind, tt.in, tt.have, tr)
		if entry != tt.entry || problem != tt.problem {
			t.Errorf("CheckEntry(%d, %q) = %q, %q; want %q, %q", tt.kind, tt.in, entry, problem, tt.entry, tt.problem)
		}
	}
}

func TestSuggestions(t *testing.T) {
	got := Suggestions(ListBotNames, nil, tr)
	want := []Suggestion{{Value: "Issue Sync – Synchronization for Jira", Label: "Add Issue Sync – Synchronization for Jira"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bot names = %+v", got)
	}
	got = Suggestions(ListMetadataFilters, []string{`^x$`}, tr)
	want = []Suggestion{{Value: `^Remote comment create date:.*$`, Label: `Add ^Remote comment create date:.*$`}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("patterns = %+v", got)
	}
	// Not once the list has it (a bot name in any spelling of its dash).
	if got = Suggestions(ListBotNames, []string{"Deploy Bot", " issue sync - Synchronization for JIRA"}, tr); got != nil {
		t.Errorf("bot names with the suggestion = %+v", got)
	}
	if got = Suggestions(ListMetadataFilters, []string{`^Remote comment create date:.*$`}, tr); got != nil {
		t.Errorf("patterns with the suggestion = %+v", got)
	}
	// Not for the other lists, and not for a full list.
	if Suggestions(ListAuthorPrefixes, nil, tr) != nil || Suggestions(ListSenders, nil, tr) != nil {
		t.Error("suggestions for a list without any")
	}
	var full []string
	for i := 0; i < api.MaxJiraListEntries; i++ {
		full = append(full, fmt.Sprintf("Bot %d", i))
	}
	if got = Suggestions(ListBotNames, full, tr); got != nil {
		t.Errorf("a full list = %+v", got)
	}
	cs := catalog{{"", "Add %s"}: "Přidat %s"}
	if got = Suggestions(ListBotNames, nil, cs); len(got) != 1 || got[0].Label != "Přidat Issue Sync – Synchronization for Jira" {
		t.Errorf("Czech = %+v", got)
	}
	// Both suggestions pass the page's own check.
	if entry, problem := CheckEntry(ListBotNames, SuggestedBotName, nil, tr); entry != SuggestedBotName || problem != "" {
		t.Errorf("the suggested bot name = %q, %q", entry, problem)
	}
	if entry, problem := CheckEntry(ListMetadataFilters, SuggestedMetadataFilter, nil, tr); entry != SuggestedMetadataFilter || problem != "" {
		t.Errorf("the suggested pattern = %q, %q", entry, problem)
	}
}

func TestNewSettingsForm(t *testing.T) {
	got := NewSettingsForm(cloudAccount())
	want := SettingsForm{
		Name:       "Acme",
		Spaces:     []api.SpaceRef{{ID: "10001", Key: "ITSD", Name: "IT Service Desk"}, {ID: "10002", Key: "WEB", Name: "Website"}},
		ShowEvents: true, NotificationMail: api.NotificationMailSync,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("defaults = %+v", got)
	}

	cfg := cloudAccount()
	cfg.Jira.OfflineDays = 45
	cfg.Jira.OnlyMine = true
	cfg.Jira.HideEvents = true
	cfg.Jira.DisabledFolders = []api.VirtualFolder{api.VirtualWatching}
	cfg.Jira.ClosedStatuses = []api.StatusRef{{ID: "5", Name: "Resolved"}}
	cfg.Jira.NotificationMail = api.NotificationMailHide
	cfg.Jira.NotificationSenders = []string{"jira@acme.example"}
	cfg.Jira.BotNames = []string{SuggestedBotName}
	cfg.Jira.MetadataFilters = []string{SuggestedMetadataFilter}
	cfg.Jira.AuthorPrefixes = []string{"ACME"}
	got = NewSettingsForm(cfg)
	want = SettingsForm{
		Name: "Acme", Spaces: want.Spaces, OfflineDays: 45, OnlyMine: true, ShowEvents: false,
		DisabledFolders:     []api.VirtualFolder{api.VirtualWatching},
		ClosedStatuses:      []api.StatusRef{{ID: "5", Name: "Resolved"}},
		NotificationMail:    api.NotificationMailHide,
		NotificationSenders: []string{"jira@acme.example"},
		BotNames:            []string{SuggestedBotName},
		MetadataFilters:     []string{SuggestedMetadataFilter},
		AuthorPrefixes:      []string{"ACME"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stored = %+v", got)
	}
	// The form is a copy: editing it leaves the account alone.
	got.Spaces[0].Key = "X"
	got.BotNames[0] = "X"
	if cfg.Jira.Spaces[0].Key != "ITSD" || cfg.Jira.BotNames[0] != SuggestedBotName {
		t.Error("the form shares its lists with the account")
	}
	// A mode this client does not know is shown as the default; an account
	// of another kind has an empty form.
	cfg.Jira.NotificationMail = "later"
	if got = NewSettingsForm(cfg); got.NotificationMail != api.NotificationMailSync {
		t.Errorf("an unknown mode = %q", got.NotificationMail)
	}
	got = NewSettingsForm(api.AccountConfig{Name: "Mail", Email: "jana@acme.example"})
	if !reflect.DeepEqual(got, SettingsForm{Name: "Mail", ShowEvents: true, NotificationMail: api.NotificationMailSync}) {
		t.Errorf("a mail account = %+v", got)
	}
}

func TestSettingsFormApply(t *testing.T) {
	// Nothing edited: the account as it is stored.
	stored := cloudAccount()
	got := NewSettingsForm(stored).Apply(stored)
	if !reflect.DeepEqual(got, stored) {
		t.Errorf("unedited\n got %+v\nwant %+v", got.Jira, stored.Jira)
	}
	if Changed(stored, got) {
		t.Error("an unedited form counts as changed")
	}
	if got.Jira == stored.Jira {
		t.Error("Apply returned the account's own block")
	}

	f := NewSettingsForm(stored)
	f.Name = "  Acme Jira  "
	f.Spaces = []api.SpaceRef{{ID: "10003", Key: "MOB", Name: "Mobile"}, {ID: "10003", Key: "MOB"}, {ID: "", Key: "X"}, {ID: "10001", Key: "ITSD"}}
	f.OfflineDays = 90
	f.OnlyMine = true
	f.ShowEvents = false
	f.DisabledFolders = []api.VirtualFolder{api.VirtualOpen, api.VirtualAssignedToMe, api.VirtualOpen}
	f.ClosedStatuses = []api.StatusRef{{ID: " 5 ", Name: " Resolved "}, {ID: "5"}, {ID: ""}, {ID: "6", Name: "Done"}}
	f.NotificationMail = api.NotificationMailHide
	f.NotificationSenders = []string{" Jira@Acme.Example", "jira@acme.example", ""}
	f.BotNames = []string{" Issue Sync – Synchronization for Jira ", "issue sync - synchronization for jira"}
	f.MetadataFilters = []string{SuggestedMetadataFilter, " " + SuggestedMetadataFilter}
	f.AuthorPrefixes = []string{"ACME", "  ", "Acme"}
	got = f.Apply(stored)
	want := cloudAccount()
	want.Name = "Acme Jira"
	want.Jira.Spaces = []api.SpaceRef{{ID: "10003", Key: "MOB", Name: "Mobile"}, {ID: "10001", Key: "ITSD"}}
	want.Jira.OfflineDays = 90
	want.Jira.OnlyMine = true
	want.Jira.HideEvents = true
	want.Jira.DisabledFolders = []api.VirtualFolder{api.VirtualAssignedToMe, api.VirtualOpen}
	want.Jira.ClosedStatuses = []api.StatusRef{{ID: "5", Name: "Resolved"}, {ID: "6", Name: "Done"}}
	want.Jira.NotificationMail = api.NotificationMailHide
	want.Jira.NotificationSenders = []string{"jira@acme.example"}
	want.Jira.BotNames = []string{"Issue Sync – Synchronization for Jira"}
	want.Jira.MetadataFilters = []string{SuggestedMetadataFilter}
	want.Jira.AuthorPrefixes = []string{"ACME"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("edited\n got %+v\nwant %+v", got.Jira, want.Jira)
	}
	if !Changed(stored, got) {
		t.Error("an edited form does not count as changed")
	}
	// The connection stays, and the stored account is not touched.
	if got.Jira.SiteURL != stored.Jira.SiteURL || got.Jira.Login != stored.Jira.Login || got.Jira.CloudID != stored.Jira.CloudID ||
		got.Email != stored.Email {
		t.Errorf("the connection changed: %+v", got.Jira)
	}
	if !reflect.DeepEqual(stored, cloudAccount()) {
		t.Error("Apply changed the stored account")
	}

	// Back to the defaults: everything optional is left out.
	back := NewSettingsForm(got)
	back.OfflineDays = 0
	back.OnlyMine = false
	back.ShowEvents = true
	back.DisabledFolders = nil
	back.ClosedStatuses = nil
	back.NotificationMail = api.NotificationMailSync
	back.NotificationSenders, back.BotNames, back.MetadataFilters, back.AuthorPrefixes = nil, []string{" "}, nil, nil
	back.Spaces = cloudAccount().Jira.Spaces
	back.Name = "Acme"
	if got = back.Apply(got); !reflect.DeepEqual(got, cloudAccount()) {
		t.Errorf("defaults again\n got %+v\nwant %+v", got.Jira, cloudAccount().Jira)
	}

	// An empty name is the site's host; the window is kept within the limit.
	f = NewSettingsForm(stored)
	f.Name = " \t"
	f.OfflineDays = 5000
	if got = f.Apply(stored); got.Name != "acme.atlassian.net" || got.Jira.OfflineDays != api.MaxJiraOfflineDays {
		t.Errorf("an empty name and a long window = %q, %d", got.Name, got.Jira.OfflineDays)
	}
	f.OfflineDays = -3
	f.Name = strings.Repeat("ž", 200)
	if got = f.Apply(stored); got.Jira.OfflineDays != 0 || len(got.Name) != 256 {
		t.Errorf("a negative window and a long name = %d, %d bytes", got.Jira.OfflineDays, len(got.Name))
	}
	// An unknown mode is the default; an account of another kind is left alone.
	f.NotificationMail = "later"
	if got = f.Apply(stored); got.Jira.NotificationMail != "" {
		t.Errorf("an unknown mode = %q", got.Jira.NotificationMail)
	}
	mail := api.AccountConfig{Name: "Mail", Email: "jana@acme.example"}
	if got = f.Apply(mail); !reflect.DeepEqual(got, mail) {
		t.Errorf("a mail account = %+v", got)
	}
}

func TestSettingsProblem(t *testing.T) {
	f := NewSettingsForm(cloudAccount())
	if got := f.SettingsProblem(tr); got != "" {
		t.Errorf("a stored account = %q", got)
	}
	f.Spaces = nil
	if got := f.SettingsProblem(tr); got != "Select at least one space" {
		t.Errorf("no space = %q", got)
	}
	f.Spaces = cloudAccount().Jira.Spaces
	for i := 0; i <= api.MaxJiraStatuses; i++ {
		f.ClosedStatuses = append(f.ClosedStatuses, api.StatusRef{ID: fmt.Sprint(i)})
	}
	if got := f.SettingsProblem(tr); got != "Select at most 64 statuses" {
		t.Errorf("too many statuses = %q", got)
	}
}

func TestChanged(t *testing.T) {
	edit := func(f func(*api.AccountConfig)) api.AccountConfig {
		cfg := cloudAccount()
		cfg.Jira.ClosedStatuses = []api.StatusRef{{ID: "5", Name: "Resolved"}, {ID: "6", Name: "Done"}}
		cfg.Jira.DisabledFolders = []api.VirtualFolder{api.VirtualWatching, api.VirtualOpen}
		cfg.Jira.BotNames = []string{"Deploy Bot", SuggestedBotName}
		cfg.Jira.AuthorPrefixes = []string{"ACME", "Globex"}
		if f != nil {
			f(&cfg)
		}
		return cfg
	}
	old := edit(nil)
	tests := []struct {
		name string
		f    func(*api.AccountConfig)
		want bool
	}{
		{"nothing", func(*api.AccountConfig) {}, false},
		{"the default window written out", func(c *api.AccountConfig) { c.Jira.OfflineDays = 30 }, false},
		{"the default mode written out", func(c *api.AccountConfig) { c.Jira.NotificationMail = api.NotificationMailSync }, false},
		{"spaces around the name", func(c *api.AccountConfig) { c.Name = " Acme " }, false},
		{"the order of the spaces", func(c *api.AccountConfig) { c.Jira.Spaces[0], c.Jira.Spaces[1] = c.Jira.Spaces[1], c.Jira.Spaces[0] }, false},
		{"the name of a space", func(c *api.AccountConfig) { c.Jira.Spaces[0].Name = "Help Desk" }, false},
		{"the name and order of the statuses", func(c *api.AccountConfig) {
			c.Jira.ClosedStatuses = []api.StatusRef{{ID: "6"}, {ID: "5", Name: "Closed"}, {ID: "6"}}
		}, false},
		{"the order of the views", func(c *api.AccountConfig) {
			c.Jira.DisabledFolders = []api.VirtualFolder{api.VirtualOpen, api.VirtualWatching, api.VirtualOpen}
		}, false},
		{"the order and spelling of the bots", func(c *api.AccountConfig) {
			c.Jira.BotNames = []string{" issue sync - synchronization for jira", "Deploy Bot", "deploy bot"}
		}, false},
		{"an empty list for none", func(c *api.AccountConfig) { c.Jira.MetadataFilters = []string{} }, false},
		{"the kind written out", func(c *api.AccountConfig) { c.Kind = api.AccountJira }, false},

		{"the name", func(c *api.AccountConfig) { c.Name = "Acme Jira" }, true},
		{"a space more", func(c *api.AccountConfig) {
			c.Jira.Spaces = append(c.Jira.Spaces, api.SpaceRef{ID: "10003", Key: "MOB"})
		}, true},
		{"the key of a space", func(c *api.AccountConfig) { c.Jira.Spaces[0].Key = "HELP" }, true},
		{"the window", func(c *api.AccountConfig) { c.Jira.OfflineDays = 90 }, true},
		{"only mine", func(c *api.AccountConfig) { c.Jira.OnlyMine = true }, true},
		{"the events", func(c *api.AccountConfig) { c.Jira.HideEvents = true }, true},
		{"a view", func(c *api.AccountConfig) { c.Jira.DisabledFolders = []api.VirtualFolder{api.VirtualWatching} }, true},
		{"a status", func(c *api.AccountConfig) { c.Jira.ClosedStatuses = c.Jira.ClosedStatuses[:1] }, true},
		{"no statuses", func(c *api.AccountConfig) { c.Jira.ClosedStatuses = nil }, true},
		{"the mode", func(c *api.AccountConfig) { c.Jira.NotificationMail = api.NotificationMailIgnore }, true},
		{"a sender", func(c *api.AccountConfig) { c.Jira.NotificationSenders = []string{"@acme.example"} }, true},
		{"a bot", func(c *api.AccountConfig) { c.Jira.BotNames = c.Jira.BotNames[:1] }, true},
		{"a pattern", func(c *api.AccountConfig) { c.Jira.MetadataFilters = []string{`^x$`} }, true},
		{"the order of the prefixes", func(c *api.AccountConfig) { c.Jira.AuthorPrefixes = []string{"Globex", "ACME"} }, true},
		{"the site", func(c *api.AccountConfig) { c.Jira.SiteURL = "https://globex.atlassian.net" }, true},
		{"the login", func(c *api.AccountConfig) { c.Jira.Login = "petr@acme.example" }, true},
		{"the interval", func(c *api.AccountConfig) { c.SyncInterval = 600 }, true},
	}
	for _, tt := range tests {
		if got := Changed(old, edit(tt.f)); got != tt.want {
			t.Errorf("Changed after %s = %v", tt.name, got)
		}
	}
	// Accounts of another kind compare as they are.
	mail := api.AccountConfig{Name: "Mail", Email: "jana@acme.example"}
	other := mail
	other.Email = "petr@acme.example"
	if Changed(mail, mail) || !Changed(mail, other) || !Changed(mail, old) {
		t.Error("Changed of mail accounts")
	}
}
