// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package contextmenu

import (
	"fmt"
	"strings"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// identity returns the msgids; N picks English's form.
type identity struct{}

func (identity) T(msgid string) string { return msgid }
func (identity) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}
func (identity) C(_, msgid string) string { return msgid }

// show writes groups as "id id- | id", "-" after a disabled item.
func show(gs [][]Item) string {
	parts := make([]string, len(gs))
	for i, g := range gs {
		ids := make([]string, len(g))
		for j, it := range g {
			ids[j] = string(it.ID)
			if !it.Enabled {
				ids[j] += "-"
			}
		}
		parts[i] = strings.Join(ids, " ")
	}
	return strings.Join(parts, " | ")
}

// A mail message everything applies to.
var mail = MessageState{
	OfferReply: true, OfferReplyAll: true, OfferForward: true,
	OfferArchive: true, OfferMove: true, OfferJunk: true, OfferTrash: true,
	Reply: true, ReplyAll: true, Forward: true, MarkUnread: true, ToggleFlag: true,
	Archive: true, Move: true, Junk: true, Trash: true, Assistant: true,
}

func TestMessageItems(t *testing.T) {
	cases := []struct {
		name string
		s    func(MessageState) MessageState
		want string
	}{
		{"read mail", func(s MessageState) MessageState { return s },
			"open | reply replyAll forward | markUnread toggleFlag | archive moveTo junk trash | assistant"},
		{"unread mail, no Assistant", func(s MessageState) MessageState {
			s.MarkUnread, s.MarkRead, s.Assistant = false, true, false
			return s
		}, "open | reply replyAll forward | markRead toggleFlag | archive moveTo junk trash"},
		{"conversation with read and unread members", func(s MessageState) MessageState {
			s.MarkRead = true
			return s
		}, "open | reply replyAll forward | markRead markUnread toggleFlag | archive moveTo junk trash | assistant"},
		{"in the Archive and the Junk folder of its own", func(s MessageState) MessageState {
			s.Archive, s.Junk = false, false
			return s
		}, "open | reply replyAll forward | markUnread toggleFlag | archive- moveTo junk- trash | assistant"},
		{"draft", func(s MessageState) MessageState {
			s.Draft = true
			return s
		}, "edit | reply replyAll forward | markUnread toggleFlag | archive moveTo junk trash | assistant"},
		{"outbox", func(s MessageState) MessageState {
			return MessageState{Outbox: true, OfferReply: true, OfferForward: true, OfferTrash: true,
				OfferArchive: true, OfferMove: true, OfferJunk: true, Reply: true, Forward: true, Trash: true}
		}, "open | reply forward | toggleFlag- | archive- moveTo- junk- trash"},
		{"issue tracker", func(MessageState) MessageState {
			// Comments and forwards only; Trash is not offered.
			return MessageState{OfferReply: true, OfferForward: true, Reply: true, Forward: true,
				MarkRead: true, ToggleFlag: true, ChangeStatus: true, Assistant: true}
		}, "open | reply forward | markRead toggleFlag changeStatus | assistant"},
		{"queued comment of an issue tracker", func(MessageState) MessageState {
			return MessageState{Outbox: true, Trash: true}
		}, "open | toggleFlag- | trash"},
	}
	for _, c := range cases {
		if got := show(MessageItems(c.s(mail))); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestFolderItems(t *testing.T) {
	cases := []struct {
		name string
		f    FolderState
		want string
	}{
		{"inbox with unread mail", FolderState{Selectable: true, Role: api.RoleInbox, Unread: 3, Total: 9, CanDelete: true, Foldable: true},
			"checkMail | markAllRead | favourite fold"},
		{"all read", FolderState{Selectable: true, Role: api.RoleNone, Total: 9, CanDelete: true},
			"checkMail | markAllRead- | favourite"},
		{"trash", FolderState{Selectable: true, Role: api.RoleTrash, Unread: 1, Total: 4, CanDelete: true},
			"checkMail | markAllRead empty | favourite"},
		{"empty junk", FolderState{Selectable: true, Role: api.RoleJunk, CanDelete: true},
			"checkMail | markAllRead- empty- | favourite"},
		{"trash of an account that cannot delete", FolderState{Selectable: true, Role: api.RoleTrash, Total: 4},
			"checkMail | markAllRead- | favourite"},
		{"outbox", FolderState{Selectable: true, Role: api.RoleOutbox, Total: 2, CanDelete: true},
			"favourite"},
		{"container", FolderState{Role: api.RoleNone, Foldable: true}, "fold"},
		{"container that does not fold", FolderState{Role: api.RoleTrash, Total: 3, CanDelete: true}, ""},
	}
	for _, c := range cases {
		if got := show(FolderItems(c.f)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	if got := show(AccountItems(true)); got != "checkMail | fold" {
		t.Errorf("account: %s", got)
	}
	if got := show(AccountItems(false)); got != "checkMail" {
		t.Errorf("account without folding: %s", got)
	}
}

func folder(id string, role api.FolderRole, selectable bool) api.Folder {
	return api.Folder{ID: api.FolderID(id), Name: id, Role: role, Selectable: selectable}
}

func TestMoveTargets(t *testing.T) {
	view := folder("open", api.RoleNone, true)
	view.Virtual = api.VirtualOpen
	entries := []Entry{
		{Folder: api.Folder{}}, // a heading
		{Folder: folder("inbox", api.RoleInbox, true)},
		{Folder: folder("drafts", api.RoleDrafts, true)},
		{Folder: folder("sent", api.RoleSent, true)},
		{Folder: folder("all", api.RoleAll, true)},
		{Folder: folder("trash", api.RoleTrash, true)},
		{Folder: folder("outbox", api.RoleOutbox, true)},
		{Folder: view},
		// A container with a subfolder, a container with only a container
		// under it, and a nested one whose subfolder is the current one.
		{Folder: folder("projects", api.RoleNone, false)},
		{Folder: folder("client", api.RoleNone, true), Depth: 1},
		{Folder: folder("empty", api.RoleNone, false)},
		{Folder: folder("emptier", api.RoleNone, false), Depth: 1},
		{Folder: folder("lists", api.RoleNone, false)},
		{Folder: folder("deep", api.RoleNone, false), Depth: 1},
		{Folder: folder("current", api.RoleNone, true), Depth: 2},
	}
	var got []string
	for _, tg := range MoveTargets(entries, "current") {
		s := fmt.Sprintf("%s/%d", tg.Folder.ID, tg.Depth)
		if !tg.Enabled {
			s += "-"
		}
		got = append(got, s)
	}
	want := "inbox/0 sent/0 trash/0 projects/0- client/1 lists/0- deep/1- current/2-"
	if strings.Join(got, " ") != want {
		t.Errorf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
	if len(MoveTargets(nil, "x")) != 0 {
		t.Error("targets out of nothing")
	}
}

func TestLabels(t *testing.T) {
	tr := identity{}
	cases := []struct {
		id   ItemID
		l    Labels
		want string
	}{
		{OpenMessage, Labels{}, "Open in New Window"},
		{EditDraft, Labels{}, "Edit"},
		{Reply, Labels{}, "Reply"},
		{Reply, Labels{Comment: true}, "Comment"},
		{ReplyAll, Labels{}, "Reply All"},
		{Forward, Labels{}, "Forward"},
		{MarkRead, Labels{}, "Mark as _Read"},
		{MarkUnread, Labels{}, "Mark as _Unread"},
		{ToggleFlag, Labels{}, "Star"},
		{ToggleFlag, Labels{Flagged: true}, "Unstar"},
		{ChangeStatus, Labels{}, "Change Status"},
		{Archive, Labels{}, "Archive"},
		{MoveTo, Labels{}, "Move To"},
		{Junk, Labels{}, "Mark as Junk"},
		{Trash, Labels{}, "Move to Trash"},
		{Trash, Labels{Outbox: true}, "Cancel Sending"},
		{Assistant, Labels{}, "Assistant"},
		{CheckMail, Labels{}, "Check for New Mail"},
		{MarkAllRead, Labels{}, "Mark All as Read"},
		{EmptyFolder, Labels{}, "Empty Trash"},
		{EmptyFolder, Labels{Junk: true}, "Empty Junk"},
		{Favourite, Labels{}, "Add to Favourites"},
		{Favourite, Labels{Favourite: true}, "Remove from Favourites"},
		{Fold, Labels{}, "Collapse"},
		{Fold, Labels{Collapsed: true}, "Expand"},
		{"nothing", Labels{}, ""},
	}
	for _, c := range cases {
		if got := Label(tr, c.id, c.l); got != c.want {
			t.Errorf("%s %+v: %q, want %q", c.id, c.l, got, c.want)
		}
	}
}

func TestEmptyQuestion(t *testing.T) {
	q := EmptyQuestion(identity{}, false, 1)
	if q.Heading != "Empty the Trash?" || q.Confirm != "_Empty" ||
		q.Body != "1 message will be deleted for good. Older messages that are only on the mail server stay there." {
		t.Errorf("trash: %+v", q)
	}
	q = EmptyQuestion(identity{}, true, 12)
	if q.Heading != "Empty the Junk Folder?" || !strings.HasPrefix(q.Body, "12 messages will be deleted for good.") {
		t.Errorf("junk: %+v", q)
	}
}

func TestProgressTexts(t *testing.T) {
	tr := identity{}
	if MovingText(tr, 1) != "Moving the message" || MovingText(tr, 3) != "Moving 3 messages" {
		t.Errorf("moving: %q %q", MovingText(tr, 1), MovingText(tr, 3))
	}
	if MarkingReadText(tr) != "Marking the folder as read" {
		t.Error(MarkingReadText(tr))
	}
	if EmptyingText(tr, false) != "Emptying the Trash" || EmptyingText(tr, true) != "Emptying the Junk folder" {
		t.Error(EmptyingText(tr, false), EmptyingText(tr, true))
	}
}
