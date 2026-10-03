// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/maildate"
	"github.com/schotek/malachi/ui/internal/settings"
)

func TestDateSectionsScopeAndThreadFlags(t *testing.T) {
	key := folderKey{Account: "acc", Folder: "f_inbox"}
	w := &Window{dateWeekStart: time.Monday, model: mailModel{listFolder: key, folders: map[api.AccountID][]api.Folder{"acc": {{ID: "f_inbox", Role: api.RoleInbox}, {ID: "sent", Role: api.RoleSent}}}}}
	if !w.usesDateGroups() {
		t.Fatal("inbox not grouped")
	}
	w.model.search.active = true
	if w.usesDateGroups() {
		t.Fatal("search grouped")
	}
	w.model.search.active = false
	w.model.listFolder.Folder = "sent"
	if w.usesDateGroups() {
		t.Fatal("sent grouped")
	}
	w.model.listFolder = key
	w.model.grouped = true
	latest := member("new", "thread", 0, "alice")
	w.model.setThreads([]api.ThreadSummary{thr("thread", 2, 0, latest, api.FlagFlagged)}, api.PageInfo{Total: 1})
	sections := w.dateSections()
	if len(sections) != 1 || sections[0].Group.Kind != maildate.Flagged {
		t.Fatalf("aggregate flag ignored: %+v", sections)
	}
}

// Opt-in real-widget check; run under xvfb-run with MALACHI_GTK_TEST=1.
// It uses synthetic summaries and never connects to a daemon or mailbox.
func TestDateGroupsGTK(t *testing.T) {
	if os.Getenv("MALACHI_GTK_TEST") != "1" {
		t.Skip("requires GTK display (MALACHI_GTK_TEST=1)")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	key := folderKey{Account: "acc", Folder: "inbox"}
	w := &Window{
		settings: settings.NewMemory(), dateWeekStart: time.Monday,
		model:       mailModel{listFolder: key, folders: map[api.AccountID][]api.Folder{"acc": {{ID: "inbox", Role: api.RoleInbox}}}},
		messageList: gtk.NewListBox(), listStack: gtk.NewStack(), loadMoreButton: gtk.NewButton(),
		loadMoreSpinner: adw.NewSpinner(), searchNote: gtk.NewLabel(""),
	}
	w.listStack.AddNamed(gtk.NewBox(gtk.OrientationVertical, 0), "messages")
	today := summary("today")
	today.Date = time.Now()
	old := summary("old", api.FlagFlagged)
	old.Date = today.Date.AddDate(-1, 0, 0)
	w.model.setMessages([]api.MessageSummary{today, old}, api.PageInfo{Total: 2})
	w.syncDateRows(false)
	if _, ok := w.rowForWidget(w.messageList.RowAtIndex(0)); ok {
		t.Fatal("heading resolves to message")
	}
	flagged := w.rows[listKey{Message: "old"}]
	if flagged == nil || flagged.Index() != 1 {
		t.Fatal("old starred message not first")
	}
	w.messageList.SelectRow(flagged.ListBoxRow)
	if row, ok := w.selectedRow(); !ok || row.Message.ID != "old" {
		t.Fatalf("wrong selected message: %+v", row)
	}
	// Clear the star: preserve identity although its table position changes.
	w.model.messages[1].Flags = nil
	w.refreshRow("old")
	if row, ok := w.selectedRow(); !ok || row.Message.ID != "old" {
		t.Fatal("selection lost when unflagging")
	}
	group := maildate.Containing(old.Date, time.Now(), time.Monday)
	w.toggleDateGroup(group)
	if w.messageList.SelectedRow() != nil || w.rows[listKey{Message: "old"}] != nil {
		t.Fatal("hidden message stayed selectable")
	}
	w.toggleDateGroup(group)
	if w.rows[listKey{Message: "old"}] == nil {
		t.Fatal("unfold did not restore message")
	}
	// A new page can move an older star before all date groups.
	ancient := summary("ancient", api.FlagFlagged)
	ancient.Date = today.Date.AddDate(-2, 0, 0)
	w.model.appendMessages([]api.MessageSummary{ancient}, api.PageInfo{Total: 3})
	w.syncDateRows(false)
	if w.rows[listKey{Message: "ancient"}].Index() != 1 {
		t.Fatal("later page did not join first section")
	}
	// Removal and rollback must use message identity, never the header index.
	restore := w.removeMessageRow("old")
	if w.rows[listKey{Message: "old"}] != nil {
		t.Fatal("removed message remains")
	}
	restore()
	if w.rows[listKey{Message: "old"}] == nil {
		t.Fatal("rollback lost message")
	}
	// Search keeps the original flat ordering and has no section headings.
	w.model.search.active = true
	w.rebuildMessageRows()
	if first, ok := w.rowForWidget(w.messageList.RowAtIndex(0)); !ok || first.Message.ID != "today" {
		t.Fatal("search still has date headings")
	}
	if w.messageList.RowAtIndex(3) != nil {
		t.Fatal("search has extra rows")
	}
	w.model.search.active = false
	// Aggregate flag on a conversation moves the parent and every expanded
	// member together, even though its newest message itself is unstarred.
	a := member("a", "thread", 1, "alice", api.FlagFlagged)
	b := member("b", "thread", 2, "bob")
	thread := thr("thread", 2, 1, b, api.FlagFlagged)
	w.model.grouped = true
	w.model.setThreads([]api.ThreadSummary{thread}, api.PageInfo{Total: 1})
	w.model.setExpanded("thread", true)
	w.model.setMembers("thread", thread, []api.MessageSummary{a, b}, nil)
	w.syncRows()
	if w.rows[listKey{Thread: "thread"}].Index() != 1 || w.rows[listKey{Thread: "thread", Message: "a"}].Index() != 2 || w.rows[listKey{Thread: "thread", Message: "b"}].Index() != 3 {
		t.Fatal("conversation split across sections")
	}
	w.toggleDateGroup(maildate.Group{Kind: maildate.Flagged})
	if len(w.rows) != 0 || w.messageList.RowAtIndex(1) != nil {
		t.Fatal("folded conversation members remain visible")
	}
	w.toggleDateGroup(maildate.Group{Kind: maildate.Flagged})
	if len(w.rows) != 3 {
		t.Fatal("unfold lost conversation members")
	}
}
