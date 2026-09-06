// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"testing"
	"time"

	"github.com/schotek/malachi/backend/pkg/api"
)

func summary(id string, flags ...api.Flag) api.MessageSummary {
	return api.MessageSummary{ID: api.MessageID(id), Subject: "s-" + id, Flags: flags}
}

func TestHasFlag(t *testing.T) {
	flags := []api.Flag{api.FlagSeen, api.FlagFlagged}
	if !hasFlag(flags, api.FlagFlagged) || hasFlag(flags, api.FlagJunk) || hasFlag(nil, api.FlagSeen) {
		t.Error("hasFlag wrong")
	}
}

func TestMatchesFilter(t *testing.T) {
	seen := summary("a", api.FlagSeen)
	seenFlagged := summary("b", api.FlagSeen, api.FlagFlagged)
	unread := summary("c")
	unreadFlagged := summary("d", api.FlagFlagged)

	cases := []struct {
		filter api.MessageFilter
		want   map[api.MessageID]bool
	}{
		{"", map[api.MessageID]bool{"a": true, "b": true, "c": true, "d": true}},
		{api.FilterAll, map[api.MessageID]bool{"a": true, "b": true, "c": true, "d": true}},
		{api.FilterUnread, map[api.MessageID]bool{"a": false, "b": false, "c": true, "d": true}},
		{api.FilterFlagged, map[api.MessageID]bool{"a": false, "b": true, "c": false, "d": true}},
	}
	for _, c := range cases {
		for _, s := range []api.MessageSummary{seen, seenFlagged, unread, unreadFlagged} {
			if got := matchesFilter(s, c.filter); got != c.want[s.ID] {
				t.Errorf("matchesFilter(%s, %q) = %v", s.ID, c.filter, got)
			}
		}
	}
}

func TestSummaryMessage(t *testing.T) {
	date := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	s := api.MessageSummary{
		From:           []api.Address{{Name: "Alice", Address: "alice@example.invalid"}},
		Subject:        "Hi",
		Snippet:        "snip",
		Date:           date,
		Flags:          []api.Flag{api.FlagFlagged},
		HasAttachments: true,
	}
	m := summaryMessage(s)
	if len(m.From) != 1 || m.From[0].Name != "Alice" || m.Subject != "Hi" || m.Snippet != "snip" || !m.Date.Equal(date) {
		t.Errorf("projection: %+v", m)
	}
	if !m.Unread || !m.Flagged || !m.HasAttachments {
		t.Errorf("flags: %+v", m)
	}
	s.Flags = []api.Flag{api.FlagSeen}
	s.From = nil
	m = summaryMessage(s)
	if m.Unread || m.Flagged || len(m.From) != 0 {
		t.Errorf("seen, no sender: %+v", m)
	}
}

func TestSetAppendMessages(t *testing.T) {
	var m mailModel
	m.setMessages([]api.MessageSummary{summary("a"), summary("b"), summary("a")}, api.PageInfo{NextCursor: "c1", Total: 10})
	if len(m.messages) != 2 || m.index["a"] != 0 || m.index["b"] != 1 || m.nextCursor != "c1" || m.total != 10 {
		t.Fatalf("set: %+v", m)
	}
	added := m.appendMessages([]api.MessageSummary{summary("b"), summary("c")}, api.PageInfo{Total: 10})
	if added != 1 || len(m.messages) != 3 || m.index["c"] != 2 || m.nextCursor != "" {
		t.Errorf("append: added=%d %+v", added, m)
	}
	if s, idx, ok := m.message("c"); !ok || idx != 2 || s.ID != "c" {
		t.Errorf("message(c): %v %d %v", s, idx, ok)
	}
	if _, ok := m.messageAt(3); ok {
		t.Error("messageAt out of range")
	}
	if s, ok := m.messageAt(1); !ok || s.ID != "b" {
		t.Errorf("messageAt(1): %v", s)
	}
	// Replacing the page resets the index and error.
	m.listErr = errTest
	m.setMessages(nil, api.PageInfo{Total: -1})
	if len(m.messages) != 0 || len(m.index) != 0 || m.listErr != nil || m.total != -1 {
		t.Errorf("reset: %+v", m)
	}
}

