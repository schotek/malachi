// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package contextmenu is what the right-click menus of the message list
// and of the sidebar offer, in all three clients: which items, in which
// groups, and which of them are enabled. A right click on a message
// selects it first, so the message menu acts on the selection through the
// window's own actions; the sidebar's menu acts on the folder or account
// clicked and leaves the selection alone. Pure logic, no widgets: each
// client maps the item ids to its actions and builds its menu from the
// groups (a separator between two). Texts are in text.go. Ported 1:1 to
// macOS (MalachiCore/ContextMenu) and Windows (Malachi.Core/ContextMenu).
package contextmenu

import (
	"slices"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/crm"
)

// ItemID names an item of a context menu.
type ItemID string

// The items of the message menu.
const (
	// OpenMessage opens the message in a window of its own, as a double
	// click does; EditDraft is the same for a message of the Drafts
	// folder, which opens in the compose window.
	OpenMessage ItemID = "open"
	EditDraft   ItemID = "edit"
	// Reply is Comment on an issue tracker's account (Labels.Comment).
	Reply    ItemID = "reply"
	ReplyAll ItemID = "replyAll"
	Forward  ItemID = "forward"
	// MarkRead and MarkUnread appear only when they apply; a conversation
	// with read and unread members has both.
	MarkRead   ItemID = "markRead"
	MarkUnread ItemID = "markUnread"
	// ToggleFlag is Star or Unstar (Labels.Flagged).
	ToggleFlag ItemID = "toggleFlag"
	// ChangeStatus is the submenu of an issue's transitions.
	ChangeStatus ItemID = "changeStatus"
	Archive      ItemID = "archive"
	// MoveTo is the submenu of the account's folders (MoveTargets).
	MoveTo ItemID = "moveTo"
	Junk   ItemID = "junk"
	// Trash is Move to Trash, or Cancel Sending in the outbox
	// (Labels.Outbox).
	Trash ItemID = "trash"
	// Assistant is the submenu of the Assistant's actions on the message.
	Assistant ItemID = "assistant"
	// CrmSave opens the Save to eWay-CRM dialog over the selection
	// (ui/internal/crm Dialog); several rows save together.
	CrmSave ItemID = "crmSave"
	// CrmOpen, CrmEditLinks and CrmRemove replace CrmSave for one saved
	// message: crm.SavedMenu's items (crm.MenuOpen, crm.MenuEditLinks,
	// crm.MenuRemove), Remove after crm.RemoveQuestion.
	CrmOpen      ItemID = "crmOpen"
	CrmEditLinks ItemID = "crmEditLinks"
	CrmRemove    ItemID = "crmRemove"
)

// The items of the sidebar's menus.
const (
	// CheckMail synchronises the folder clicked, or the whole account
	// from its heading.
	CheckMail ItemID = "checkMail"
	// MarkAllRead is folder.markRead.
	MarkAllRead ItemID = "markAllRead"
	// EmptyFolder is folder.empty: Empty Trash or Empty Junk
	// (Labels.Junk), always after a confirmation (EmptyQuestion).
	EmptyFolder ItemID = "empty"
	// Favourite is Add to or Remove from Favourites (Labels.Favourite).
	Favourite ItemID = "favourite"
	// Fold is Expand or Collapse (Labels.Collapsed).
	Fold ItemID = "fold"
)

// Item is one entry of a menu.
type Item struct {
	ID      ItemID
	Enabled bool
}

// MessageState is what the window knows of the message row clicked (by
// then the selected one; a folded conversation stands for its members in
// the folder, as for the window's other actions), or of the selection of
// several rows it belongs to (Count; ui/internal/selection Combine).
type MessageState struct {
	// Count is how many rows are selected (0 and 1: one).
	Count int
	// Open: Open in New Window can open them (several: at most
	// selection.MaxOpen); one row always can.
	Open bool
	// Draft: a message of the Drafts folder, which opens in the compose
	// window.
	Draft bool
	// Outbox: a queued message (Trash cancels its sending).
	Outbox bool
	// What the account offers at all (capabilities.Supported, the outbox
	// always offering Trash): an item it does not offer is left out.
	OfferReply, OfferReplyAll, OfferForward bool
	OfferArchive, OfferMove, OfferJunk      bool
	OfferTrash                              bool
	// What can be done now (the window's action rules): an offered item
	// that cannot is disabled.
	Reply, ReplyAll, Forward bool
	MarkRead, MarkUnread     bool
	ToggleFlag               bool
	Archive, Move, Junk      bool
	Trash                    bool
	// ChangeStatus: the message is an issue's and its account can change
	// the status.
	ChangeStatus bool
	// Assistant: the Assistant is shown (its submenu says what can run).
	Assistant bool
	// OfferCrm, Crm and CrmSaved are crm.OfferFor of the selected rows
	// (Shown, Enabled, Saved; SetCrm): the eWay-CRM items are left out
	// unless offered, Save is disabled unless Crm, and one saved message
	// has the saved-state items instead of Save.
	OfferCrm, Crm, CrmSaved bool
}

// SetCrm fills the eWay-CRM fields from crm.OfferFor of the selected rows.
func (s *MessageState) SetCrm(o crm.Offer) {
	s.OfferCrm, s.Crm, s.CrmSaved = o.Shown, o.Enabled, o.Saved
}

