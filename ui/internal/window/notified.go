// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"errors"

	"github.com/schotek/malachi/backend/pkg/api"
)

// notifiedMax bounds the desktop notifications the window remembers. The
// oldest one is withdrawn when a newer one pushes it out: once forgotten
// it could never be withdrawn again.
const notifiedMax = 50

// notificationID is the GNotification id of message id's notification.
func notificationID(id api.MessageID) string { return "message-" + string(id) }

// notifiedSet holds the messages the window sent a desktop notification
// for that may still show in the shell's message tray, oldest first, each
// with the folder it was notified in. Only messages in it are ever
// withdrawn (notify.go withdrawNotifications), so the window never
// withdraws blindly.
type notifiedSet struct {
	entries []notifiedEntry
}

// notifiedEntry is one notified message and the folder it arrived in.
type notifiedEntry struct {
	id  api.MessageID
	key folderKey
}

// add records the notification of message id in folder k as the newest; a
// message notified twice keeps one entry. It returns the messages pushed
// out beyond notifiedMax, oldest first, for the caller to withdraw.
func (s *notifiedSet) add(id api.MessageID, k folderKey) (evicted []api.MessageID) {
	s.drop(func(e notifiedEntry) bool { return e.id == id })
	s.entries = append(s.entries, notifiedEntry{id: id, key: k})
	if over := len(s.entries) - notifiedMax; over > 0 {
		for _, e := range s.entries[:over] {
			evicted = append(evicted, e.id)
		}
		s.entries = append(s.entries[:0], s.entries[over:]...)
	}
	return evicted
}

// remove forgets the given messages and returns those it held.
func (s *notifiedSet) remove(ids []api.MessageID) []api.MessageID {
	if len(s.entries) == 0 {
		return nil
	}
	want := make(map[api.MessageID]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	return s.drop(func(e notifiedEntry) bool { return want[e.id] })
}

// removeFolder forgets the messages notified in folder k and returns them.
func (s *notifiedSet) removeFolder(k folderKey) []api.MessageID {
	return s.drop(func(e notifiedEntry) bool { return e.key == k })
}

// removeAccountsExcept forgets the messages of every account not in keep
// and returns them.
func (s *notifiedSet) removeAccountsExcept(keep map[api.AccountID]bool) []api.MessageID {
	return s.drop(func(e notifiedEntry) bool { return !keep[e.key.Account] })
}

// ofAccount is a copy of the entries of account acc, oldest first.
func (s *notifiedSet) ofAccount(acc api.AccountID) []notifiedEntry {
	var out []notifiedEntry
	for _, e := range s.entries {
		if e.key.Account == acc {
			out = append(out, e)
		}
	}
	return out
}

// drop removes the entries match selects and returns their messages, in
// order.
func (s *notifiedSet) drop(match func(notifiedEntry) bool) (removed []api.MessageID) {
	kept := s.entries[:0]
	for _, e := range s.entries {
		if match(e) {
			removed = append(removed, e.id)
			continue
		}
		kept = append(kept, e)
	}
	s.entries = kept
	return removed
}

// notificationOutdated reads message.get's answer about a notified message
// (m on success, err otherwise): the notification is outdated when the
// message was read or has left the folder it was notified in, or when the
// daemon no longer has the message or its account. ok is false when the
// answer tells nothing (no connection, a timeout, a storage error); the
// notification then stays.
func notificationOutdated(e notifiedEntry, m api.MessageSummary, err error) (outdated, ok bool) {
	if err == nil {
		moved := m.FolderID != "" && m.FolderID != e.key.Folder
		return moved || hasFlag(m.Flags, api.FlagSeen), true
	}
	var ae *api.Error
	if errors.As(err, &ae) && (ae.Code == api.CodeMessageNotFound || ae.Code == api.CodeAccountNotFound) {
		return true, true
	}
	return false, false
}