var errTest = &api.Error{Code: api.CodeInternalError, Message: "test"}

func TestInsertRemoveMessage(t *testing.T) {
	var m mailModel
	if !m.insertMessage(5, summary("a")) || len(m.messages) != 1 || m.index["a"] != 0 {
		t.Fatalf("insert into empty: %+v", m)
	}
	m.total = 1
	if !m.insertMessage(0, summary("b")) || m.messages[0].ID != "b" || m.index["a"] != 1 || m.total != 2 {
		t.Fatalf("insert at top: %+v", m)
	}
	if m.insertMessage(0, summary("a")) {
		t.Error("duplicate insert accepted")
	}
	m.insertMessage(-1, summary("c"))
	if m.messages[0].ID != "c" || m.index["b"] != 1 || m.index["a"] != 2 {
		t.Errorf("negative index: %+v", m.messages)
	}

	s, idx, ok := m.removeMessage("b")
	if !ok || idx != 1 || s.ID != "b" || len(m.messages) != 2 || m.index["a"] != 1 || m.total != 2 {
		t.Errorf("remove: %v %d %v %+v", s, idx, ok, m)
	}
	if _, present := m.index["b"]; present {
		t.Error("removed id still indexed")
	}
	if _, _, ok := m.removeMessage("b"); ok {
		t.Error("second remove succeeded")
	}
	m.total = -1
	m.removeMessage("a")
	if m.total != -1 {
		t.Error("unknown total was decremented")
	}
}

func TestUpdateFlags(t *testing.T) {
	var m mailModel
	m.setMessages([]api.MessageSummary{summary("a", api.FlagSeen)}, api.PageInfo{})
	if m.updateFlags("a", []api.Flag{api.FlagSeen}, nil) {
		t.Error("no-op reported a change")
	}
	if !m.updateFlags("a", []api.Flag{api.FlagFlagged}, []api.Flag{api.FlagSeen}) {
		t.Error("change not reported")
	}
	s, _, _ := m.message("a")
	if hasFlag(s.Flags, api.FlagSeen) || !hasFlag(s.Flags, api.FlagFlagged) || len(s.Flags) != 1 {
		t.Errorf("flags: %v", s.Flags)
	}
	if m.updateFlags("zz", []api.Flag{api.FlagSeen}, nil) {
		t.Error("unknown id reported a change")
	}
}

func testAccounts() ([]api.Account, map[api.AccountID][]api.Folder) {
	accounts := []api.Account{
		{ID: "acc1", Enabled: true, Config: api.AccountConfig{Email: "one@example.invalid"}},
		{ID: "acc2", Enabled: true, Config: api.AccountConfig{Email: "two@example.invalid"}},
		{ID: "acc3", Enabled: false, Config: api.AccountConfig{Email: "off@example.invalid"}},
	}
	folders := map[api.AccountID][]api.Folder{
		"acc1": {
			{ID: "zeta", Name: "zeta", Path: "zeta", Role: api.RoleNone, Selectable: true},
			{ID: "trash", Name: "Trash", Path: "Trash", Role: api.RoleTrash, Selectable: true},
			{ID: "inbox", Name: "INBOX", Path: "INBOX", Role: api.RoleInbox, Selectable: true, Unread: 2},
			{ID: "alpha", Name: "Alpha", Path: "Alpha", Role: api.RoleNone, Selectable: true},
			{ID: "sub2", Name: "b", Path: "Alpha/b", ParentID: "alpha", Selectable: true},
			{ID: "sub1", Name: "A", Path: "Alpha/A", ParentID: "alpha", Selectable: true},
			{ID: "deep", Name: "deep", Path: "Alpha/A/deep", ParentID: "sub1", Selectable: true},
			{ID: "container", Name: "Container", Path: "Container", Selectable: false},
			{ID: "leaf", Name: "leaf", Path: "Container/leaf", ParentID: "container", Selectable: true},
		},
		"acc2": {
			{ID: "in2", Name: "INBOX", Path: "INBOX", Role: api.RoleInbox, Selectable: true},
		},
		"acc3": {
			{ID: "in3", Name: "INBOX", Path: "INBOX", Role: api.RoleInbox, Selectable: true},
		},
	}
	return accounts, folders
}

