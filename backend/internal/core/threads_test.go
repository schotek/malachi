// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/schotek/malachi/backend/internal/store"
	"github.com/schotek/malachi/backend/pkg/api"
)

// threadedMailbox is one account with a conversation spanning the inbox
// and Sent, plus singletons, with the thread ids set directly so the
// service is tested on its own (the linker has its tests in the store).
type threadedMailbox struct {
	b           *Backend
	acc         api.AccountID
	inbox, sent api.FolderID
	a1, a2, a3  api.MessageID // "Lunch": alice (inbox, unread) → me (sent, seen) → bob (inbox, unread, flagged, attachment)
	b1, c1, d1  api.MessageID // "Report" (inbox, seen); sent-only; "Tie" (inbox, same date as a3)
	unlinked    api.MessageID // an inbox row without thread id
	base        time.Time
}

func seedThreadedMailbox(t *testing.T) *threadedMailbox {
	t.Helper()
	b, f := newSyncBackend(t)
	ctx := context.Background()
	acc := seedAccount(t, b, "me@example.invalid")
	folders := seedFolders(t, b, acc, []store.Folder{
		{Mailbox: "INBOX", Name: "Inbox", Path: "Inbox", Role: api.RoleInbox, Subscribed: true, Selectable: true},
		{Mailbox: "Sent", Name: "Sent", Path: "Sent", Role: api.RoleSent, Subscribed: true, Selectable: true},
		{Mailbox: "Trash", Name: "Trash", Path: "Trash", Role: api.RoleTrash, Subscribed: true, Selectable: true},
	})
	inbox, sent := folders["INBOX"].ID, folders["Sent"].ID
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	alice := []api.Address{{Name: "Alice", Address: "alice@example.invalid"}}
	me := []api.Address{{Name: "Me", Address: "me@example.invalid"}}
	bob := []api.Address{{Name: "Bob", Address: "bob@example.invalid"}}
	rows := []*store.Message{
		{AccountID: acc, FolderID: inbox, UID: 1, ThreadID: "t_a", Subject: "Lunch", Date: base, From: alice, RFCMessageID: "a1", Size: 10, Snippet: "where"},
		{AccountID: acc, FolderID: sent, UID: 1, ThreadID: "t_a", Subject: "Re: Lunch", Date: base.Add(time.Hour), From: me, RFCMessageID: "a2", Flags: []api.Flag{api.FlagSeen}, Size: 10},
		{AccountID: acc, FolderID: inbox, UID: 2, ThreadID: "t_a", Subject: "Re: Lunch", Date: base.Add(2 * time.Hour), From: bob, RFCMessageID: "a3",
			Flags: []api.Flag{api.FlagFlagged}, HasAttachments: true, Size: 10, Snippet: "see you"},
		{AccountID: acc, FolderID: inbox, UID: 3, ThreadID: "t_b", Subject: "Report", Date: base.Add(-time.Hour), From: alice, RFCMessageID: "b1", Flags: []api.Flag{api.FlagSeen}, Size: 10},
		{AccountID: acc, FolderID: sent, UID: 2, ThreadID: "t_c", Subject: "Sent only", Date: base.Add(3 * time.Hour), From: me, RFCMessageID: "c1", Size: 10},
		{AccountID: acc, FolderID: inbox, UID: 4, ThreadID: "t_d", Subject: "Tie", Date: base.Add(2 * time.Hour), From: []api.Address{{Address: "ALICE@example.invalid"}}, RFCMessageID: "d1", Size: 10},
		{AccountID: acc, FolderID: inbox, UID: 5, Subject: "Unlinked", Date: base.Add(-2 * time.Hour), From: alice, RFCMessageID: "u1", Size: 10},
	}
	if err := b.store.UpsertMessages(ctx, rows); err != nil {
		t.Fatal(err)
	}
	// The unlinked row stands in for a store an older daemon wrote.
	if _, err := b.store.DB().ExecContext(ctx, `UPDATE messages SET thread_id = '' WHERE id = ?`, rows[6].ID); err != nil {
		t.Fatal(err)
	}
	f.reset()
	return &threadedMailbox{b: b, acc: api.AccountID(acc), inbox: api.FolderID(inbox), sent: api.FolderID(sent),
		a1: api.MessageID(rows[0].ID), a2: api.MessageID(rows[1].ID), a3: api.MessageID(rows[2].ID),
		b1: api.MessageID(rows[3].ID), c1: api.MessageID(rows[4].ID), d1: api.MessageID(rows[5].ID),
		unlinked: api.MessageID(rows[6].ID), base: base}
}

