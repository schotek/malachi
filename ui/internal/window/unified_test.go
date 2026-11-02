// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/selection"
	"github.com/schotek/malachi/ui/internal/settings"
	"github.com/schotek/malachi/ui/internal/unified"
)

// unifiedModel has two mail accounts, a and b, each with an inbox and a
// Sent folder, and the All Inboxes item on.
func unifiedModel() *mailModel {
	m := &mailModel{
		accounts: []api.Account{ruleAccount("a", "", api.MailCapabilities), ruleAccount("b", "", api.MailCapabilities)},
		folders: map[api.AccountID][]api.Folder{
			"a": {
				{ID: "ain", AccountID: "a", Path: "INBOX", Role: api.RoleInbox, Selectable: true, Synced: true, Unread: 2, Total: 10},
				{ID: "asent", AccountID: "a", Path: "Sent", Role: api.RoleSent, Selectable: true, Synced: true},
			},
			"b": {
				{ID: "bin", AccountID: "b", Path: "INBOX", Role: api.RoleInbox, Selectable: true, Synced: true, Unread: 3, Total: 5},
				{ID: "bsent", AccountID: "b", Path: "Sent", Role: api.RoleSent, Selectable: true, Synced: true},
			},
		},
		unifiedOn: true,
	}
	m.rebuildEntries()
	return m
}

func TestUnifiedEntryIsFirstWithTheSummedBadge(t *testing.T) {
	m := unifiedModel()
	e := m.entries[0]
	if !e.Unified || e.Header || e.Favourite || e.Badge != 5 || e.Folder.Total != 15 {
		t.Fatalf("first entry %+v", e)
	}
	if folderTitle(e.Folder) != "All Inboxes" || e.Folder.Role != api.RoleInbox {
		t.Errorf("title %q role %q", folderTitle(e.Folder), e.Folder.Role)
	}
	// A count that moves moves the item's badge too.
	m.adjustCounts(folderKey{Account: "b", Folder: "bin"}, -1, 0)
	if m.entries[0].Badge != 4 {
		t.Errorf("badge after a read: %d", m.entries[0].Badge)
	}
}

func TestUnifiedEntryFollowsTheSettingAndTheAccounts(t *testing.T) {
	m := unifiedModel()
	m.unifiedOn = false
	m.rebuildEntries()
	if m.entries[0].Unified {
		t.Error("shown with the setting off")
	}
	m.unifiedOn = true
	m.accounts = m.accounts[:1]
	m.rebuildEntries()
	for _, e := range m.entries {
		if e.Unified {
			t.Error("shown with one mail account")
		}
	}
	if m.folderListed(folderKey{Account: unified.Key.Account, Folder: unified.Key.Folder}) {
		t.Error("the key is listed while the item is hidden")
	}
}

func TestUnifiedKeyIsListedAndNeverTheInitialFolder(t *testing.T) {
	m := unifiedModel()
	k := folderKey{Account: unified.Key.Account, Folder: unified.Key.Folder}
	if !m.folderListed(k) {
		t.Error("the shown item is not listed")
	}
	if got, ok := m.initialFolder(); !ok || got != (folderKey{Account: "a", Folder: "ain"}) {
		t.Errorf("initial folder %+v %v", got, ok)
	}
	f, ok := m.folder(k)
	if !ok || f.Unread != 5 || f.Total != 15 {
		t.Errorf("folder(key) = %+v %v", f, ok)
	}
	if folderCountsText(f) == "" {
		t.Error("no counts text")
	}
}