func TestSortFolders(t *testing.T) {
	accounts, folders := testAccounts()
	entries := sortFolders(accounts, folders, newCollapseState(), newFavouriteState())

	type row struct {
		id     api.FolderID
		header bool
		depth  int
	}
	want := []row{
		{header: true},
		{id: "inbox"}, {id: "trash"},
		{id: "alpha"}, {id: "sub1", depth: 1}, {id: "deep", depth: 2}, {id: "sub2", depth: 1},
		{id: "container"}, {id: "leaf", depth: 1},
		{id: "zeta"},
		{header: true},
		{id: "in2"},
	}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(entries), len(want), entries)
	}
	for i, e := range entries {
		got := row{id: e.Folder.ID, header: e.Header, depth: e.Depth}
		if got != want[i] {
			t.Errorf("entry %d: got %+v, want %+v", i, got, want[i])
		}
	}
	if entries[0].Account.ID != "acc1" || entries[10].Account.ID != "acc2" {
		t.Error("header accounts wrong")
	}

	// A single enabled account gets no header row.
	single := sortFolders(accounts[:1], folders, newCollapseState(), newFavouriteState())
	if len(single) != 9 || single[0].Header {
		t.Errorf("single account: %+v", single)
	}
	if got := sortFolders(nil, nil, newCollapseState(), newFavouriteState()); len(got) != 0 {
		t.Errorf("no accounts: %+v", got)
	}
}

func TestSortFoldersCycleGuard(t *testing.T) {
	accounts := []api.Account{{ID: "a", Enabled: true}}
	folders := map[api.AccountID][]api.Folder{"a": {
		{ID: "x", Name: "x", Path: "p/x", ParentID: "y", Selectable: true},
		{ID: "y", Name: "y", Path: "p/q/y", ParentID: "x", Selectable: true},
		{ID: "self", Name: "self", Path: "self", ParentID: "self", Selectable: true},
		{ID: "orphan", Name: "o", Path: "gone/o", ParentID: "missing", Selectable: true},
	}}
	entries := sortFolders(accounts, folders, newCollapseState(), newFavouriteState())
	if len(entries) != 4 {
		t.Fatalf("got %d entries: %+v", len(entries), entries)
	}
	depths := map[api.FolderID]int{}
	for _, e := range entries {
		depths[e.Folder.ID] = e.Depth
	}
	// Self-parent and missing parent are roots; the cycle falls back to
	// the path depth.
	if depths["self"] != 0 || depths["orphan"] != 0 || depths["x"] != 1 || depths["y"] != 2 {
		t.Errorf("depths: %v", depths)
	}
	if entries[0].Folder.ID != "orphan" || entries[1].Folder.ID != "self" {
		t.Errorf("roots order: %+v", entries[:2])
	}
}

