// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// unifiedBox is an imap account, a graph account, a disabled imap account
// and accounts of the other kinds, each with an inbox-role folder (the
// non-mail ones only to prove they stay out), plus an unsynced inbox.
type unifiedBox struct {
	b               *Backend
	imap, graph     string
	imapIn, graphIn string
	imapOther       string
	imapMsgs        []string
	graphMsgs       []string
}

func seedUnified(t *testing.T) *unifiedBox {
	t.Helper()
	b, _ := newSyncBackend(t)
	ctx := context.Background()
	x := &unifiedBox{b: b}
	x.imap = seedAccount(t, b, "me@example.invalid")
	off := seedAccount(t, b, "off@example.invalid")
	if _, err := b.Accounts().SetEnabled(ctx, api.AccountSetEnabledParams{AccountID: api.AccountID(off), Enabled: false}); err != nil {
		t.Fatal(err)
	}
	add := func(a store.Account) string {
		t.Helper()
		a.Enabled = true
		if err := b.store.AddAccount(ctx, &a); err != nil {
			t.Fatal(err)
		}
		return a.ID
	}
	x.graph = add(store.Account{Name: "M365", Config: api.AccountConfig{
		Name: "M365", Email: "me@contoso.invalid", Kind: api.AccountGraph, Graph: &api.GraphConfig{},
	}})
	jira := add(store.Account{Name: "Acme", Config: api.AccountConfig{
		Name: "Acme", Email: "me@example.invalid", Kind: api.AccountJira,
		Jira: &api.JiraConfig{SiteURL: "https://acme.atlassian.net", Deployment: api.JiraCloud, Login: "me@example.invalid"},
	}})
	caldav := add(store.Account{Name: "Cal", Config: api.AccountConfig{
		Name: "Cal", Email: "me@example.invalid", Kind: api.AccountCalDAV,
		CalDAV: &api.CalDAVConfig{ServerURL: "https://dav.example.invalid/dav"},
	}})
	eway := add(store.Account{Name: "CRM", Config: api.AccountConfig{
		Name: "CRM", Email: "me@example.invalid", Kind: api.AccountEway,
		Eway: &api.EwayConfig{ServiceURL: "https://hosting.eway-crm.com/acme", Username: "me"},
	}})

	inbox := func(acc string, extra ...store.Folder) map[string]store.Folder {
		fs := append([]store.Folder{{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true}}, extra...)
		return seedFolders(t, b, acc, fs)
	}
	im := inbox(x.imap, store.Folder{Mailbox: "Other", Name: "Other", Path: "Other", Subscribed: true, Selectable: true})
	x.imapIn, x.imapOther = im["INBOX"].ID, im["Other"].ID
	x.graphIn = inbox(x.graph)["INBOX"].ID
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	put := func(acc, folder string, uid uint32, id, reply string, at time.Duration, flags ...api.Flag) string {
		t.Helper()
		m := &store.Message{AccountID: acc, FolderID: folder, UID: uid, Subject: id, Date: base.Add(at),
			From: []api.Address{{Address: "x@example.invalid"}}, RFCMessageID: "<" + id + "@example.invalid>", Flags: flags}
		if reply != "" {
			m.InReplyTo = "<" + reply + "@example.invalid>"
		}
		if err := b.store.UpsertMessages(ctx, []*store.Message{m}); err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	x.imapMsgs = []string{
		put(x.imap, x.imapIn, 1, "i0", "", 0, api.FlagSeen),
		put(x.imap, x.imapIn, 2, "i1", "i0", 2*time.Minute),
		put(x.imap, x.imapIn, 3, "i2", "", 4*time.Minute),
	}
	x.graphMsgs = []string{
		put(x.graph, x.graphIn, 1, "g0", "", time.Minute),
		put(x.graph, x.graphIn, 2, "g1", "", 3*time.Minute, api.FlagSeen, api.FlagFlagged),
	}
	put(x.imap, x.imapOther, 9, "other", "", time.Hour)
	// Mail of accounts that never join: disabled, jira, caldav, eway.
	for i, acc := range []string{off, jira, caldav, eway} {
		in := inbox(acc)["INBOX"].ID
		put(acc, in, 1, "out"+acc, "", time.Duration(10+i)*time.Hour)
	}
	// An unsynced inbox-role folder is out as well.
	allMail := seedFolders(t, b, x.graph, []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
		{Mailbox: "Arch", Name: "Arch", Path: "Arch", Role: api.RoleInbox, Subscribed: true, Selectable: true, Unsynced: true},
	})
	put(x.graph, allMail["Arch"].ID, 1, "unsynced", "", 20*time.Hour)
	return x
}