func TestThreadList(t *testing.T) {
	m := seedThreadedMailbox(t)
	ctx := context.Background()
	svc := m.b.Threads()

	res, err := svc.List(ctx, api.ThreadListParams{AccountID: m.acc, FolderID: m.inbox})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Threads) != 3 || res.Page.Total != 3 || res.Page.NextCursor != "" {
		t.Fatalf("list = %+v", res)
	}
	// Equal latest dates: the higher thread id first under dateDesc.
	if res.Threads[0].ID != "t_d" || res.Threads[1].ID != "t_a" || res.Threads[2].ID != "t_b" {
		t.Fatalf("order = %s %s %s", res.Threads[0].ID, res.Threads[1].ID, res.Threads[2].ID)
	}
	a := res.Threads[1]
	if a.AccountID != m.acc || a.Subject != "Lunch" || a.MessageCount != 2 || a.UnreadCount != 2 || !a.LatestDate.Equal(m.base.Add(2*time.Hour)) ||
		a.Latest.ID != m.a3 || a.Latest.Subject != "Re: Lunch" || a.Snippet != "see you" || !a.HasAttachments ||
		fmt.Sprint(a.Flags) != fmt.Sprint([]api.Flag{api.FlagFlagged}) {
		t.Fatalf("thread a = %+v", a)
	}
	if len(a.Participants) != 2 || a.Participants[0].Name != "Bob" || a.Participants[1].Address != "alice@example.invalid" {
		t.Errorf("participants = %+v", a.Participants)
	}
	if fmt.Sprint(a.FolderIDs) != fmt.Sprint(sortedFolders(m.inbox, m.sent)) {
		t.Errorf("folderIds = %v", a.FolderIDs)
	}
	if a.Latest.From == nil || a.Latest.Flags == nil || res.Threads[2].Participants == nil {
		t.Errorf("nil slices in %+v", a)
	}
	for _, th := range res.Threads {
		if th.ID == "t_c" {
			t.Error("a sent-only thread is listed in the inbox")
		}
	}

	asc, err := svc.List(ctx, api.ThreadListParams{AccountID: m.acc, FolderID: m.inbox, Sort: api.SortDateAsc, Page: api.Page{Limit: 2}})
	if err != nil || len(asc.Threads) != 2 || asc.Threads[0].ID != "t_b" || asc.Threads[1].ID != "t_a" || asc.Page.NextCursor == "" || asc.Page.Total != 3 {
		t.Fatalf("asc page 1 = %+v, %v", asc, err)
	}
	page2, err := svc.List(ctx, api.ThreadListParams{AccountID: m.acc, FolderID: m.inbox, Sort: api.SortDateAsc, Page: api.Page{Limit: 2, Cursor: asc.Page.NextCursor}})
	if err != nil || len(page2.Threads) != 1 || page2.Threads[0].ID != "t_d" || page2.Page.NextCursor != "" {
		t.Fatalf("asc page 2 = %+v, %v", page2, err)
	}
	one, err := svc.List(ctx, api.ThreadListParams{AccountID: m.acc, FolderID: m.inbox, Page: api.Page{Limit: 1}})
	if err != nil || len(one.Threads) != 1 || one.Threads[0].ID != "t_d" || one.Page.NextCursor == "" {
		t.Fatalf("limit 1 = %+v, %v", one, err)
	}

	unread, err := svc.List(ctx, api.ThreadListParams{AccountID: m.acc, FolderID: m.inbox, Filter: api.FilterUnread})
	if err != nil || len(unread.Threads) != 2 || unread.Page.Total != 2 {
		t.Fatalf("unread = %+v, %v", unread, err)
	}
	flagged, err := svc.List(ctx, api.ThreadListParams{AccountID: m.acc, FolderID: m.inbox, Filter: api.FilterFlagged})
	if err != nil || len(flagged.Threads) != 1 || flagged.Threads[0].ID != "t_a" || flagged.Page.Total != 1 {
		t.Fatalf("flagged = %+v, %v", flagged, err)
	}
	sentList, err := svc.List(ctx, api.ThreadListParams{AccountID: m.acc, FolderID: m.sent})
	if err != nil || len(sentList.Threads) != 2 || sentList.Threads[0].ID != "t_c" || sentList.Threads[1].MessageCount != 1 || sentList.Threads[1].Latest.ID != m.a2 {
		t.Fatalf("sent = %+v, %v", sentList, err)
	}

	msgCursor, err := m.b.Messages().List(ctx, api.MessageListParams{AccountID: m.acc, FolderID: m.inbox, Page: api.Page{Limit: 1}})
	if err != nil || msgCursor.Page.NextCursor == "" {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		p    api.ThreadListParams
		code api.ErrorCode
	}{
		{"no account", api.ThreadListParams{FolderID: m.inbox}, api.CodeInvalidArgument},
		{"no folder", api.ThreadListParams{AccountID: m.acc}, api.CodeInvalidArgument},
		{"unknown account", api.ThreadListParams{AccountID: "acc_nope", FolderID: m.inbox}, api.CodeAccountNotFound},
		{"unknown folder", api.ThreadListParams{AccountID: m.acc, FolderID: "f_nope"}, api.CodeFolderNotFound},
		{"bad sort", api.ThreadListParams{AccountID: m.acc, FolderID: m.inbox, Sort: "subject"}, api.CodeInvalidArgument},
		{"bad filter", api.ThreadListParams{AccountID: m.acc, FolderID: m.inbox, Filter: "starred"}, api.CodeInvalidArgument},
		{"bad cursor", api.ThreadListParams{AccountID: m.acc, FolderID: m.inbox, Page: api.Page{Cursor: "!!"}}, api.CodeInvalidArgument},
		{"cursor from other sort", api.ThreadListParams{AccountID: m.acc, FolderID: m.inbox, Page: api.Page{Cursor: asc.Page.NextCursor}}, api.CodeInvalidArgument},
		{"cursor from message.list", api.ThreadListParams{AccountID: m.acc, FolderID: m.inbox, Page: api.Page{Cursor: msgCursor.Page.NextCursor}}, api.CodeInvalidArgument},
	}
	for _, c := range cases {
		_, err := svc.List(ctx, c.p)
		if got := errCode(t, err); got != c.code {
			t.Errorf("%s: code %v, want %v", c.name, got, c.code)
		}
	}
	// And the other way round: a thread cursor is refused by message.list.
	_, err = m.b.Messages().List(ctx, api.MessageListParams{AccountID: m.acc, FolderID: m.inbox, Page: api.Page{Cursor: one.Page.NextCursor}})
	if got := errCode(t, err); got != api.CodeInvalidArgument {
		t.Errorf("thread cursor in message.list: %v", got)
	}
}