func TestRoleRankAndIcon(t *testing.T) {
	roles := []api.FolderRole{api.RoleInbox, api.RoleDrafts, api.RoleSent, api.RoleArchive, api.RoleJunk, api.RoleTrash, api.RoleOutbox, api.RoleAll}
	for i := 1; i < len(roles); i++ {
		if roleRank(roles[i-1]) >= roleRank(roles[i]) {
			t.Errorf("%s should sort before %s", roles[i-1], roles[i])
		}
	}
	if roleRank(api.RoleNone) <= roleRank(api.RoleAll) || roleRank("bogus") != roleRank(api.RoleNone) {
		t.Error("plain folders must sort last")
	}
	icons := map[api.FolderRole]string{
		api.RoleInbox:   "mail-unread-symbolic",
		api.RoleDrafts:  "document-edit-symbolic",
		api.RoleSent:    "mail-send-symbolic",
		api.RoleTrash:   "user-trash-symbolic",
		api.RoleJunk:    "mail-mark-junk-symbolic",
		api.RoleArchive: "folder-download-symbolic",
		api.RoleOutbox:  "mail-send-symbolic",
		api.RoleAll:     "folder-symbolic",
		api.RoleNone:    "folder-symbolic",
		"bogus":         "folder-symbolic",
	}
	for role, want := range icons {
		if got := roleIcon(role); got != want {
			t.Errorf("roleIcon(%s) = %q, want %q", role, got, want)
		}
	}
}

func TestModelFolders(t *testing.T) {
	accounts, folders := testAccounts()
	m := mailModel{accounts: accounts, folders: folders}
	m.rebuildEntries()

	k, ok := m.initialFolder()
	if !ok || k != (folderKey{Account: "acc1", Folder: "inbox"}) {
		t.Errorf("initialFolder: %+v %v", k, ok)
	}
	if f, ok := m.folder(k); !ok || f.Unread != 2 {
		t.Errorf("folder: %+v %v", f, ok)
	}
	if _, ok := m.folder(folderKey{Account: "acc1", Folder: "nope"}); ok {
		t.Error("unknown folder found")
	}
	if f, ok := m.folderByRole("acc1", api.RoleTrash); !ok || f.ID != "trash" {
		t.Errorf("folderByRole: %+v %v", f, ok)
	}
	if _, ok := m.folderByRole("acc1", api.RoleArchive); ok {
		t.Error("missing role found")
	}
	if a, ok := m.account("acc2"); !ok || a.Config.Email != "two@example.invalid" {
		t.Errorf("account: %+v %v", a, ok)
	}
	if _, ok := m.account("acc9"); ok {
		t.Error("unknown account found")
	}
	if got := m.enabledAccounts(); len(got) != 2 || got[0].ID != "acc1" || got[1].ID != "acc2" {
		t.Errorf("enabledAccounts: %+v", got)
	}

	m.adjustUnread(k, -5)
	if f, _ := m.folder(k); f.Unread != 0 {
		t.Errorf("floor: %d", f.Unread)
	}
	m.adjustUnread(k, 3)
	if f, _ := m.folder(k); f.Unread != 3 {
		t.Errorf("adjust: %d", f.Unread)
	}
	for _, e := range m.entries {
		if !e.Header && e.Folder.ID == "inbox" && e.Folder.Unread != 3 {
			t.Errorf("entry not updated: %d", e.Folder.Unread)
		}
	}
	m.adjustUnread(folderKey{Account: "acc1", Folder: "nope"}, 1) // no panic
}