func TestUnifiedListMessages(t *testing.T) {
	x := seedUnified(t)
	ctx := context.Background()
	svc := x.b.Messages()
	var got []api.MessageSummary
	cursor := ""
	for {
		res, err := svc.List(ctx, api.MessageListParams{Unified: api.RoleInbox, Page: api.Page{Limit: 2, Cursor: cursor}})
		if err != nil {
			t.Fatal(err)
		}
		if res.Page.Total != 5 {
			t.Fatalf("total = %d", res.Page.Total)
		}
		got = append(got, res.Messages...)
		if res.Page.NextCursor == "" {
			break
		}
		cursor = res.Page.NextCursor
	}
	want := []string{x.imapMsgs[2], x.graphMsgs[1], x.imapMsgs[1], x.graphMsgs[0], x.imapMsgs[0]}
	if len(got) != len(want) {
		t.Fatalf("got %d rows: %+v", len(got), got)
	}
	for i, m := range got {
		if string(m.ID) != want[i] {
			t.Errorf("row %d = %s (%s), want %s", i, m.ID, m.Subject, want[i])
		}
		wantAcc, wantFolder := x.imap, x.imapIn
		if m.AccountID == api.AccountID(x.graph) {
			wantAcc, wantFolder = x.graph, x.graphIn
		}
		if string(m.AccountID) != wantAcc || string(m.FolderID) != wantFolder {
			t.Errorf("row %d in %s/%s", i, m.AccountID, m.FolderID)
		}
	}
	res, err := svc.List(ctx, api.MessageListParams{Unified: api.RoleInbox, Filter: api.FilterUnread})
	if err != nil || res.Page.Total != 3 {
		t.Errorf("unread: %+v %v", res, err)
	}
	res, err = svc.List(ctx, api.MessageListParams{Unified: api.RoleInbox, Filter: api.FilterFlagged, Sort: api.SortDateAsc})
	if err != nil || len(res.Messages) != 1 || string(res.Messages[0].ID) != x.graphMsgs[1] {
		t.Errorf("flagged: %+v %v", res, err)
	}

	// A single-folder cursor is no unified cursor and the other way round.
	one, err := svc.List(ctx, api.MessageListParams{AccountID: api.AccountID(x.imap), FolderID: api.FolderID(x.imapIn), Page: api.Page{Limit: 1}})
	if err != nil || one.Page.NextCursor == "" {
		t.Fatal(err)
	}
	if _, err := svc.List(ctx, api.MessageListParams{Unified: api.RoleInbox, Page: api.Page{Cursor: one.Page.NextCursor}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("folder cursor in unified: %v", err)
	}
	uni, _ := svc.List(ctx, api.MessageListParams{Unified: api.RoleInbox, Page: api.Page{Limit: 1}})
	if _, err := svc.List(ctx, api.MessageListParams{AccountID: api.AccountID(x.imap), FolderID: api.FolderID(x.imapIn), Page: api.Page{Cursor: uni.Page.NextCursor}}); errCode(t, err) != api.CodeInvalidArgument {
		t.Errorf("unified cursor in a folder: %v", err)
	}
}

func TestUnifiedListRefusals(t *testing.T) {
	x := seedUnified(t)
	ctx := context.Background()
	acc, folder := api.AccountID(x.imap), api.FolderID(x.imapIn)
	for name, p := range map[string]api.MessageListParams{
		"with account": {Unified: api.RoleInbox, AccountID: acc},
		"with folder":  {Unified: api.RoleInbox, FolderID: folder},
		"with both":    {Unified: api.RoleInbox, AccountID: acc, FolderID: folder},
		"sent":         {Unified: api.RoleSent},
		"unknown role": {Unified: "bogus"},
		"bad sort":     {Unified: api.RoleInbox, Sort: "x"},
		"bad filter":   {Unified: api.RoleInbox, Filter: "x"},
	} {
		if _, err := x.b.Messages().List(ctx, p); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("message.list %s: %v", name, err)
		}
		tp := api.ThreadListParams{AccountID: p.AccountID, FolderID: p.FolderID, Unified: p.Unified, Sort: p.Sort, Filter: p.Filter}
		if _, err := x.b.Threads().List(ctx, tp); errCode(t, err) != api.CodeInvalidArgument {
			t.Errorf("thread.list %s: %v", name, err)
		}
	}
}