func TestUnifiedListCoversItsInboxesOnly(t *testing.T) {
	m := unifiedModel()
	m.listFolder = folderKey{Account: unified.Key.Account, Folder: unified.Key.Folder}
	for k, want := range map[folderKey]bool{
		{Account: "a", Folder: "ain"}:   true,
		{Account: "b", Folder: "bin"}:   true,
		{Account: "a", Folder: "asent"}: false,
		{Account: "b", Folder: "ain"}:   false,
	} {
		if m.listCovers(k) != want {
			t.Errorf("listCovers(%v) != %v", k, want)
		}
	}
	m.listFolder = folderKey{Account: "a", Folder: "ain"}
	if !m.listCovers(folderKey{Account: "a", Folder: "ain"}) || m.listCovers(folderKey{Account: "b", Folder: "bin"}) {
		t.Error("a folder list covers only itself")
	}
}

func TestUnifiedListParams(t *testing.T) {
	m := unifiedModel()
	m.listFilter = api.FilterUnread
	p := m.messageListParams(folderKey{Account: unified.Key.Account, Folder: unified.Key.Folder}, "c1")
	if p.Unified != api.RoleInbox || p.AccountID != "" || p.FolderID != "" || p.Page.Cursor != "c1" || p.Filter != api.FilterUnread {
		t.Errorf("message params %+v", p)
	}
	tp := m.threadListParams(folderKey{Account: unified.Key.Account, Folder: unified.Key.Folder}, "")
	if tp.Unified != api.RoleInbox || tp.AccountID != "" || tp.FolderID != "" {
		t.Errorf("thread params %+v", tp)
	}
	q := m.messageListParams(folderKey{Account: "a", Folder: "ain"}, "")
	if q.Unified != "" || q.AccountID != "a" || q.FolderID != "ain" || q.Page.Limit != api.DefaultPageLimit {
		t.Errorf("folder params %+v", q)
	}
}

func TestUnifiedConversationIsReadInItsOwnAccountAndFolder(t *testing.T) {
	m := unifiedModel()
	m.listFolder = folderKey{Account: unified.Key.Account, Folder: unified.Key.Folder}
	m.grouped = true
	m.setThreads([]api.ThreadSummary{
		{ID: "t1", AccountID: "b", Latest: api.MessageSummary{ID: "m1", AccountID: "b", FolderID: "bin"}, MessageCount: 2},
	}, api.PageInfo{})
	key := m.threadKey("b", "t1")
	if got := m.membersFolder(key); got != (folderKey{Account: "b", Folder: "bin"}) {
		t.Errorf("membersFolder = %+v", got)
	}
	if m.rawThread(key) != "t1" {
		t.Errorf("rawThread(%q) = %q", key, m.rawThread(key))
	}
	m.listFolder = folderKey{Account: "a", Folder: "ain"}
	if got := m.membersFolder("t1"); got != m.listFolder || m.threadKey("b", "t1") != "t1" {
		t.Errorf("folder list: %+v", got)
	}
}

func TestUnifiedRowsNameTheirAccount(t *testing.T) {
	m := unifiedModel()
	s := api.MessageSummary{ID: "1", AccountID: "b", FolderID: "bin"}
	if got := m.rowMessage(s); got.Origin != "" {
		t.Errorf("origin in a folder list: %q", got.Origin)
	}
	m.listFolder = folderKey{Account: unified.Key.Account, Folder: unified.Key.Folder}
	if got := m.rowMessage(s); got.Origin == "" {
		t.Error("no origin in the All Inboxes list")
	}
}

func TestUnifiedSearchIsAcrossAllAccounts(t *testing.T) {
	m := unifiedModel()
	m.selected = folderKey{Account: unified.Key.Account, Folder: unified.Key.Folder}
	for _, scope := range []settings.SearchScope{settings.SearchFolder, settings.SearchAccount} {
		p, eff := m.searchRequest("hello", scope)
		if eff != settings.SearchAll || p.AccountID != "" || p.FolderID != "" {
			t.Errorf("scope %s: %+v %s", scope, p, eff)
		}
	}
}

