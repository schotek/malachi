// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/client"
)

var (
	inboxA   = folderKey{Account: "a", Folder: "inbox"}
	archiveA = folderKey{Account: "a", Folder: "archive"}
	inboxB   = folderKey{Account: "b", Folder: "inbox"}
)

func msgIDs(s ...string) []api.MessageID {
	out := make([]api.MessageID, 0, len(s))
	for _, id := range s {
		out = append(out, api.MessageID(id))
	}
	return out
}

func setIDs(s *notifiedSet) []api.MessageID {
	var out []api.MessageID
	for _, e := range s.entries {
		out = append(out, e.id)
	}
	return out
}

func TestNotifiedSetAddRemove(t *testing.T) {
	var s notifiedSet
	s.add("m1", inboxA)
	s.add("m2", archiveA)
	s.add("m3", inboxB)
	// Delivered twice: one entry, now the newest, in the folder of the
	// latest notification.
	if ev := s.add("m1", inboxA); ev != nil {
		t.Errorf("re-add evicted %v", ev)
	}
	if got, want := setIDs(&s), msgIDs("m2", "m3", "m1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}

	// Only what the set holds comes back: nothing is withdrawn blindly.
	if got, want := s.remove(msgIDs("m9", "m3", "m3")), msgIDs("m3"); !reflect.DeepEqual(got, want) {
		t.Errorf("remove = %v, want %v", got, want)
	}
	if got := s.remove(msgIDs("m3")); got != nil {
		t.Errorf("second remove = %v, want nothing", got)
	}
	if got := s.remove(nil); got != nil {
		t.Errorf("remove(nil) = %v", got)
	}
	if got, want := setIDs(&s), msgIDs("m2", "m1"); !reflect.DeepEqual(got, want) {
		t.Errorf("left = %v, want %v", got, want)
	}

	var empty notifiedSet
	if got := empty.remove(msgIDs("m1")); got != nil {
		t.Errorf("empty remove = %v", got)
	}
}

func TestNotifiedSetBounded(t *testing.T) {
	var s notifiedSet
	for i := range notifiedMax {
		if ev := s.add(api.MessageID(fmt.Sprint("m", i)), inboxA); ev != nil {
			t.Fatalf("add %d evicted %v below the cap", i, ev)
		}
	}
	// The oldest goes, and the caller is told to withdraw it.
	if got, want := s.add("new", inboxA), msgIDs("m0"); !reflect.DeepEqual(got, want) {
		t.Fatalf("evicted = %v, want %v", got, want)
	}
	if len(s.entries) != notifiedMax {
		t.Fatalf("len = %d, want %d", len(s.entries), notifiedMax)
	}
	if s.entries[0].id != "m1" || s.entries[notifiedMax-1].id != "new" {
		t.Errorf("ends = %s … %s", s.entries[0].id, s.entries[notifiedMax-1].id)
	}
	// Re-adding a held message evicts nothing.
	if ev := s.add("m1", inboxA); ev != nil {
		t.Errorf("re-add at the cap evicted %v", ev)
	}
}

func TestNotifiedSetFolderAndAccount(t *testing.T) {
	var s notifiedSet
	s.add("m1", inboxA)
	s.add("m2", archiveA)
	s.add("m3", inboxA)
	s.add("m4", inboxB)

	got := s.ofAccount("a")
	if len(got) != 3 || got[0].id != "m1" || got[1].key != archiveA || got[2].id != "m3" {
		t.Errorf("ofAccount(a) = %v", got)
	}
	// A copy: checking the account while the set changes is safe.
	got[0].id = "changed"
	if s.entries[0].id != "m1" {
		t.Error("ofAccount shares the set's storage")
	}
	if s.ofAccount("zzz") != nil {
		t.Error("ofAccount of an unknown account is not empty")
	}

	// Viewing the inbox withdraws the inbox's notifications, not the
	// archive's or the other account's inbox.
	if got, want := s.removeFolder(inboxA), msgIDs("m1", "m3"); !reflect.DeepEqual(got, want) {
		t.Errorf("removeFolder = %v, want %v", got, want)
	}
	if got := s.removeFolder(inboxA); got != nil {
		t.Errorf("second removeFolder = %v", got)
	}

	// Account b was removed or paused.
	if got, want := s.removeAccountsExcept(map[api.AccountID]bool{"a": true}), msgIDs("m4"); !reflect.DeepEqual(got, want) {
		t.Errorf("removeAccountsExcept = %v, want %v", got, want)
	}
	if got, want := s.removeAccountsExcept(nil), msgIDs("m2"); !reflect.DeepEqual(got, want) {
		t.Errorf("no accounts left: %v, want %v", got, want)
	}
	if len(s.entries) != 0 {
		t.Errorf("left %v", s.entries)
	}
}

func TestNotificationOutdated(t *testing.T) {
	e := notifiedEntry{id: "m1", key: inboxA}
	unread := api.MessageSummary{ID: "m1", AccountID: "a", FolderID: "inbox", Flags: []api.Flag{api.FlagFlagged}}
	read := unread
	read.Flags = []api.Flag{api.FlagFlagged, api.FlagSeen}
	moved := unread
	moved.FolderID = "archive"
	noFolder := unread
	noFolder.FolderID = "" // an older daemon: nothing to compare

	cases := []struct {
		name         string
		m            api.MessageSummary
		err          error
		outdated, ok bool
	}{
		{"still unread in its folder", unread, nil, false, true},
		{"read elsewhere", read, nil, true, true},
		{"moved by another client", moved, nil, true, true},
		{"no folder in the answer", noFolder, nil, false, true},
		{"deleted", api.MessageSummary{}, &api.Error{Code: api.CodeMessageNotFound}, true, true},
		{"account removed", api.MessageSummary{}, &api.Error{Code: api.CodeAccountNotFound}, true, true},
		{"wrapped not found", api.MessageSummary{}, fmt.Errorf("call: %w", &api.Error{Code: api.CodeMessageNotFound}), true, true},
		{"storage error", api.MessageSummary{}, &api.Error{Code: api.CodeStorageError}, false, false},
		{"disconnected", api.MessageSummary{}, client.ErrDisconnected, false, false},
		{"timeout", api.MessageSummary{}, context.DeadlineExceeded, false, false},
		{"other", api.MessageSummary{}, errors.New("boom"), false, false},
	}
	for _, c := range cases {
		outdated, ok := notificationOutdated(e, c.m, c.err)
		if outdated != c.outdated || ok != c.ok {
			t.Errorf("%s: outdated=%v ok=%v, want %v %v", c.name, outdated, ok, c.outdated, c.ok)
		}
	}
}

func TestNotificationID(t *testing.T) {
	if got := notificationID("m_42"); got != "message-m_42" {
		t.Errorf("notificationID = %q", got)
	}
}
