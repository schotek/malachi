// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

// issueMailBox is a mail account with an inbox and an archive next to an
// issue-tracker account with two stored issues.
type issueMailBox struct {
	s              *Store
	mail, jira     string
	inbox, archive Folder
	t0             time.Time
}

func seedIssueMailBox(t *testing.T) *issueMailBox {
	t.Helper()
	ctx := context.Background()
	s := openTestStore(t)
	mail := seedAccounts(t, s, 1)[0]
	b := &issueMailBox{s: s, mail: mail, t0: time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)}
	b.inbox = seedFolder(t, s, mail, "INBOX", api.RoleInbox)
	b.archive = seedFolder(t, s, mail, "Archive", api.RoleArchive)
	jira := Account{Name: "Acme", Enabled: true, Config: jiraConfig("jana@example.invalid", "https://acme.atlassian.net")}
	if err := s.AddAccount(ctx, &jira); err != nil {
		t.Fatal(err)
	}
	b.jira = jira.ID
	for id, key := range map[string]string{"10042": "ITSD-42", "10043": "ITSD-43"} {
		if err := s.PutIssue(ctx, Issue{AccountID: jira.ID, IssueID: id, Key: key, SpaceID: "10001"}); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

// note stores an unread notification mail of the site.
func (b *issueMailBox) note(t *testing.T, f Folder, uid uint32, key string) *Message {
	t.Helper()
	m := &Message{
		AccountID: f.AccountID, FolderID: f.ID, UID: uid,
		From:    []api.Address{{Name: "Petr Svoboda (Jira)", Address: "jira@acme.atlassian.net"}},
		Subject: "[JIRA] (" + key + ") Printer", Date: b.t0.Add(time.Duration(uid) * time.Minute),
		InternalDate: b.t0.Add(time.Duration(uid) * time.Minute),
		RFCMessageID: fmt.Sprintf("<note-%d@acme.atlassian.net>", uid), Size: 100,
	}
	if err := b.s.UpsertMessages(context.Background(), []*Message{m}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.s.RecountFolder(context.Background(), f.ID); err != nil {
		t.Fatal(err)
	}
	return m
}

func (b *issueMailBox) hidden(t *testing.T, m *Message) bool {
	t.Helper()
	got, err := b.s.GetMessage(context.Background(), m.AccountID, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	byID, err := b.s.MessageHidden(context.Background(), m.ID)
	if err != nil || byID != got.Hidden {
		t.Fatalf("MessageHidden %v, row %v, %v", byID, got.Hidden, err)
	}
	return got.Hidden
}

func (b *issueMailBox) link(t *testing.T, m *Message, key, issueID string) {
	t.Helper()
	if err := b.s.LinkIssueMail(context.Background(), m.ID, b.jira, key, issueID); err != nil {
		t.Fatal(err)
	}
}

func (b *issueMailBox) links(t *testing.T) []IssueMailLink {
	t.Helper()
	got, err := b.s.IssueMailLinks(context.Background(), b.jira, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestLinkIssueMail(t *testing.T) {
	ctx := context.Background()
	b := seedIssueMailBox(t)
	s := b.s
	n42 := b.note(t, b.inbox, 1, "ITSD-42")
	n99 := b.note(t, b.inbox, 2, "ITSD-99")

	for _, c := range []struct{ message, account, key string }{
		{"", b.jira, "ITSD-42"}, {n42.ID, "", "ITSD-42"}, {n42.ID, b.jira, ""},
	} {
		if err := s.LinkIssueMail(ctx, c.message, c.account, c.key, ""); err == nil {
			t.Errorf("link %+v accepted", c)
		}
	}
	if err := s.LinkIssueMail(ctx, "m_none", b.jira, "ITSD-42", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("link of an unknown message: %v", err)
	}
	if linked, err := s.IssueMailLinked(ctx, b.jira, "ITSD-42"); err != nil || linked {
		t.Fatalf("linked before any link: %v %v", linked, err)
	}

	// The stored issue's id is found by its key, and the issue marked.
	b.link(t, n42, "ITSD-42", "")
	b.link(t, n99, "ITSD-99", "")
	got := b.links(t)
	if len(got) != 2 {
		t.Fatalf("links: %+v", got)
	}
	byMessage := map[string]IssueMailLink{}
	for _, l := range got {
		byMessage[l.MessageID] = l
	}
	l := byMessage[n42.ID]
	if l.IssueID != "10042" || l.IssueKey != "ITSD-42" || l.IssueAccountID != b.jira || l.MailAccountID != b.mail ||
		l.FolderID != b.inbox.ID || l.Subject != n42.Subject || l.Hidden || !l.InternalDate.Equal(n42.InternalDate) ||
		len(l.From) != 1 || l.From[0].Address != "jira@acme.atlassian.net" {
		t.Fatalf("link of the stored issue: %+v", l)
	}
	if l := byMessage[n99.ID]; l.IssueID != "" || l.IssueKey != "ITSD-99" {
		t.Fatalf("link of an issue not stored: %+v", l)
	}
	for key, want := range map[string]bool{"ITSD-42": true, "ITSD-99": true, "ITSD-43": false, "itsd-42": false} {
		if linked, err := s.IssueMailLinked(ctx, b.jira, key); err != nil || linked != want {
			t.Errorf("linked %s = %v, %v", key, linked, err)
		}
	}
	if linked, _ := s.IssueMailLinked(ctx, "acc_other", "ITSD-42"); linked {
		t.Error("another account's issue linked")
	}
	if is, _ := s.IssueByKey(ctx, b.jira, "ITSD-42"); !is.ViaMail {
		t.Error("the named issue is not marked")
	}
	if is, _ := s.IssueByKey(ctx, b.jira, "ITSD-43"); is.ViaMail {
		t.Error("another issue is marked")
	}

	// Linking again changes nothing; paging goes by message id.
	b.link(t, n42, "ITSD-42", "")
	if got := b.links(t); len(got) != 2 {
		t.Fatalf("links after linking again: %+v", got)
	}
	first, err := s.IssueMailLinks(ctx, b.jira, "", 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("first page: %+v %v", first, err)
	}
	rest, err := s.IssueMailLinks(ctx, b.jira, first[0].MessageID, 1)
	if err != nil || len(rest) != 1 || rest[0].MessageID == first[0].MessageID {
		t.Fatalf("second page: %+v %v", rest, err)
	}
	if end, _ := s.IssueMailLinks(ctx, b.jira, rest[0].MessageID, 1); len(end) != 0 {
		t.Fatalf("past the end: %+v", end)
	}

	// A link that moves to another issue shows the message again.
	if _, changed, err := s.SetMessageHidden(ctx, n42.ID, true); err != nil || !changed {
		t.Fatal(changed, err)
	}
	b.link(t, n42, "ITSD-43", "10043")
	if b.hidden(t, n42) {
		t.Fatal("hidden because of an issue it no longer names")
	}
	if u, n := folderCounts(t, s, b.mail, b.inbox.ID); u != 2 || n != 2 {
		t.Fatalf("counts %d/%d", u, n)
	}
	// One of the same issue keeps it hidden.
	if _, _, err := s.SetMessageHidden(ctx, n42.ID, true); err != nil {
		t.Fatal(err)
	}
	b.link(t, n42, "ITSD-43", "")
	if !b.hidden(t, n42) {
		t.Fatal("linking again showed the message")
	}

	// The link goes with its message.
	if err := s.DeleteMessagesByUID(ctx, b.inbox.ID, []uint32{1}); err != nil {
		t.Fatal(err)
	}
	if got := b.links(t); len(got) != 1 || got[0].MessageID != n99.ID {
		t.Fatalf("links after the message went: %+v", got)
	}
}

func TestSetMessageHidden(t *testing.T) {
	ctx := context.Background()
	b := seedIssueMailBox(t)
	s := b.s
	n := b.note(t, b.inbox, 1, "ITSD-42")
	read := b.note(t, b.inbox, 2, "ITSD-42")
	if _, err := s.db.ExecContext(ctx, `UPDATE messages SET flags = '["seen"]', unread = 0 WHERE id = ?`, read.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RecountFolder(ctx, b.inbox.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetMessage(ctx, b.mail, n.ID)

	if _, _, err := s.SetMessageHidden(ctx, "m_none", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown message: %v", err)
	}
	if hidden, err := s.MessageHidden(ctx, "m_none"); err != nil || hidden {
		t.Fatalf("unknown message hidden: %v %v", hidden, err)
	}
	if folder, changed, err := s.SetMessageHidden(ctx, n.ID, false); err != nil || changed || folder != b.inbox.ID {
		t.Fatalf("showing a shown message: %q %v %v", folder, changed, err)
	}
	folder, changed, err := s.SetMessageHidden(ctx, n.ID, true)
	if err != nil || !changed || folder != b.inbox.ID {
		t.Fatalf("hide: %q %v %v", folder, changed, err)
	}
	if u, total := folderCounts(t, s, b.mail, b.inbox.ID); u != 0 || total != 1 {
		t.Fatalf("counts while hidden %d/%d", u, total)
	}
	if _, changed, _ := s.SetMessageHidden(ctx, n.ID, true); changed {
		t.Fatal("hiding twice changed something")
	}
	// Nothing but the filter changes: flags, folder and the times a
	// syncer compares stay.
	after, _ := s.GetMessage(ctx, b.mail, n.ID)
	if !after.Hidden || !sameFlags(after.Flags, before.Flags) || after.FolderID != before.FolderID || after.UID != before.UID ||
		after.ModSeq != before.ModSeq || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("hiding changed the row: %+v", after)
	}
	var ops int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM message_ops`).Scan(&ops); err != nil || ops != 0 {
		t.Fatalf("hiding queued %d operations for the server, %v", ops, err)
	}
	if _, changed, err := s.SetMessageHidden(ctx, read.ID, true); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if u, total := folderCounts(t, s, b.mail, b.inbox.ID); u != 0 || total != 0 {
		t.Fatalf("counts with both hidden %d/%d", u, total)
	}
	if _, changed, err := s.SetMessageHidden(ctx, n.ID, false); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if u, total := folderCounts(t, s, b.mail, b.inbox.ID); u != 1 || total != 1 {
		t.Fatalf("counts after showing one %d/%d", u, total)
	}
}

func TestSetIssueMailHidden(t *testing.T) {
	ctx := context.Background()
	b := seedIssueMailBox(t)
	s := b.s
	n42 := b.note(t, b.inbox, 1, "ITSD-42")
	n42b := b.note(t, b.archive, 2, "ITSD-42")
	n43 := b.note(t, b.inbox, 3, "ITSD-43")
	n99 := b.note(t, b.inbox, 4, "ITSD-99") // its issue is not stored
	plain := b.note(t, b.inbox, 5, "ITSD-42")
	b.link(t, n42, "ITSD-42", "")
	b.link(t, n42b, "ITSD-42", "")
	b.link(t, n43, "ITSD-43", "")
	b.link(t, n99, "ITSD-99", "")
	// Another account's link to the same key is not this account's.
	other := b.note(t, b.inbox, 6, "ITSD-42")
	if err := s.LinkIssueMail(ctx, other.ID, "acc_other", "ITSD-42", ""); err != nil {
		t.Fatal(err)
	}
	both := []string{b.inbox.ID, b.archive.ID}
	slices.Sort(both)

	if _, err := s.SetIssueMailHidden(ctx, "", true, nil); err == nil {
		t.Fatal("no account accepted")
	}
	// An empty list of keys is nothing, nil is every key.
	touched, err := s.SetIssueMailHidden(ctx, b.jira, true, []string{})
	if err != nil || len(touched) != 0 || b.hidden(t, n42) {
		t.Fatalf("no keys: %v %v", touched, err)
	}
	touched, err = s.SetIssueMailHidden(ctx, b.jira, true, []string{"ITSD-42", "ITSD-42", "ITSD-99", ""})
	if err != nil || !reflect.DeepEqual(touched, map[string][]string{b.mail: both}) {
		t.Fatalf("hide one key: %v %v", touched, err)
	}
	for m, want := range map[*Message]bool{n42: true, n42b: true, n43: false, n99: false, plain: false, other: false} {
		if got := b.hidden(t, m); got != want {
			t.Errorf("%s (uid %d) hidden = %v", m.Subject, m.UID, got)
		}
	}
	if u, n := folderCounts(t, s, b.mail, b.inbox.ID); u != 4 || n != 4 {
		t.Fatalf("inbox counts %d/%d", u, n)
	}
	if u, n := folderCounts(t, s, b.mail, b.archive.ID); u != 0 || n != 0 {
		t.Fatalf("archive counts %d/%d", u, n)
	}
	// Again: nothing changes, nothing is reported.
	if touched, err := s.SetIssueMailHidden(ctx, b.jira, true, []string{"ITSD-42"}); err != nil || len(touched) != 0 {
		t.Fatalf("hide again: %v %v", touched, err)
	}
	touched, err = s.SetIssueMailHidden(ctx, b.jira, true, nil)
	if err != nil || !reflect.DeepEqual(touched, map[string][]string{b.mail: {b.inbox.ID}}) || !b.hidden(t, n43) || b.hidden(t, n99) {
		t.Fatalf("hide all: %v %v", touched, err)
	}

	// An issue that arrives hides its mail, and the link learns its id;
	// one that goes shows it.
	if err := s.PutIssue(ctx, Issue{AccountID: b.jira, IssueID: "10099", Key: "ITSD-99", SpaceID: "10001"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM issues WHERE account_id = ? AND issue_id = '10043'`, b.jira); err != nil {
		t.Fatal(err)
	}
	touched, err = s.SetIssueMailHidden(ctx, b.jira, true, nil)
	if err != nil || !reflect.DeepEqual(touched, map[string][]string{b.mail: {b.inbox.ID}}) {
		t.Fatalf("after the issues changed: %v %v", touched, err)
	}
	if !b.hidden(t, n99) || b.hidden(t, n43) || !b.hidden(t, n42) {
		t.Fatal("hidden does not follow the stored issues")
	}
	for _, l := range b.links(t) {
		if l.MessageID == n99.ID && l.IssueID != "10099" {
			t.Fatalf("the link did not learn the issue: %+v", l)
		}
	}

	// Showing: one key, then everything.
	touched, err = s.SetIssueMailHidden(ctx, b.jira, false, []string{"ITSD-99"})
	if err != nil || !reflect.DeepEqual(touched, map[string][]string{b.mail: {b.inbox.ID}}) || b.hidden(t, n99) || !b.hidden(t, n42) {
		t.Fatalf("show one key: %v %v", touched, err)
	}
	touched, err = s.SetIssueMailHidden(ctx, b.jira, false, nil)
	if err != nil || !reflect.DeepEqual(touched, map[string][]string{b.mail: both}) {
		t.Fatalf("show all: %v %v", touched, err)
	}
	for _, m := range []*Message{n42, n42b, n43, n99} {
		if b.hidden(t, m) {
			t.Errorf("uid %d stays hidden", m.UID)
		}
	}
	if len(b.links(t)) != 4 {
		t.Fatal("showing took the links")
	}
	if u, n := folderCounts(t, s, b.mail, b.inbox.ID); u != 5 || n != 5 {
		t.Fatalf("inbox counts at the end %d/%d", u, n)
	}
}

func TestUnlinkIssueMail(t *testing.T) {
	ctx := context.Background()
	b := seedIssueMailBox(t)
	s := b.s
	n42 := b.note(t, b.inbox, 1, "ITSD-42")
	n43 := b.note(t, b.archive, 2, "ITSD-43")
	shown := b.note(t, b.inbox, 3, "ITSD-42")
	free := b.note(t, b.inbox, 4, "ITSD-42")
	for _, m := range []*Message{n42, n43, shown} {
		b.link(t, m, strings.TrimSuffix(strings.TrimPrefix(m.Subject, "[JIRA] ("), ") Printer"), "")
	}
	if _, err := s.SetIssueMailHidden(ctx, b.jira, true, []string{"ITSD-43"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetMessageHidden(ctx, n42.ID, true); err != nil {
		t.Fatal(err)
	}
	// A message hidden without a link is none of its business.
	if _, _, err := s.SetMessageHidden(ctx, free.ID, true); err != nil {
		t.Fatal(err)
	}

	if touched, err := s.UnlinkIssueMail(ctx, nil); err != nil || len(touched) != 0 {
		t.Fatalf("nothing to unlink: %v %v", touched, err)
	}
	touched, err := s.UnlinkIssueMail(ctx, []string{n42.ID, shown.ID, free.ID, "m_none", n42.ID})
	if err != nil || !reflect.DeepEqual(touched, map[string][]string{b.mail: {b.inbox.ID}}) {
		t.Fatalf("unlink: %v %v", touched, err)
	}
	if b.hidden(t, n42) || !b.hidden(t, n43) || !b.hidden(t, free) {
		t.Fatal("unlinking showed the wrong messages")
	}
	if got := b.links(t); len(got) != 1 || got[0].MessageID != n43.ID {
		t.Fatalf("links left: %+v", got)
	}
	if u, n := folderCounts(t, s, b.mail, b.inbox.ID); u != 2 || n != 2 {
		t.Fatalf("inbox counts %d/%d", u, n)
	}
}

func TestMailFromSenders(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	accounts := seedAccounts(t, s, 3)
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	inboxes := map[string]Folder{}
	for _, acc := range accounts {
		inboxes[acc] = seedFolder(t, s, acc, "INBOX", api.RoleInbox)
	}
	type row struct {
		account  string
		from     []api.Address
		age      time.Duration
		internal bool // the server's date of arrival is known
		subject  string
	}
	site := func(addr string) []api.Address { return []api.Address{{Name: "Jira", Address: addr}} }
	rows := []row{
		{accounts[0], site("jira@acme.atlassian.net"), time.Hour, true, "exact"},
		{accounts[0], site("JIRA@Acme.Atlassian.NET"), time.Hour, true, "case"},
		{accounts[1], site("automation@acme.atlassian.net"), 48 * time.Hour, true, "host, another account"},
		{accounts[0], site("jira@acme.atlassian.net"), 40 * 24 * time.Hour, true, "old"},
		{accounts[0], site("jira@acme.atlassian.net"), time.Hour, false, "header date only"},
		{accounts[0], site("jira@evil.acme.atlassian.net"), time.Hour, true, "subdomain"},
		{accounts[0], site("jira@acme.atlassian.net.example.org"), time.Hour, true, "suffix"},
		{accounts[0], site("jira@notacme.atlassian.net"), time.Hour, true, "longer host"},
		{accounts[0], []api.Address{{Name: "jira@acme.atlassian.net", Address: "mallory@example.org"}}, time.Hour, true, "name only"},
		{accounts[0], []api.Address{{Address: "mallory@example.org"}, {Address: "jira@acme.atlassian.net"}}, time.Hour, true, "second sender"},
		{accounts[0], nil, time.Hour, true, "no sender"},
		{accounts[0], site("jira@acme.atlassian.net%"), time.Hour, true, "wildcard"},
		{accounts[0], site("jira@acmé.atlassian.net"), time.Hour, true, "look-alike"},
		{accounts[2], site("jira@acme.atlassian.net"), time.Hour, true, "account not asked for"},
	}
	for i, r := range rows {
		m := &Message{AccountID: r.account, FolderID: inboxes[r.account].ID, UID: uint32(i + 1), From: r.from,
			Subject: r.subject, Date: now.Add(-r.age), Size: 10}
		if r.internal {
			m.InternalDate = now.Add(-r.age)
			m.Date = now.Add(-100 * 24 * time.Hour) // a header cannot make a message older or newer
		}
		if err := s.UpsertMessages(ctx, []*Message{m}); err != nil {
			t.Fatal(err)
		}
	}
	since := now.Add(-30 * 24 * time.Hour)
	subjects := func(senders []string, accs []string) []string {
		t.Helper()
		var out []string
		after := ""
		for {
			page, err := s.MailFromSenders(ctx, accs, senders, since, after, 2)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range page {
				if m.ID <= after {
					t.Fatalf("page out of order: %s after %s", m.ID, after)
				}
				out = append(out, m.Subject)
				after = m.ID
			}
			if len(page) < 2 {
				break
			}
		}
		slices.Sort(out)
		return out
	}
	two := accounts[:2]
	for _, c := range []struct {
		name    string
		senders []string
		want    []string
	}{
		{"host", []string{"@acme.atlassian.net"}, []string{"case", "exact", "header date only", "host, another account", "second sender"}},
		{"host in capitals", []string{"@ACME.atlassian.NET"}, []string{"case", "exact", "header date only", "host, another account", "second sender"}},
		{"address", []string{"jira@acme.atlassian.net"}, []string{"case", "exact", "header date only", "second sender"}},
		{"both", []string{"automation@acme.atlassian.net", "jira@evil.acme.atlassian.net"}, []string{"host, another account", "subdomain"}},
		{"nobody", []string{"@example.net"}, nil},
		{"no usable sender", []string{"", "acme.atlassian.net", "jira@", "@"}, nil},
		{"SQL wildcards are text", []string{"@%", "%@acme.atlassian.net", "@acme_atlassian.net"}, nil},
	} {
		if got := subjects(c.senders, two); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if got := subjects([]string{"@acme.atlassian.net"}, nil); got != nil {
		t.Errorf("no accounts: %v", got)
	}
	if got := subjects([]string{"@acme.atlassian.net"}, accounts[2:]); !slices.Equal(got, []string{"account not asked for"}) {
		t.Errorf("third account: %v", got)
	}
	// Hidden messages are listed: the caller evaluates them again.
	all, err := s.MailFromSenders(ctx, two, []string{"jira@acme.atlassian.net"}, since, "", 0)
	if err != nil || len(all) != 4 {
		t.Fatalf("all: %d %v", len(all), err)
	}
	if _, _, err := s.SetMessageHidden(ctx, all[0].ID, true); err != nil {
		t.Fatal(err)
	}
	again, err := s.MailFromSenders(ctx, two, []string{"jira@acme.atlassian.net"}, since, "", 0)
	if err != nil || len(again) != 4 || !again[0].Hidden {
		t.Fatalf("with a hidden one: %d %v", len(again), err)
	}
}

// A sender list in the store that is not what the encoder writes never
// fails the scan.
func TestMailFromSendersOddRows(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	acc := seedAccounts(t, s, 1)[0]
	inbox := seedFolder(t, s, acc, "INBOX", api.RoleInbox)
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	for i, from := range []string{`[]`, `["jira@acme.atlassian.net"]`, `[{"address":null}]`, `[{"address":42}]`, `[[{"address":"jira@acme.atlassian.net"}]]`,
		`[{"name":"x"}]`, `[{"address":"jira@acme.atlassian.net"}]`} {
		m := &Message{AccountID: acc, FolderID: inbox.ID, UID: uint32(i + 1), Subject: from, Date: now, InternalDate: now}
		if err := s.UpsertMessages(ctx, []*Message{m}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE messages SET from_json = ? WHERE id = ?`, from, m.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, senders := range [][]string{{"jira@acme.atlassian.net"}, {"@acme.atlassian.net"}} {
		got, err := s.MailFromSenders(ctx, []string{acc}, senders, now.Add(-time.Hour), "", 0)
		if err != nil {
			t.Fatalf("%v: the filter fails on an odd row: %v", senders, err)
		}
		if len(got) != 1 || got[0].Subject != `[{"address":"jira@acme.atlassian.net"}]` {
			t.Fatalf("%v matched %d rows", senders, len(got))
		}
	}
}

func TestASCIILower(t *testing.T) {
	kelvin := "jira@" + string(rune(0x212A)) + "iosk.example" // the Kelvin sign, which Unicode folds to k
	for in, want := range map[string]string{
		"": "", "abc": "abc", "JIRA@Acme.NET": "jira@acme.net", "ÁČ@X.cz": "ÁČ@x.cz", kelvin: kelvin,
	} {
		if got := asciiLower(in); got != want {
			t.Errorf("asciiLower(%q) = %q, want %q", in, got, want)
		}
	}
}
