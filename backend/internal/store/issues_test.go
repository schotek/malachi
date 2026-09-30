// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func jiraConfig(email, site string) api.AccountConfig {
	return api.AccountConfig{
		Name: "Acme Jira", Email: email, Kind: api.AccountJira,
		Jira: &api.JiraConfig{SiteURL: site, Deployment: api.JiraCloud, Login: email,
			Spaces: []api.SpaceRef{{ID: "10001", Key: "ITSD"}}},
	}
}

// jiraFolders stores an issue-tracker account's folder list: the views,
// then one space folder.
func jiraFolders(t *testing.T, s *Store, account string) (space, assigned, watching Folder) {
	t.Helper()
	stored, _, err := s.UpsertFolders(context.Background(), account, []Folder{
		{Mailbox: "view:assignedToMe", Name: "Assigned to Me", Path: "Assigned to Me", Virtual: api.VirtualAssignedToMe, Selectable: true, Subscribed: true},
		{Mailbox: "view:watching", Name: "Watching", Path: "Watching", Virtual: api.VirtualWatching, Selectable: true, Subscribed: true},
		{Mailbox: "space:10001", Name: "IT Service Desk", Path: "IT Service Desk", Selectable: true, Subscribed: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return stored[2], stored[0], stored[1]
}

// seedItem stores one copy of an issue item in f, with a raw file.
func seedItem(t *testing.T, s *Store, f Folder, issueID, remoteID, subject string, date time.Time, flags ...api.Flag) *Message {
	t.Helper()
	m := &Message{
		AccountID: f.AccountID, FolderID: f.ID, RemoteID: remoteID, Flags: flags,
		From:    []api.Address{{Name: "Jana Dvořáková", Address: "u-1@users.jira.invalid"}},
		Subject: subject, Date: date, InternalDate: date, Size: 100,
		RFCMessageID: "<" + strings.ReplaceAll(remoteID, ":", ".") + ".issue." + issueID + "@acme.malachi.invalid>",
		ThreadID:     IssueThreadID(issueID),
	}
	if err := s.UpsertMessages(context.Background(), []*Message{m}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteMessageRaw(context.Background(), f.AccountID, m.ID, strings.NewReader("Subject: "+subject+"\r\n\r\nbody"), 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RecountFolder(context.Background(), f.ID); err != nil {
		t.Fatal(err)
	}
	return m
}

// hide marks rows hidden the way the notification-mail matcher will, with
// a link to the issue, and recounts their folder.
func hide(t *testing.T, s *Store, issueAccountID, issueKey, issueID string, msgs ...*Message) {
	t.Helper()
	ctx := context.Background()
	for _, m := range msgs {
		if _, err := s.db.ExecContext(ctx, `UPDATE messages SET hidden = 1 WHERE id = ?`, m.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO issue_mail_links (message_id, issue_account_id, issue_key, issue_id)
			VALUES (?, ?, ?, ?)`, m.ID, issueAccountID, issueKey, issueID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.RecountFolder(ctx, m.FolderID); err != nil {
			t.Fatal(err)
		}
	}
}

func folderCounts(t *testing.T, s *Store, account, id string) (unread, total int) {
	t.Helper()
	f, err := s.GetFolder(context.Background(), account, id)
	if err != nil {
		t.Fatal(err)
	}
	return f.Unread, f.Total
}

func TestRealmOf(t *testing.T) {
	for _, c := range []struct {
		cfg  api.AccountConfig
		want string
	}{
		{testAccountConfig("me@example.invalid"), ""},
		{api.AccountConfig{Kind: api.AccountGraph, Graph: &api.GraphConfig{Source: api.GraphSourceGOA}}, ""},
		{api.AccountConfig{Kind: api.AccountJira}, ""},
		{jiraConfig("me@example.invalid", ""), ""},
		{jiraConfig("me@example.invalid", "not a url"), ""},
		{jiraConfig("me@example.invalid", "https://Acme.Atlassian.net"), "acme.atlassian.net"},
		{jiraConfig("me@example.invalid", "https://acme.atlassian.net/"), "acme.atlassian.net"},
		{jiraConfig("me@example.invalid", "https://acme.atlassian.net:443"), "acme.atlassian.net"},
		{jiraConfig("me@example.invalid", "https://jira.example.org:8443/Jira/"), "jira.example.org:8443/jira"},
		{jiraConfig("me@example.invalid", "http://jira.example.org:80/jira"), "jira.example.org/jira"},
		{jiraConfig("me@example.invalid", "http://jira.example.org:443/jira"), "jira.example.org:443/jira"},
		{jiraConfig("me@example.invalid", "http://127.0.0.1:8080"), "127.0.0.1:8080"},
		{jiraConfig("me@example.invalid", "http://[::1]:80/x"), "[::1]/x"},
		// A mail kind never has a realm, whatever it carries.
		{api.AccountConfig{Kind: api.AccountIMAP, Jira: &api.JiraConfig{SiteURL: "https://acme.atlassian.net"}}, ""},
	} {
		site := ""
		if c.cfg.Jira != nil {
			site = c.cfg.Jira.SiteURL
		}
		if got := RealmOf(c.cfg); got != c.want {
			t.Errorf("RealmOf(%s %q) = %q, want %q", c.cfg.Protocol(), site, got, c.want)
		}
	}
}

// An address is unique within its realm: a Jira account may share the
// mailbox's address, two accounts of one site may not.
func TestAccountsUniqueWithinRealm(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	mail := Account{Name: "Mail", Enabled: true, Config: testAccountConfig("jana@example.invalid")}
	if err := s.AddAccount(ctx, &mail); err != nil {
		t.Fatal(err)
	}
	cloud := Account{Name: "Acme", Enabled: true, Config: jiraConfig("Jana@Example.invalid", "https://acme.atlassian.net")}
	if err := s.AddAccount(ctx, &cloud); err != nil {
		t.Fatalf("jira account with the mailbox's address: %v", err)
	}
	if cloud.Realm != "acme.atlassian.net" || cloud.Email != "jana@example.invalid" {
		t.Fatalf("added: %+v", cloud)
	}
	twin := Account{Name: "Twin", Enabled: true, Config: jiraConfig("jana@example.invalid", "https://ACME.atlassian.net/")}
	if err := s.AddAccount(ctx, &twin); !errors.Is(err, ErrExists) {
		t.Fatalf("second account of the site: %v", err)
	}
	other := Account{Name: "Other", Enabled: true, Config: jiraConfig("jana@example.invalid", "https://jira.example.org/jira")}
	if err := s.AddAccount(ctx, &other); err != nil {
		t.Fatalf("account of another site: %v", err)
	}
	again := Account{Name: "Mail 2", Enabled: true, Config: testAccountConfig("JANA@example.invalid")}
	if err := s.AddAccount(ctx, &again); !errors.Is(err, ErrExists) {
		t.Fatalf("second mailbox with the address: %v", err)
	}
	noSite := Account{Name: "Broken", Enabled: true, Config: jiraConfig("x@example.invalid", "")}
	if err := s.AddAccount(ctx, &noSite); err == nil || errors.Is(err, ErrExists) {
		t.Fatalf("jira account without a site: %v", err)
	}

	// Moving an account to a site taken by the same address conflicts;
	// to a free one it does not, and the realm follows the config.
	moved := other
	moved.Config = jiraConfig("jana@example.invalid", "https://acme.atlassian.net")
	if err := s.UpdateAccount(ctx, &moved); !errors.Is(err, ErrExists) {
		t.Fatalf("update onto a taken site: %v", err)
	}
	moved.Config = jiraConfig("jana@example.invalid", "https://jira.example.org:8443")
	moved.Realm = "ignored"
	if err := s.UpdateAccount(ctx, &moved); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAccount(ctx, other.ID)
	if err != nil || got.Realm != "jira.example.org:8443" {
		t.Fatalf("after update: %+v %v", got, err)
	}
	// A mailbox moving to the address of a Jira account is fine too.
	second := Account{Name: "Home", Enabled: true, Config: testAccountConfig("home@example.invalid")}
	if err := s.AddAccount(ctx, &second); err != nil {
		t.Fatal(err)
	}
	second.Email, second.Config = "", testAccountConfig("jana@example.invalid")
	if err := s.UpdateAccount(ctx, &second); !errors.Is(err, ErrExists) {
		t.Fatalf("mailbox onto the mailbox's address: %v", err)
	}
	list, err := s.ListAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	realms := map[string]string{}
	for _, a := range list {
		realms[a.ID] = a.Realm
	}
	if realms[mail.ID] != "" || realms[cloud.ID] != "acme.atlassian.net" || len(list) != 4 {
		t.Fatalf("list: %v", realms)
	}
}

func TestIssueSpaces(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	spaces := []IssueSpace{{SpaceID: "2", Key: "WEB", Name: "Web"}, {SpaceID: "1", Key: "ITSD", Name: "IT Service Desk", ServiceDesk: true}}
	if err := s.SetIssueSpaces(ctx, "acc", spaces); err != nil {
		t.Fatal(err)
	}
	got, err := s.IssueSpaces(ctx, "acc")
	if err != nil {
		t.Fatal(err)
	}
	want := []IssueSpace{{AccountID: "acc", SpaceID: "1", Key: "ITSD", Name: "IT Service Desk", ServiceDesk: true},
		{AccountID: "acc", SpaceID: "2", Key: "WEB", Name: "Web"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("spaces = %+v", got)
	}
	if err := s.SetIssueSpaces(ctx, "acc", []IssueSpace{{SpaceID: "3", Key: "MOB"}, {SpaceID: "3", Key: "MOB"}}); err == nil {
		t.Fatal("duplicate space accepted")
	}
	if err := s.SetIssueSpaces(ctx, "acc", []IssueSpace{{SpaceID: "", Key: "X"}}); err == nil {
		t.Fatal("empty space id accepted")
	}
	if got, _ := s.IssueSpaces(ctx, "acc"); len(got) != 2 {
		t.Fatalf("a refused set changed the spaces: %+v", got)
	}
	if err := s.SetIssueSpaces(ctx, "acc", []IssueSpace{{SpaceID: "3", Key: "MOB"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.IssueSpaces(ctx, "acc"); len(got) != 1 || got[0].Key != "MOB" {
		t.Fatalf("replaced: %+v", got)
	}
	if got, _ := s.IssueSpaces(ctx, "other"); len(got) != 0 {
		t.Fatalf("another account's spaces: %+v", got)
	}
}

func TestIssuesRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	t0 := time.Date(2026, 9, 1, 8, 30, 0, 123e6, time.UTC)
	is := Issue{
		AccountID: "acc", IssueID: "10042", Key: "ITSD-42", SpaceID: "10001", Summary: "Printer on the 2nd floor",
		StatusID: "3", Status: "In Progress", StatusCategory: api.StatusCategoryInProgress, Type: "Bug", Priority: "High",
		AssigneeID: "u-1", AssigneeName: "Jana Dvořáková", ReporterID: "u-2", ReporterName: "Petr Novák",
		Watching: true, ServiceDesk: true, ViaMail: true,
		Created: t0, Updated: t0.Add(time.Hour), SyncedUpdated: t0, RenderKey: "r1",
		Views: []api.VirtualFolder{api.VirtualAssignedToMe, api.VirtualOpen},
	}
	if err := s.PutIssue(ctx, is); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetIssue(ctx, "acc", "10042")
	if err != nil || !reflect.DeepEqual(got, is) {
		t.Fatalf("get:\n got %+v %v\nwant %+v", got, err, is)
	}
	if got, err := s.IssueByKey(ctx, "acc", "ITSD-42"); err != nil || got.IssueID != "10042" {
		t.Fatalf("by key: %+v %v", got, err)
	}
	for _, miss := range []func() error{
		func() error { _, err := s.GetIssue(ctx, "other", "10042"); return err },
		func() error { _, err := s.IssueByKey(ctx, "acc", "itsd-42"); return err },
		func() error { _, err := s.GetIssue(ctx, "acc", "1"); return err },
	} {
		if err := miss(); !errors.Is(err, ErrNotFound) {
			t.Errorf("missing issue: %v", err)
		}
	}
	if err := s.PutIssue(ctx, Issue{AccountID: "acc", IssueID: "1"}); err == nil {
		t.Error("issue without a key accepted")
	}

	// A refresh replaces the row; the zero values clear what they stand for.
	is.Summary, is.Views, is.AssigneeID, is.AssigneeName, is.SyncedUpdated = "Printer fixed", nil, "", "", is.Updated
	if err := s.PutIssue(ctx, is); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetIssue(ctx, "acc", "10042")
	if got.Summary != "Printer fixed" || len(got.Views) != 0 || got.AssigneeName != "" || !got.SyncedUpdated.Equal(got.Updated) {
		t.Fatalf("refreshed: %+v", got)
	}

	// The key moved to another issue: the stale holder keeps its row under
	// a placeholder no lookup by key finds.
	moved := Issue{AccountID: "acc", IssueID: "20001", Key: "ITSD-42", SpaceID: "10001", Updated: t0.Add(2 * time.Hour)}
	if err := s.PutIssue(ctx, moved); err != nil {
		t.Fatal(err)
	}
	if got, err := s.IssueByKey(ctx, "acc", "ITSD-42"); err != nil || got.IssueID != "20001" {
		t.Fatalf("key after the move: %+v %v", got, err)
	}
	if got, err := s.GetIssue(ctx, "acc", "10042"); err != nil || got.Key == "ITSD-42" || !strings.HasPrefix(got.Key, "~") {
		t.Fatalf("stale holder: %+v %v", got, err)
	}
	old := is
	old.Key = "WEB-7"
	if err := s.PutIssue(ctx, old); err != nil {
		t.Fatal(err)
	}

	byID, err := s.IssuesByID(ctx, "acc", []string{"10042", "20001", "404", "10042"})
	if err != nil || len(byID) != 2 || byID["10042"].Key != "WEB-7" || byID["20001"].Key != "ITSD-42" {
		t.Fatalf("by id: %+v %v", byID, err)
	}
	byThread, err := s.IssuesByThread(ctx, "acc", []string{IssueThreadID("20001"), "t_0123", "jira:", "conv-x", IssueThreadID("404")})
	if err != nil || len(byThread) != 1 || byThread[IssueThreadID("20001")].Key != "ITSD-42" {
		t.Fatalf("by thread: %+v %v", byThread, err)
	}
	if id, ok := IssueIDOfThread("jira:"); ok || id != "" {
		t.Errorf("empty issue thread id parsed: %q", id)
	}

	stamps, err := s.ListIssueStamps(ctx, "acc")
	if err != nil || len(stamps) != 2 {
		t.Fatalf("stamps: %+v %v", stamps, err)
	}
	if st := stamps["10042"]; !st.Updated.Equal(t0.Add(time.Hour)) || !st.SyncedUpdated.Equal(st.Updated) || st.RenderKey != "r1" {
		t.Errorf("stamp: %+v", st)
	}
	if st := stamps["20001"]; !st.SyncedUpdated.IsZero() || st.Views != nil && len(st.Views) != 0 {
		t.Errorf("new issue stamp: %+v", st)
	}

	// Retention candidates: updated before the cutoff, never an unknown time.
	if err := s.PutIssue(ctx, Issue{AccountID: "acc", IssueID: "30000", Key: "MOB-1"}); err != nil {
		t.Fatal(err)
	}
	old2, err := s.IssuesUpdatedBefore(ctx, "acc", t0.Add(90*time.Minute))
	if err != nil || !slices.Equal(old2, []string{"10042"}) {
		t.Fatalf("updated before: %v %v", old2, err)
	}
	if all, _ := s.IssuesUpdatedBefore(ctx, "acc", t0.Add(24*time.Hour)); !slices.Equal(all, []string{"10042", "20001"}) {
		t.Fatalf("updated before, later cutoff: %v", all)
	}
}

func TestIssueItemsAndDecorations(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	t0 := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	if err := s.PutIssue(ctx, Issue{AccountID: "acc", IssueID: "10042", Key: "ITSD-42", Summary: "Printer", Status: "Open"}); err != nil {
		t.Fatal(err)
	}
	items := []IssueItem{
		{RemoteID: "i:10042", IssueID: "10042", Kind: api.IssueItemDescription, AuthorID: "u-2", Updated: t0},
		{RemoteID: "c:500", IssueID: "10042", Kind: api.IssueItemComment, Visibility: api.CommentInternal, AuthorID: "u-1",
			Via: "Issue Sync", Edited: true, Updated: t0.Add(time.Minute)},
		{RemoteID: "h:900", IssueID: "10042", Kind: api.IssueItemEvent,
			Changes: []api.IssueChange{{Field: api.IssueFieldStatus, From: "To Do", To: "Open"}}},
	}
	if err := s.PutIssueItems(ctx, "acc", items); err != nil {
		t.Fatal(err)
	}
	if err := s.PutIssueItems(ctx, "acc", []IssueItem{{RemoteID: "", IssueID: "10042"}}); err == nil {
		t.Fatal("item without a remote id accepted")
	}
	got, err := s.IssueItems(ctx, "acc", "10042")
	if err != nil || len(got) != 3 {
		t.Fatalf("items: %+v %v", got, err)
	}
	if got[0].RemoteID != "c:500" || got[0].AccountID != "acc" || got[0].Visibility != api.CommentInternal || !got[0].Edited ||
		got[0].Via != "Issue Sync" || !got[0].Updated.Equal(t0.Add(time.Minute)) {
		t.Errorf("comment: %+v", got[0])
	}
	if got[1].RemoteID != "h:900" || len(got[1].Changes) != 1 || got[1].Changes[0].To != "Open" || !got[1].Updated.IsZero() {
		t.Errorf("event: %+v", got[1])
	}
	// Re-put replaces an item.
	items[1].Edited, items[1].Via = false, ""
	if err := s.PutIssueItems(ctx, "acc", items[1:2]); err != nil {
		t.Fatal(err)
	}

	deco, err := s.IssueDecorations(ctx, "acc", []string{"c:500", "h:900", "c:404", "", "c:500"})
	if err != nil || len(deco) != 2 {
		t.Fatalf("decorations: %+v %v", deco, err)
	}
	if d := deco["c:500"]; d.Item.Kind != api.IssueItemComment || d.Item.Edited || d.Issue.Key != "ITSD-42" || d.Issue.Summary != "Printer" {
		t.Errorf("comment decoration: %+v", d)
	}
	if d := deco["h:900"]; d.Item.Kind != api.IssueItemEvent || d.Issue.Status != "Open" {
		t.Errorf("event decoration: %+v", d)
	}
	// An item whose issue row is gone has no decoration; another account's
	// ids are not this account's.
	if err := s.PutIssueItems(ctx, "acc", []IssueItem{{RemoteID: "c:7", IssueID: "gone", Kind: api.IssueItemComment}}); err != nil {
		t.Fatal(err)
	}
	if deco, _ := s.IssueDecorations(ctx, "acc", []string{"c:7"}); len(deco) != 0 {
		t.Errorf("orphan item decorated: %+v", deco)
	}
	if deco, _ := s.IssueDecorations(ctx, "other", []string{"c:500"}); len(deco) != 0 {
		t.Errorf("another account's item decorated: %+v", deco)
	}
}

// The copies of an item share flags and envelope; rows of an issue are
// found through its thread; deleting items and issues takes every copy.
func TestIssueRowsCopies(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	space, assigned, watching := jiraFolders(t, s, "acc")
	t0 := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	desc := seedItem(t, s, space, "10042", "i:10042", "ITSD-42: Printer", t0)
	descView := seedItem(t, s, assigned, "10042", "i:10042", "ITSD-42: Printer", t0)
	comment := seedItem(t, s, space, "10042", "c:500", "ITSD-42: Printer", t0.Add(time.Hour))
	commentView := seedItem(t, s, assigned, "10042", "c:500", "ITSD-42: Printer", t0.Add(time.Hour))
	other := seedItem(t, s, space, "10043", "i:10043", "ITSD-43: Monitor", t0)

	if u, n := folderCounts(t, s, "acc", space.ID); u != 3 || n != 3 {
		t.Fatalf("space counts %d/%d", u, n)
	}

	rows, err := s.IssueRows(ctx, "acc", "10042")
	if err != nil || len(rows) != 4 {
		t.Fatalf("issue rows: %+v %v", rows, err)
	}
	for _, r := range rows {
		if r.BodyState != BodyNone || r.RawState != RawFull || len(r.Flags) != 0 {
			t.Errorf("row %+v", r)
		}
	}

	// Flags go to every copy, without a pending operation.
	folders, err := s.SetFlagsByRemoteID(ctx, "acc", "c:500", []api.Flag{api.FlagSeen, api.FlagFlagged})
	want := []string{space.ID, assigned.ID}
	slices.Sort(want)
	if err != nil || !slices.Equal(folders, want) {
		t.Fatalf("set flags: %v %v, want %v", folders, err, want)
	}
	for _, id := range []string{comment.ID, commentView.ID} {
		m, err := s.GetMessage(ctx, "acc", id)
		if err != nil || !sameFlags(m.Flags, []api.Flag{api.FlagFlagged, api.FlagSeen}) {
			t.Fatalf("copy %s flags %v %v", id, m.Flags, err)
		}
	}
	if u, n := folderCounts(t, s, "acc", assigned.ID); u != 1 || n != 2 {
		t.Fatalf("view counts %d/%d", u, n)
	}
	if n, err := s.CountPendingOps(ctx, "acc"); err != nil || n != 0 {
		t.Fatalf("pending operations %d %v", n, err)
	}
	if folders, err := s.SetFlagsByRemoteID(ctx, "acc", "c:500", []api.Flag{api.FlagFlagged, api.FlagSeen}); err != nil || len(folders) != 0 {
		t.Fatalf("unchanged flags: %v %v", folders, err)
	}
	for _, rid := range []string{"c:404", ""} {
		if _, err := s.SetFlagsByRemoteID(ctx, "acc", rid, nil); !errors.Is(err, ErrNotFound) {
			t.Errorf("flags of %q: %v", rid, err)
		}
	}
	if _, err := s.SetFlagsByRemoteID(ctx, "other", "c:500", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("another account's item: %v", err)
	}

	// A renamed issue and a re-attributed author: every copy, and the
	// full-text index, follow.
	from := []api.Address{{Name: "Petr Novák", Address: "n-abc@users.jira.invalid"}}
	if err := s.UpdateEnvelopeByRemoteID(ctx, "acc", "c:500", "ITSD-42: Laser printer", from, t0.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{comment.ID, commentView.ID} {
		m, _ := s.GetMessage(ctx, "acc", id)
		if m.Subject != "ITSD-42: Laser printer" || len(m.From) != 1 || m.From[0].Name != "Petr Novák" || !m.Date.Equal(t0.Add(30*time.Minute)) {
			t.Fatalf("envelope of %s: %+v", id, m)
		}
	}
	if got := searchSubjects(t, s, SearchFilter{AccountID: "acc", Match: match("laser")}); !slices.Equal(got, []string{"ITSD-42: Laser printer"}) {
		t.Fatalf("search after the rename: %v", got)
	}
	if err := s.UpdateEnvelopeByRemoteID(ctx, "acc", "i:10042", "ITSD-42: Laser printer", nil, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.GetMessage(ctx, "acc", descView.ID); !m.Date.Equal(t0) || m.Subject != "ITSD-42: Laser printer" || len(m.From) != 0 {
		t.Fatalf("zero date kept: %+v", m)
	}
	if err := s.UpdateEnvelopeByRemoteID(ctx, "acc", "c:404", "x", nil, t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown remote id: %v", err)
	}

	// The issue moved to another key: every row of its thread, and
	// nothing else, is retitled.
	if err := s.RetitleThread(ctx, "acc", IssueThreadID("10042"), "WEB-7: Laser printer"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.ThreadMessages(ctx, "acc", IssueThreadID("10042"), assigned.ID, 0); len(rows) != 2 ||
		rows[0].Subject != "WEB-7: Laser printer" || rows[1].Subject != "WEB-7: Laser printer" {
		t.Fatalf("retitled: %+v", rows)
	}
	if m, _ := s.GetMessage(ctx, "acc", other.ID); m.Subject != "ITSD-43: Monitor" {
		t.Fatalf("another issue retitled: %q", m.Subject)
	}
	if got := searchSubjects(t, s, SearchFilter{AccountID: "acc", Match: match("subject:web")}); len(got) != 2 {
		t.Fatalf("search after the move: %v", got)
	}
	for _, tid := range []string{IssueThreadID("404"), ""} {
		if err := s.RetitleThread(ctx, "acc", tid, "x"); !errors.Is(err, ErrNotFound) {
			t.Errorf("retitle %q: %v", tid, err)
		}
	}

	// A deleted comment takes every copy and its file.
	if err := s.PutIssueItems(ctx, "acc", []IssueItem{{RemoteID: "c:500", IssueID: "10042", Kind: api.IssueItemComment}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteIssueItems(ctx, "acc", []string{"c:500", "c:404", ""}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{comment.ID, commentView.ID} {
		if _, err := s.GetMessage(ctx, "acc", id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("copy %s kept: %v", id, err)
		}
		if fileExists(t, s.MessageRawPath("acc", id)) {
			t.Fatalf("file of %s kept", id)
		}
	}
	if items, _ := s.IssueItems(ctx, "acc", "10042"); len(items) != 0 {
		t.Fatalf("item kept: %+v", items)
	}
	if u, n := folderCounts(t, s, "acc", assigned.ID); u != 1 || n != 1 {
		t.Fatalf("view counts after the delete %d/%d", u, n)
	}
	_ = watching

	// Deleting an issue takes its rows in every folder, and nothing else.
	if err := s.PutIssue(ctx, Issue{AccountID: "acc", IssueID: "10042", Key: "ITSD-42"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutIssueItems(ctx, "acc", []IssueItem{{RemoteID: "i:10042", IssueID: "10042", Kind: api.IssueItemDescription}}); err != nil {
		t.Fatal(err)
	}
	touched, err := s.DeleteIssues(ctx, "acc", []string{"10042", "404", ""})
	if err != nil || len(touched) != 0 {
		t.Fatalf("delete issues: %v %v", touched, err)
	}
	for _, id := range []string{desc.ID, descView.ID} {
		if _, err := s.GetMessage(ctx, "acc", id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("row %s kept: %v", id, err)
		}
	}
	if _, err := s.GetIssue(ctx, "acc", "10042"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("issue kept: %v", err)
	}
	if items, _ := s.IssueItems(ctx, "acc", "10042"); len(items) != 0 {
		t.Fatalf("items kept: %+v", items)
	}
	if _, err := s.GetMessage(ctx, "acc", other.ID); err != nil {
		t.Fatalf("another issue's row: %v", err)
	}
	if u, n := folderCounts(t, s, "acc", space.ID); u != 1 || n != 1 {
		t.Fatalf("space counts after the delete %d/%d", u, n)
	}
	if u, n := folderCounts(t, s, "acc", assigned.ID); u != 0 || n != 0 {
		t.Fatalf("view counts after the delete %d/%d", u, n)
	}
}

// A hidden message is found by id and nowhere else: not in a listing, a
// thread, a search or a count.
func TestHiddenMessagesLeaveListings(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	acc := seedAccounts(t, s, 1)[0]
	inbox := seedFolder(t, s, acc, "INBOX", api.RoleInbox)
	t0 := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	root := seedMessage(t, s, inbox, 1, "Printer ITSD-42", t0)
	note := &Message{AccountID: acc, FolderID: inbox.ID, UID: 2, Subject: "[JIRA] ITSD-42 Printer", Date: t0.Add(time.Hour),
		RFCMessageID: "<n@example.invalid>", InReplyTo: root.RFCMessageID, Size: 10,
		From: []api.Address{{Address: "jira@acme.example.invalid"}}}
	plain := seedMessage(t, s, inbox, 3, "Lunch", t0.Add(2*time.Hour), api.FlagSeen)
	if err := s.UpsertMessages(ctx, []*Message{note}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RecountFolder(ctx, inbox.ID); err != nil {
		t.Fatal(err)
	}
	if note.ThreadID != root.ThreadID {
		t.Fatalf("the reply is not in the root's thread")
	}
	if u, n := folderCounts(t, s, acc, inbox.ID); u != 2 || n != 3 {
		t.Fatalf("counts before %d/%d", u, n)
	}
	hide(t, s, "acc_jira", "ITSD-42", "10042", note)

	if u, n := folderCounts(t, s, acc, inbox.ID); u != 1 || n != 2 {
		t.Fatalf("counts after hiding %d/%d", u, n)
	}
	for _, filter := range []api.MessageFilter{api.FilterAll, api.FilterUnread} {
		items, _, total, err := s.ListMessages(ctx, acc, inbox.ID, "", 50, "", filter)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range items {
			if m.ID == note.ID {
				t.Fatalf("%s listing shows the hidden message", filter)
			}
		}
		if want := map[api.MessageFilter]int{api.FilterAll: 2, api.FilterUnread: 1}[filter]; total != want || len(items) != want {
			t.Fatalf("%s: total %d, %d items", filter, total, len(items))
		}
	}
	threads, _, total, err := s.ListThreads(ctx, acc, inbox.ID, "", 50, "", "")
	if err != nil || total != 2 || len(threads) != 2 {
		t.Fatalf("threads: %d %d %v", total, len(threads), err)
	}
	for _, th := range threads {
		if th.ID == root.ThreadID && (th.MessageCount != 1 || th.Latest.ID != root.ID) {
			t.Fatalf("thread counts the hidden member: %+v", th)
		}
	}
	if _, _, unreadTotal, _ := s.ListThreads(ctx, acc, inbox.ID, "", 50, "", api.FilterUnread); unreadTotal != 1 {
		t.Fatalf("unread threads %d", unreadTotal)
	}
	for _, folder := range []string{inbox.ID, ""} {
		row, err := s.GetThread(ctx, acc, root.ThreadID, folder)
		if err != nil || row.MessageCount != 1 || row.UnreadCount != 1 || row.Latest.ID != root.ID || len(row.Participants) != 1 {
			t.Fatalf("thread in %q: %+v %v", folder, row, err)
		}
		members, err := s.ThreadMessages(ctx, acc, root.ThreadID, folder, 0)
		if err != nil || len(members) != 1 || members[0].ID != root.ID {
			t.Fatalf("members in %q: %v", folder, err)
		}
	}
	// A thread of nothing but hidden messages is no thread.
	hide(t, s, "acc_jira", "ITSD-42", "10042", plain)
	if _, err := s.GetThread(ctx, acc, plain.ThreadID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hidden-only thread: %v", err)
	}
	if _, err := s.ThreadMessages(ctx, acc, plain.ThreadID, inbox.ID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hidden-only thread members: %v", err)
	}
	if got := searchSubjects(t, s, SearchFilter{AccountID: acc, Match: match("printer")}); !slices.Equal(got, []string{"Printer ITSD-42"}) {
		t.Fatalf("search: %v", got)
	}
	if got := searchSubjects(t, s, SearchFilter{AccountID: acc, FolderIDs: []string{inbox.ID}}); !slices.Equal(got, []string{"Printer ITSD-42"}) {
		t.Fatalf("search of the folder: %v", got)
	}
	if got := searchSubjects(t, s, SearchFilter{}); !slices.Equal(got, []string{"Printer ITSD-42"}) {
		t.Fatalf("search of every account: %v", got)
	}
	m, err := s.GetMessage(ctx, acc, note.ID)
	if err != nil || !m.Hidden {
		t.Fatalf("hidden message by id: %+v %v", m.Hidden, err)
	}
	if m, _ := s.GetMessage(ctx, acc, root.ID); m.Hidden {
		t.Fatal("visible message marked hidden")
	}
	// A new batch never unhides a row: the flags are all it updates.
	note.Flags = []api.Flag{api.FlagSeen}
	if err := s.UpsertMessages(ctx, []*Message{note}); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.GetMessage(ctx, acc, note.ID); !m.Hidden {
		t.Fatal("upsert showed the hidden message")
	}
}

// The views' copies are left out of every account-wide scope: each item is
// found once, in its space folder, unless a view is named.
func TestVirtualCopiesLeftOutAccountWide(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	acc := Account{Name: "Acme", Enabled: true, Config: jiraConfig("jana@example.invalid", "https://acme.atlassian.net")}
	if err := s.AddAccount(ctx, &acc); err != nil {
		t.Fatal(err)
	}
	space, assigned, watching := jiraFolders(t, s, acc.ID)
	for _, f := range []Folder{assigned, watching} {
		if f.Virtual == "" || f.Role != api.RoleNone {
			t.Fatalf("view stored as %+v", f)
		}
	}
	if space.Virtual != "" {
		t.Fatalf("space folder is virtual: %q", space.Virtual)
	}
	if folders, _ := s.ListFolders(ctx, acc.ID); folders[0].Virtual != api.VirtualAssignedToMe || folders[2].Virtual != "" {
		t.Fatalf("listed folders: %+v", folders)
	}
	t0 := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	seedItem(t, s, space, "10042", "i:10042", "ITSD-42: Printer", t0)
	seedItem(t, s, assigned, "10042", "i:10042", "ITSD-42: Printer", t0)
	seedItem(t, s, watching, "10042", "i:10042", "ITSD-42: Printer", t0)
	c := seedItem(t, s, space, "10042", "c:500", "ITSD-42: Printer", t0.Add(time.Hour))
	seedItem(t, s, assigned, "10042", "c:500", "ITSD-42: Printer", t0.Add(time.Hour))
	tid := IssueThreadID("10042")

	if got := searchSubjects(t, s, SearchFilter{AccountID: acc.ID, Match: match("printer")}); len(got) != 2 {
		t.Fatalf("account-wide search: %v", got)
	}
	if got := searchSubjects(t, s, SearchFilter{Match: match("printer")}); len(got) != 2 {
		t.Fatalf("search of every account: %v", got)
	}
	if got := searchSubjects(t, s, SearchFilter{AccountID: acc.ID, FolderIDs: []string{assigned.ID}, Match: match("printer")}); len(got) != 2 {
		t.Fatalf("search of the view: %v", got)
	}
	if got := searchSubjects(t, s, SearchFilter{AccountID: acc.ID, FolderIDs: []string{watching.ID}}); len(got) != 1 {
		t.Fatalf("search of the other view: %v", got)
	}

	members, err := s.ThreadMessages(ctx, acc.ID, tid, "", 0)
	if err != nil || len(members) != 2 {
		t.Fatalf("account-wide members: %d %v", len(members), err)
	}
	for _, m := range members {
		if m.FolderID != space.ID {
			t.Fatalf("a view's copy among the members: %+v", m)
		}
	}
	row, err := s.GetThread(ctx, acc.ID, tid, "")
	if err != nil || row.MessageCount != 2 || row.UnreadCount != 2 || row.Latest.ID != c.ID {
		t.Fatalf("account-wide thread: %+v %v", row, err)
	}
	want := []string{space.ID, assigned.ID, watching.ID}
	slices.Sort(want)
	if !slices.Equal(row.FolderIDs, want) {
		t.Fatalf("folders %v, want %v", row.FolderIDs, want)
	}
	if row, err := s.GetThread(ctx, acc.ID, tid, watching.ID); err != nil || row.MessageCount != 1 {
		t.Fatalf("thread in a view: %+v %v", row, err)
	}
	if members, err := s.ThreadMessages(ctx, acc.ID, tid, assigned.ID, 0); err != nil || len(members) != 2 || members[0].FolderID != assigned.ID {
		t.Fatalf("members in a view: %v", err)
	}
	if threads, _, total, err := s.ListThreads(ctx, acc.ID, assigned.ID, "", 50, "", ""); err != nil || total != 1 || threads[0].MessageCount != 2 {
		t.Fatalf("threads of a view: %d %v", total, err)
	}
	// Renaming a view keeps its code; dropping it from the batch deletes
	// it with its copies, and the space folder keeps its rows.
	stored, removed, err := s.UpsertFolders(ctx, acc.ID, []Folder{
		{Mailbox: "view:assignedToMe", Name: "Mine", Path: "Mine", Virtual: api.VirtualAssignedToMe, Selectable: true, Subscribed: true},
		{Mailbox: "space:10001", Name: "IT Service Desk", Path: "IT Service Desk", Selectable: true, Subscribed: true},
	})
	if err != nil || len(removed) != 1 || removed[0] != watching.ID || stored[0].Virtual != api.VirtualAssignedToMe || stored[0].ID != assigned.ID {
		t.Fatalf("folders after dropping a view: %+v %v %v", stored, removed, err)
	}
	if rows, _ := s.IssueRows(ctx, acc.ID, "10042"); len(rows) != 4 {
		t.Fatalf("rows after dropping a view: %d", len(rows))
	}
}

// Removing an issue-tracker account takes its issue tables and shows the
// mail it hid in another account again; removing an issue does the same
// for that issue's mail.
func TestIssueAccountRemovalShowsHiddenMail(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	mail := seedAccounts(t, s, 1)[0]
	inbox := seedFolder(t, s, mail, "INBOX", api.RoleInbox)
	archive := seedFolder(t, s, mail, "Archive", api.RoleArchive)
	jira := Account{Name: "Acme", Enabled: true, Config: jiraConfig("a@example.invalid", "https://acme.atlassian.net")}
	if err := s.AddAccount(ctx, &jira); err != nil {
		t.Fatal(err)
	}
	space, assigned, _ := jiraFolders(t, s, jira.ID)
	t0 := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	seedItem(t, s, space, "10042", "i:10042", "ITSD-42: Printer", t0)
	seedItem(t, s, assigned, "10042", "i:10042", "ITSD-42: Printer", t0)
	if err := s.PutIssue(ctx, Issue{AccountID: jira.ID, IssueID: "10042", Key: "ITSD-42"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutIssue(ctx, Issue{AccountID: jira.ID, IssueID: "10043", Key: "ITSD-43"}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutIssueItems(ctx, jira.ID, []IssueItem{{RemoteID: "i:10042", IssueID: "10042", Kind: api.IssueItemDescription}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetIssueSpaces(ctx, jira.ID, []IssueSpace{{SpaceID: "10001", Key: "ITSD"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMeta(ctx, MetaIssueMePrefix+jira.ID, `{"id":"u-1"}`); err != nil {
		t.Fatal(err)
	}
	n42 := seedMessage(t, s, inbox, 1, "[JIRA] ITSD-42", t0)
	n42b := seedMessage(t, s, archive, 2, "[JIRA] ITSD-42 again", t0)
	n43 := seedMessage(t, s, inbox, 3, "[JIRA] ITSD-43", t0)
	keyOnly := seedMessage(t, s, inbox, 4, "[JIRA] ITSD-43 before sync", t0)
	seedMessage(t, s, inbox, 5, "Lunch", t0)
	hide(t, s, jira.ID, "ITSD-42", "10042", n42, n42b)
	hide(t, s, jira.ID, "ITSD-43", "10043", n43)
	hide(t, s, jira.ID, "ITSD-43", "", keyOnly)
	if u, n := folderCounts(t, s, mail, inbox.ID); u != 1 || n != 1 {
		t.Fatalf("inbox counts while hidden %d/%d", u, n)
	}

	// The issue leaves: its mail is shown again, the other issue's stays
	// hidden.
	touched, err := s.DeleteIssues(ctx, jira.ID, []string{"10042"})
	if err != nil {
		t.Fatal(err)
	}
	wantFolders := []string{inbox.ID, archive.ID}
	slices.Sort(wantFolders)
	if !reflect.DeepEqual(touched, map[string][]string{mail: wantFolders}) {
		t.Fatalf("touched %v, want %s: %v", touched, mail, wantFolders)
	}
	if m, _ := s.GetMessage(ctx, mail, n42.ID); m.Hidden {
		t.Fatal("the deleted issue's mail stays hidden")
	}
	if m, _ := s.GetMessage(ctx, mail, n43.ID); !m.Hidden {
		t.Fatal("another issue's mail shown")
	}
	if u, n := folderCounts(t, s, mail, inbox.ID); u != 2 || n != 2 {
		t.Fatalf("inbox counts %d/%d", u, n)
	}
	if u, n := folderCounts(t, s, mail, archive.ID); u != 1 || n != 1 {
		t.Fatalf("archive counts %d/%d", u, n)
	}
	// A link by key alone is shown again with its issue.
	touched, err = s.DeleteIssues(ctx, jira.ID, []string{"10043"})
	if err != nil || !reflect.DeepEqual(touched, map[string][]string{mail: {inbox.ID}}) {
		t.Fatalf("second delete: %v %v", touched, err)
	}
	for _, m := range []*Message{n43, keyOnly} {
		if got, _ := s.GetMessage(ctx, mail, m.ID); got.Hidden {
			t.Fatalf("%s stays hidden", m.Subject)
		}
	}

	// The account leaves with its issue tables; mail it still hid is
	// shown again.
	hide(t, s, jira.ID, "ITSD-44", "", n42)
	if err := s.DeleteAccount(ctx, jira.ID, true); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.GetMessage(ctx, mail, n42.ID); m.Hidden {
		t.Fatal("mail hidden by a removed account")
	}
	if u, n := folderCounts(t, s, mail, inbox.ID); u != 4 || n != 4 {
		t.Fatalf("inbox counts after the removal %d/%d", u, n)
	}
	for table, q := range map[string]string{
		"issue_mail_links": `SELECT COUNT(*) FROM issue_mail_links WHERE issue_account_id = ?`,
		"issues":           `SELECT COUNT(*) FROM issues WHERE account_id = ?`,
		"issue_items":      `SELECT COUNT(*) FROM issue_items WHERE account_id = ?`,
		"issue_spaces":     `SELECT COUNT(*) FROM issue_spaces WHERE account_id = ?`,
		"messages":         `SELECT COUNT(*) FROM messages WHERE account_id = ?`,
	} {
		var n int
		if err := s.db.QueryRowContext(ctx, q, jira.ID).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: %d rows left, %v", table, n, err)
		}
	}
	if _, ok, _ := s.GetMeta(ctx, MetaIssueMePrefix+jira.ID); ok {
		t.Error("the account's identity stays in meta")
	}
	// Removing the mail account takes its links with its messages.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO issue_mail_links (message_id, issue_account_id, issue_key) VALUES (?, 'acc_x', 'X-1')`, n42.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, mail, true); err != nil {
		t.Fatal(err)
	}
	var links int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM issue_mail_links`).Scan(&links); err != nil || links != 0 {
		t.Fatalf("links of deleted mail: %d %v", links, err)
	}
}

// Comment drafts and queued comments carry what the issue tracker needs.
func TestDraftAndOutboxComment(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	d := Draft{AccountID: "acc", Subject: "ITSD-42: Printer", TextBody: "done", CommentVisibility: api.CommentInternal}
	if err := s.SaveDraft(ctx, &d, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDraft(ctx, "acc", d.ID)
	if err != nil || got.CommentVisibility != api.CommentInternal {
		t.Fatalf("saved: %q %v", got.CommentVisibility, err)
	}
	d.CommentVisibility = api.CommentPublic
	if err := s.SaveDraft(ctx, &d, nil); err != nil {
		t.Fatal(err)
	}
	if list, _, _, err := s.ListDrafts(ctx, "acc", "", 10); err != nil || list[0].CommentVisibility != api.CommentPublic {
		t.Fatalf("listed: %+v %v", list, err)
	}
	if mailDraft := seedDraft(t, s, "acc"); mailDraft.CommentVisibility != "" {
		t.Fatalf("mail draft: %q", mailDraft.CommentVisibility)
	}

	in := enqueueInput(d, "Subject: c\r\n\r\ndone")
	in.Recipients = nil
	in.Comment = &OutboxComment{IssueID: "10042", Visibility: api.CommentInternal}
	m, err := s.EnqueueOutbox(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.GetOutbox(ctx, "acc", m.ID)
	if err != nil || e.Comment == nil || *e.Comment != (OutboxComment{IssueID: "10042", Visibility: api.CommentInternal}) || len(e.Recipients) != 0 {
		t.Fatalf("queued comment: %+v %v", e, err)
	}
	next, ok, err := s.NextOutbox(ctx, "acc", time.Now())
	if err != nil || !ok || next.Comment == nil || next.Comment.IssueID != "10042" {
		t.Fatalf("next: %+v %v %v", next, ok, err)
	}
	_, mailEntry := seedOutbox(t, s, "acc")
	if mailEntry.Comment != nil {
		t.Fatalf("mail entry carries a comment: %+v", mailEntry.Comment)
	}
	bad := enqueueInput(seedDraft(t, s, "acc"), "x")
	bad.Comment = &OutboxComment{}
	if _, err := s.EnqueueOutbox(ctx, bad); err == nil {
		t.Fatal("comment without an issue queued")
	}
}

// A comment queued in the outbox joins its issue's thread (its In-Reply-To
// names the item it answers) but is no item: the syncer's view of the
// issue's rows leaves it out, and forgetting the issue keeps it, so its
// delivery can still report what became of the issue.
func TestQueuedCommentInIssueThread(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	space, assigned, _ := jiraFolders(t, s, "acc")
	t0 := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	desc := seedItem(t, s, space, "10042", "i:10042", "ITSD-42: Printer", t0)
	seedItem(t, s, assigned, "10042", "i:10042", "ITSD-42: Printer", t0)

	d := Draft{AccountID: "acc", Subject: "ITSD-42: Printer", TextBody: "done", InReplyTo: desc.ID}
	if err := s.SaveDraft(ctx, &d, nil); err != nil {
		t.Fatal(err)
	}
	in := enqueueInput(d, "Subject: c\r\n\r\ndone")
	in.Message.To, in.Message.CC, in.Message.BCC, in.Recipients = nil, nil, nil, nil
	in.Message.InReplyTo, in.Message.References = desc.RFCMessageID, []string{desc.RFCMessageID}
	in.Comment = &OutboxComment{IssueID: "10042"}
	queued, err := s.EnqueueOutbox(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if m, err := s.GetMessage(ctx, "acc", queued.ID); err != nil || m.ThreadID != IssueThreadID("10042") {
		t.Fatalf("queued comment thread %q %v", m.ThreadID, err)
	}
	rows, err := s.IssueRows(ctx, "acc", "10042")
	if err != nil || len(rows) != 2 {
		t.Fatalf("issue rows: %+v %v", rows, err)
	}
	for _, r := range rows {
		if r.ID == queued.ID {
			t.Fatal("the queued comment is listed as an item's row")
		}
	}
	if _, err := s.DeleteIssues(ctx, "acc", []string{"10042"}); err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.IssueRows(ctx, "acc", "10042"); len(rows) != 0 {
		t.Fatalf("rows after forgetting: %+v", rows)
	}
	if _, err := s.GetMessage(ctx, "acc", queued.ID); err != nil {
		t.Fatalf("the queued comment went with the issue: %v", err)
	}
	if e, err := s.GetOutbox(ctx, "acc", queued.ID); err != nil || e.Comment == nil {
		t.Fatalf("outbox entry: %+v %v", e, err)
	}
	if f, err := s.OutboxFolder(ctx, "acc"); err != nil || f.Total != 1 {
		t.Fatalf("outbox folder: %+v %v", f, err)
	}
}