func TestThreadGet(t *testing.T) {
	m := seedThreadedMailbox(t)
	ctx := context.Background()
	svc := m.b.Threads()

	inFolder, err := svc.Get(ctx, api.ThreadGetParams{AccountID: m.acc, ThreadID: "t_a", FolderID: m.inbox})
	if err != nil {
		t.Fatal(err)
	}
	if len(inFolder.Messages) != 2 || inFolder.Messages[0].ID != m.a1 || inFolder.Messages[1].ID != m.a3 {
		t.Fatalf("inbox members = %+v", inFolder.Messages)
	}
	if th := inFolder.Thread; th.MessageCount != 2 || th.UnreadCount != 2 || th.Latest.ID != m.a3 || len(th.Participants) != 2 {
		t.Fatalf("inbox summary = %+v", th)
	}
	all, err := svc.Get(ctx, api.ThreadGetParams{AccountID: m.acc, ThreadID: "t_a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Messages) != 3 || all.Messages[1].ID != m.a2 || all.Messages[1].FolderID != m.sent {
		t.Fatalf("all members = %+v", all.Messages)
	}
	if th := all.Thread; th.MessageCount != 3 || th.UnreadCount != 2 || len(th.Participants) != 3 || th.Participants[1].Name != "Me" ||
		fmt.Sprint(th.FolderIDs) != fmt.Sprint(inFolder.Thread.FolderIDs) {
		t.Fatalf("account summary = %+v", th)
	}
	for _, msg := range all.Messages {
		if msg.ThreadID != "t_a" || msg.From == nil || msg.Flags == nil {
			t.Errorf("member %+v", msg)
		}
	}

	cases := []struct {
		name string
		p    api.ThreadGetParams
		code api.ErrorCode
	}{
		{"no account", api.ThreadGetParams{ThreadID: "t_a"}, api.CodeInvalidArgument},
		{"no thread", api.ThreadGetParams{AccountID: m.acc}, api.CodeInvalidArgument},
		{"unknown account", api.ThreadGetParams{AccountID: "acc_nope", ThreadID: "t_a"}, api.CodeAccountNotFound},
		{"unknown folder", api.ThreadGetParams{AccountID: m.acc, ThreadID: "t_a", FolderID: "f_nope"}, api.CodeFolderNotFound},
		{"unknown thread", api.ThreadGetParams{AccountID: m.acc, ThreadID: "t_nope"}, api.CodeThreadNotFound},
		{"no member in folder", api.ThreadGetParams{AccountID: m.acc, ThreadID: "t_c", FolderID: m.inbox}, api.CodeThreadNotFound},
	}
	for _, c := range cases {
		_, err := svc.Get(ctx, c.p)
		if got := errCode(t, err); got != c.code {
			t.Errorf("%s: code %v, want %v", c.name, got, c.code)
		}
	}
	// A thread of another account is unknown here.
	other := seedAccount(t, m.b, "other@example.invalid")
	if _, err := svc.Get(ctx, api.ThreadGetParams{AccountID: api.AccountID(other), ThreadID: "t_a"}); errCode(t, err) != api.CodeThreadNotFound {
		t.Errorf("other account: %v", err)
	}
}

// A row without a thread id (older store, not yet linked) is left out of
// the thread listing and shown by message.list with an empty threadId.
func TestThreadListSkipsUnlinked(t *testing.T) {
	m := seedThreadedMailbox(t)
	ctx := context.Background()
	res, err := m.b.Threads().List(ctx, api.ThreadListParams{AccountID: m.acc, FolderID: m.inbox, Page: api.Page{Limit: 100}})
	if err != nil {
		t.Fatal(err)
	}
	for _, th := range res.Threads {
		if th.Latest.ID == m.unlinked {
			t.Fatal("unlinked message listed as a thread")
		}
	}
	list, err := m.b.Messages().List(ctx, api.MessageListParams{AccountID: m.acc, FolderID: m.inbox})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, msg := range list.Messages {
		if msg.ID == m.unlinked {
			found = msg.ThreadID == ""
		}
	}
	if !found {
		t.Error("unlinked message missing from message.list or carrying a thread id")
	}
}

func sortedFolders(ids ...api.FolderID) []api.FolderID {
	out := append([]api.FolderID(nil), ids...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