// MessageItems is the message menu in its groups: opening; answering;
// eWay-CRM; read, star and status; where the message goes; the
// Assistant. Empty groups are left out. For several rows Open opens each
// (Labels.Count), Forward forwards them in one message, Save to eWay-CRM
// saves them together (Labels.Count), and Reply, Reply All, Change Status
// and the saved-state eWay-CRM items, which are for one message, are not
// offered.
func MessageItems(s MessageState) [][]Item {
	several := s.Count > 1
	open := Item{ID: OpenMessage, Enabled: !several || s.Open}
	if s.Draft && !several {
		open.ID = EditDraft
	}
	var answer []Item
	if s.OfferReply && !several {
		answer = append(answer, Item{Reply, s.Reply})
	}
	if s.OfferReplyAll && !several {
		answer = append(answer, Item{ReplyAll, s.ReplyAll})
	}
	if s.OfferForward {
		answer = append(answer, Item{Forward, s.Forward})
	}
	var crmItems []Item
	switch {
	case !s.OfferCrm:
	case s.CrmSaved && !several:
		crmItems = []Item{{CrmOpen, true}, {CrmEditLinks, true}, {CrmRemove, true}}
	default:
		crmItems = []Item{{CrmSave, s.Crm}}
	}
	var mark []Item
	if s.MarkRead {
		mark = append(mark, Item{MarkRead, true})
	}
	if s.MarkUnread {
		mark = append(mark, Item{MarkUnread, true})
	}
	mark = append(mark, Item{ToggleFlag, s.ToggleFlag})
	if s.ChangeStatus && !several {
		mark = append(mark, Item{ChangeStatus, true})
	}
	var move []Item
	if s.OfferArchive {
		move = append(move, Item{Archive, s.Archive})
	}
	if s.OfferMove {
		move = append(move, Item{MoveTo, s.Move})
	}
	if s.OfferJunk {
		move = append(move, Item{Junk, s.Junk})
	}
	if s.OfferTrash || s.Outbox {
		move = append(move, Item{Trash, s.Trash})
	}
	var assistant []Item
	if s.Assistant {
		assistant = []Item{{Assistant, true}}
	}
	return groups([]Item{open}, answer, crmItems, mark, move, assistant)
}

// FolderState is what the sidebar knows of the folder clicked.
type FolderState struct {
	// Selectable: the folder holds messages (not a mere container).
	Selectable bool
	Role       api.FolderRole
	// Unread and Total are the folder's own counts (folder.list), not the
	// badge, which adds a folded folder's descendants.
	Unread, Total int
	// CanDelete: the account has the delete capability.
	CanDelete bool
	// Foldable: the folder has subfolders to expand or collapse.
	Foldable bool
}

// FolderItems is a folder's menu: checking it; marking it read or
// emptying it; its place in Favourites and its folding. Empty groups are
// left out; nothing at all opens no menu.
func FolderItems(f FolderState) [][]Item {
	holds := f.Selectable && f.Role != api.RoleOutbox
	var check, act, place []Item
	if holds {
		check = append(check, Item{CheckMail, true})
		act = append(act, Item{MarkAllRead, f.Unread > 0})
	}
	if f.Selectable && (f.Role == api.RoleTrash || f.Role == api.RoleJunk) && f.CanDelete {
		act = append(act, Item{EmptyFolder, f.Total > 0})
	}
	if f.Selectable {
		place = append(place, Item{Favourite, true})
	}
	if f.Foldable {
		place = append(place, Item{Fold, true})
	}
	return groups(check, act, place)
}

// AccountItems is an account heading's menu: checking the account, and
// its folding when the heading folds.
func AccountItems(foldable bool) [][]Item {
	var fold []Item
	if foldable {
		fold = []Item{{Fold, true}}
	}
	return groups([]Item{{CheckMail, true}}, fold)
}

// groups keeps the non-empty groups.
func groups(gs ...[]Item) [][]Item {
	out := make([][]Item, 0, len(gs))
	for _, g := range gs {
		if len(g) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// Entry is a folder of the account in the sidebar's order (its tree with
// nothing folded and no Favourites), with its depth in the tree.
type Entry struct {
	Folder api.Folder
	Depth  int
}

// Target is an entry of the Move To submenu.
type Target struct {
	Folder api.Folder
	Depth  int
	// Enabled: a message can go there (a selectable folder other than
	// the current one); a container stays as a disabled heading over
	// its subfolders.
	Enabled bool
}

// MoveTargets is the Move To submenu of a message in folder current, from
// its account's folders in the sidebar's order: everything but the outbox,
// the Drafts, an \All folder (role all) and an issue tracker's views (and
// entries without a folder, such as headings); the current folder and the
// containers disabled, and a container left out when none of its
// subfolders is listed.
func MoveTargets(entries []Entry, current api.FolderID) []Target {
	var kept []Target
	for _, e := range entries {
		f := e.Folder
		if f.ID == "" || f.Virtual != "" {
			continue
		}
		switch f.Role {
		case api.RoleOutbox, api.RoleDrafts, api.RoleAll:
			continue
		}
		kept = append(kept, Target{Folder: f, Depth: e.Depth, Enabled: f.Selectable && f.ID != current})
	}
	// From the end: a container stays only when the next entry kept
	// after it is deeper, one of its subfolders.
	out := make([]Target, 0, len(kept))
	next := -1
	for i := len(kept) - 1; i >= 0; i-- {
		t := kept[i]
		if !t.Folder.Selectable && next <= t.Depth {
			continue
		}
		out = append(out, t)
		next = t.Depth
	}
	slices.Reverse(out)
	return out
}