func TestUnifiedListThreads(t *testing.T) {
	x := seedUnified(t)
	ctx := context.Background()
	res, err := x.b.Threads().List(ctx, api.ThreadListParams{Unified: api.RoleInbox})
	if err != nil {
		t.Fatal(err)
	}
	if res.Page.Total != 4 || len(res.Threads) != 4 {
		t.Fatalf("threads: %+v", res)
	}
	// i2 (4m), g1 (3m), i0+i1 (2m), g0 (1m).
	wantLatest := []string{x.imapMsgs[2], x.graphMsgs[1], x.imapMsgs[1], x.graphMsgs[0]}
	for i, th := range res.Threads {
		if string(th.Latest.ID) != wantLatest[i] {
			t.Errorf("row %d latest = %s", i, th.Latest.ID)
		}
		if th.Latest.AccountID != th.AccountID || len(th.FolderIDs) != 1 || th.FolderIDs[0] != th.Latest.FolderID {
			t.Errorf("row %d: %+v", i, th)
		}
	}
	if res.Threads[2].MessageCount != 2 || res.Threads[2].UnreadCount != 1 {
		t.Errorf("thread of two: %+v", res.Threads[2])
	}
	// Each row is what thread.list of its folder has.
	own, err := x.b.Threads().List(ctx, api.ThreadListParams{AccountID: api.AccountID(x.imap), FolderID: api.FolderID(x.imapIn)})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range own.Threads {
		found := false
		for _, r := range res.Threads {
			if r.AccountID == o.AccountID && r.ID == o.ID {
				found = r.MessageCount == o.MessageCount && r.SentCount == o.SentCount && r.DraftCount == o.DraftCount
			}
		}
		if !found {
			t.Errorf("thread %s differs from its folder's listing", o.ID)
		}
	}
	// Paging one row at a time walks the same rows.
	var walked []api.ThreadSummary
	cursor := ""
	for {
		page, err := x.b.Threads().List(ctx, api.ThreadListParams{Unified: api.RoleInbox, Page: api.Page{Limit: 1, Cursor: cursor}})
		if err != nil {
			t.Fatal(err)
		}
		walked = append(walked, page.Threads...)
		if page.Page.NextCursor == "" {
			break
		}
		cursor = page.Page.NextCursor
	}
	if len(walked) != 4 {
		t.Fatalf("walked %d", len(walked))
	}
	for i := range walked {
		if walked[i].ID != res.Threads[i].ID || walked[i].AccountID != res.Threads[i].AccountID {
			t.Errorf("walk row %d differs", i)
		}
	}
	unread, err := x.b.Threads().List(ctx, api.ThreadListParams{Unified: api.RoleInbox, Filter: api.FilterUnread})
	if err != nil || unread.Page.Total != 3 {
		t.Errorf("unread threads: %+v %v", unread, err)
	}
}

// The dispatch wrapper passes a unified listing (no account) through.
func TestUnifiedListPassesKindDispatch(t *testing.T) {
	x := seedUnified(t)
	var _ api.MessageService = x.b.Messages()
	if _, ok := x.b.Messages().(mailOnlyMessages); !ok {
		t.Fatal("message service is not the dispatch wrapper")
	}
	res, err := x.b.Messages().List(context.Background(), api.MessageListParams{Unified: api.RoleInbox})
	if err != nil || len(res.Messages) != 5 {
		t.Fatalf("through the wrapper: %+v %v", res, err)
	}
}

// With no mail account the unified listing is an empty page.
func TestUnifiedListEmpty(t *testing.T) {
	b, _ := newSyncBackend(t)
	res, err := b.Messages().List(context.Background(), api.MessageListParams{Unified: api.RoleInbox})
	if err != nil || res.Messages == nil || len(res.Messages) != 0 || res.Page.Total != 0 {
		t.Fatalf("messages: %+v %v", res, err)
	}
	th, err := b.Threads().List(context.Background(), api.ThreadListParams{Unified: api.RoleInbox})
	if err != nil || th.Threads == nil || len(th.Threads) != 0 {
		t.Fatalf("threads: %+v %v", th, err)
	}
}