func TestUnifiedSelectionAcrossAccounts(t *testing.T) {
	a := listRow{Key: listKey{Message: "1"}, Message: api.MessageSummary{ID: "1", AccountID: "a", FolderID: "ain"}}
	b := listRow{Key: listKey{Message: "2"}, Message: api.MessageSummary{ID: "2", AccountID: "b", FolderID: "bin"}}
	rows := []selection.Row[listKey]{{Key: a.Key, Account: "a"}, {Key: b.Key, Account: "b"}}
	if got := selection.RestrictIn(rows, b.Key, true); len(got) != 2 {
		t.Errorf("unified selection restricted: %v", got)
	}
	if got := selection.RestrictIn(rows, b.Key, false); len(got) != 1 || got[0] != b.Key {
		t.Errorf("folder selection kept: %v", got)
	}
}

func TestAccountGroupsSplitOnePerAccountInOrder(t *testing.T) {
	list := []api.MessageSummary{
		{ID: "1", AccountID: "b"}, {ID: "2", AccountID: "a"}, {ID: "3", AccountID: "b"},
	}
	g := accountGroups(list)
	if len(g) != 2 || g[0].Account != "b" || len(g[0].Items) != 2 || g[1].Account != "a" {
		t.Fatalf("groups %+v", g)
	}
	if crmSubjectsMixed([]crmSubject{{s: list[0]}, {s: list[2]}}) || !crmSubjectsMixed([]crmSubject{{s: list[0]}, {s: list[1]}}) {
		t.Error("crmSubjectsMixed wrong")
	}
}

func TestUnifiedActionStateIsMixedAcrossAccounts(t *testing.T) {
	m := unifiedModel()
	a := listRow{Key: listKey{Message: "1"}, Message: api.MessageSummary{ID: "1", AccountID: "a", FolderID: "ain"}}
	b := listRow{Key: listKey{Message: "2"}, Message: api.MessageSummary{ID: "2", AccountID: "b", FolderID: "bin"}}
	sa, sb := m.messageActionState(a, true), m.messageActionState(b, true)
	got := sa.with(selection.CombineAccounts([]selection.Flags{sa.flags(), sb.flags()}, []api.AccountID{"a", "b"}))
	if !got.mixed || got.move || got.forward {
		t.Errorf("mixed state %+v", got)
	}
}

// Two accounts may hold a conversation under the same thread id (Graph's
// Exchange conversation id): in the All Inboxes list they are two rows.
func TestUnifiedSameThreadIDOfTwoAccountsAreTwoConversations(t *testing.T) {
	m := unifiedModel()
	m.listFolder = folderKey{Account: unified.Key.Account, Folder: unified.Key.Folder}
	m.grouped = true
	mk := func(acc api.AccountID, folder api.FolderID, id api.MessageID) api.ThreadSummary {
		return api.ThreadSummary{ID: "same", AccountID: acc, MessageCount: 2,
			Latest: api.MessageSummary{ID: id, AccountID: acc, FolderID: folder, ThreadID: "same"}}
	}
	m.setThreads([]api.ThreadSummary{mk("a", "ain", "m1"), mk("b", "bin", "m2")}, api.PageInfo{})
	if len(m.threads) != 2 || len(m.tindex) != 2 {
		t.Fatalf("threads %d, index %d", len(m.threads), len(m.tindex))
	}
	if m.membersFolder(m.threadKey("a", "same")) != (folderKey{Account: "a", Folder: "ain"}) ||
		m.membersFolder(m.threadKey("b", "same")) != (folderKey{Account: "b", Folder: "bin"}) {
		t.Error("each conversation is read in its own account")
	}
	// A new message lands in its own account's conversation.
	s := api.MessageSummary{ID: "m3", AccountID: "b", FolderID: "bin", ThreadID: "same"}
	m.applyNewMessage(s, api.FilterAll, listKey{})
	if m.threads[m.tindex[m.threadKey("a", "same")]].MessageCount != 2 ||
		m.threads[m.tindex[m.threadKey("b", "same")]].MessageCount != 3 {
		t.Error("the new message went to the wrong conversation")
	}
}