func TestVisibleFolders(t *testing.T) {
	list := []api.Folder{
		{ID: "in", Path: "INBOX", Role: api.RoleInbox, Selectable: true, Total: 0},
		{ID: "trash", Path: "Trash", Role: api.RoleTrash, Selectable: true, Total: 0},
		{ID: "out", Path: "Outbox", Role: api.RoleOutbox, Selectable: true, Total: 0},
	}
	// An empty outbox is hidden; an empty Trash (or Inbox) is not.
	got := visibleFolders(list)
	if len(got) != 2 || got[0].ID != "in" || got[1].ID != "trash" {
		t.Errorf("empty outbox: %+v", got)
	}
	list[2].Total = 1
	if got := visibleFolders(list); len(got) != 3 || got[2].ID != "out" {
		t.Errorf("non-empty outbox: %+v", got)
	}
	if got := visibleFolders(nil); got == nil || len(got) != 0 {
		t.Errorf("nil list: %#v", got)
	}

	// The sidebar entries follow, while the model still knows the folder.
	m := mailModel{
		accounts: []api.Account{{ID: "a", Enabled: true}},
		folders:  map[api.AccountID][]api.Folder{"a": {list[0], list[1], {ID: "out", Path: "Outbox", Role: api.RoleOutbox, Selectable: true}}},
	}
	m.rebuildEntries()
	if got := ids(m.entries); !equalIDs(got, []string{"in@0", "trash@0"}) {
		t.Errorf("entries: %v", got)
	}
	if f, ok := m.folderByRole("a", api.RoleOutbox); !ok || f.ID != "out" {
		t.Errorf("hidden outbox not found by role: %+v %v", f, ok)
	}
	if m.folderRole(folderKey{Account: "a", Folder: "out"}) != api.RoleOutbox || m.folderRole(folderKey{Account: "a", Folder: "nope"}) != api.RoleNone {
		t.Error("folderRole wrong")
	}
	m.folders["a"][2].Total = 2
	m.rebuildEntries()
	if got := ids(m.entries); !equalIDs(got, []string{"in@0", "trash@0", "out@0"}) {
		t.Errorf("entries with a full outbox: %v", got)
	}
}

func TestInitialFolderFallbacks(t *testing.T) {
	m := mailModel{
		accounts: []api.Account{{ID: "a", Enabled: true}},
		folders: map[api.AccountID][]api.Folder{"a": {
			{ID: "c", Name: "c", Path: "c", Selectable: false},
			{ID: "b", Name: "b", Path: "b", Selectable: true},
		}},
	}
	m.rebuildEntries()
	if k, ok := m.initialFolder(); !ok || k.Folder != "b" {
		t.Errorf("first selectable: %+v %v", k, ok)
	}
	m.folders["a"][1].Selectable = false
	m.rebuildEntries()
	if _, ok := m.initialFolder(); ok {
		t.Error("nothing selectable but a folder was returned")
	}
	if _, ok := (&mailModel{}).initialFolder(); ok {
		t.Error("empty model returned a folder")
	}
}

func TestGenerations(t *testing.T) {
	var m mailModel
	if m.bumpList() != 1 || m.bumpList() != 2 || m.bumpBody() != 1 || m.bumpFolders() != 1 || m.listGen != 2 {
		t.Errorf("generations: %+v", m)
	}
	m.loading, m.loadingMore = true, true
	m.bumpAll()
	if m.listGen != 3 || m.bodyGen != 2 || m.foldersGen != 2 || m.loading || m.loadingMore {
		t.Errorf("bumpAll: %+v", m)
	}
}

func TestClearMessages(t *testing.T) {
	var m mailModel
	m.setMessages([]api.MessageSummary{summary("a")}, api.PageInfo{NextCursor: "c", Total: 3})
	m.listErr = errTest
	gen := m.listGen
	m.clearMessages()
	if len(m.messages) != 0 || len(m.index) != 0 || m.nextCursor != "" || m.total != -1 || m.listErr != nil {
		t.Errorf("clear: %+v", m)
	}
	if m.listGen != gen {
		t.Error("clearMessages must not touch the generation")
	}
}

func TestAccountLabel(t *testing.T) {
	a := api.Account{Config: api.AccountConfig{Name: " Work ", Email: "me@example.invalid"}}
	if got := accountLabel(a); got != "Work" {
		t.Errorf("name: %q", got)
	}
	a.Config.Name = "  "
	if got := accountLabel(a); got != "me@example.invalid" {
		t.Errorf("fallback: %q", got)
	}
}

func TestSelfAddress(t *testing.T) {
	a := api.Account{Config: api.AccountConfig{DisplayName: "Me", Email: "me@example.invalid"}}
	if got := selfAddress(a); got != (api.Address{Name: "Me", Address: "me@example.invalid"}) {
		t.Errorf("selfAddress: %+v", got)
	}
}
