// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package selection

import (
	"fmt"
	"slices"
	"testing"

	"github.com/schotek/malachi/backend/pkg/api"
)

// mail is a selected mail message everything applies to, read and not
// starred.
var mail = Flags{On: true, Reply: true, ReplyAll: true, Forward: true, Star: true, ToggleFlag: true,
	Trash: true, Archive: true, Junk: true, Move: true, MarkUnread: true, LoadImages: true, TrustSender: true}

func TestCombineOneRowIsTheRow(t *testing.T) {
	if got := Combine(nil); got != (Flags{}) {
		t.Errorf("none: %+v", got)
	}
	one := mail
	one.ChangeStatus, one.Comment = true, true
	want := one
	want.Count, want.Open = 1, true
	if got := Combine([]Flags{one}); got != want {
		t.Errorf("one: %+v", got)
	}
}

func TestCombineSeveral(t *testing.T) {
	unread := mail
	unread.MarkUnread, unread.MarkRead = false, true
	starred := mail
	starred.Flagged = true
	inArchive := mail
	inArchive.Archive = false
	got := Combine([]Flags{mail, unread, starred, inArchive})
	want := Flags{Count: 4, On: true, Forward: true, Star: true, ToggleFlag: true, Trash: true, Archive: true,
		Junk: true, Move: true, MarkRead: true, MarkUnread: true, Open: true}
	if got != want {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if !FlagTarget(got) {
		t.Error("not every row is starred: the star stars them")
	}
	if all := Combine([]Flags{starred, starred}); all.Flagged != true || FlagTarget(all) {
		t.Errorf("every row starred: %+v", all)
	}
	// Archive only when some row can go; Trash and Move only when all can.
	noTrash := mail
	noTrash.Trash, noTrash.Move, noTrash.Archive = false, false, false
	if c := Combine([]Flags{inArchive, noTrash}); c.Archive || c.Trash || c.Move || !c.Junk {
		t.Errorf("archive/trash/move: %+v", c)
	}
}

func TestCombineCaps(t *testing.T) {
	rows := make([]Flags, MaxOpen+1)
	for i := range rows {
		rows[i] = mail
	}
	if c := Combine(rows); c.Open || !c.Forward {
		t.Errorf("%d rows: %+v", len(rows), c)
	}
	rows = make([]Flags, MaxForward+1)
	for i := range rows {
		rows[i] = mail
	}
	if c := Combine(rows); c.Open || c.Forward {
		t.Errorf("%d rows: %+v", len(rows), c)
	}
	if c := Combine([]Flags{mail, mail}); !c.Open || !c.Forward {
		t.Errorf("two rows: %+v", c)
	}
}

func TestCombineOutbox(t *testing.T) {
	queued := Flags{On: true, Outbox: true, Reply: true, Forward: true, Trash: true, LoadImages: true}
	want := Flags{Count: 2, On: true, Outbox: true, Trash: true, Open: true}
	if got := Combine([]Flags{queued, queued}); got != want {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestRestrict(t *testing.T) {
	rows := []Row[string]{{"a1", "a"}, {"b1", "b"}, {"a2", "a"}, {"b2", "b"}}
	if got := Restrict(rows, "b2"); !slices.Equal(got, []string{"b1", "b2"}) {
		t.Errorf("latest b2: %v", got)
	}
	if got := Restrict(rows, "a1"); !slices.Equal(got, []string{"a1", "a2"}) {
		t.Errorf("latest a1: %v", got)
	}
	if got := Restrict(rows, "zz"); len(got) != 4 {
		t.Errorf("unknown latest: %v", got)
	}
	if got := Restrict([]Row[string]{}, "x"); len(got) != 0 {
		t.Errorf("none: %v", got)
	}
}

func TestReconcile(t *testing.T) {
	// m2 and m3 are members of conversation t, folded away; m9 is gone.
	resolve := func(k string) (string, bool) {
		switch k {
		case "m2", "m3":
			return "t", true
		case "m9":
			return "", false
		}
		return k, true
	}
	neighbour := func() (string, bool) { return "n", true }
	if got := Reconcile([]string{"m1", "m2", "m9", "m3", "m4"}, resolve, neighbour); !slices.Equal(got, []string{"m1", "t", "m4"}) {
		t.Errorf("kept: %v", got)
	}
	if got := Reconcile([]string{"m9"}, resolve, neighbour); !slices.Equal(got, []string{"n"}) {
		t.Errorf("all gone: %v", got)
	}
	if got := Reconcile([]string{"m9"}, resolve, nil); len(got) != 0 {
		t.Errorf("all gone, no neighbour: %v", got)
	}
	if got := Reconcile([]string{"m9"}, resolve, func() (string, bool) { return "", false }); len(got) != 0 {
		t.Errorf("all gone, nothing left: %v", got)
	}
	if got := Reconcile(nil, resolve, neighbour); len(got) != 0 {
		t.Errorf("nothing selected: %v", got)
	}
}

type identity struct{}

func (identity) T(msgid string) string { return msgid }
func (identity) N(msgid, plural string, n int) string {
	if n == 1 {
		return msgid
	}
	return plural
}

func TestTexts(t *testing.T) {
	tr := identity{}
	for n, want := range map[int]string{1: "1 message selected", 5: "5 messages selected"} {
		if got := SelectedText(tr, n); got != want {
			t.Errorf("%d: %q", n, got)
		}
	}
	if got := CancelSendingQuestion(tr, 3); got != "Cancel sending 3 messages?" {
		t.Errorf("cancel: %q", got)
	}
	if MaxForward != api.MaxForwardMessages {
		t.Error(fmt.Sprint(MaxForward))
	}
}

// Save to eWay-CRM for several: every row savable, at most
// api.MaxCrmMessages, never the saved-state items; not in the outbox.
func TestCombineCrm(t *testing.T) {
	savable := mail
	savable.Crm, savable.CrmSaved = true, true
	if c := Combine([]Flags{savable}); !c.Crm || !c.CrmSaved {
		t.Errorf("one: %+v", c)
	}
	if c := Combine([]Flags{savable, savable}); !c.Crm || c.CrmSaved {
		t.Errorf("two: %+v", c)
	}
	if c := Combine([]Flags{savable, mail}); c.Crm {
		t.Errorf("one row not savable: %+v", c)
	}
	rows := make([]Flags, api.MaxCrmMessages+1)
	for i := range rows {
		rows[i] = savable
	}
	if c := Combine(rows); c.Crm {
		t.Errorf("%d rows: %+v", len(rows), c)
	}
	if c := Combine(rows[:api.MaxCrmMessages]); !c.Crm {
		t.Errorf("%d rows: %+v", api.MaxCrmMessages, c)
	}
	queued := savable
	queued.Outbox = true
	if c := Combine([]Flags{queued, queued}); c.Crm {
		t.Errorf("outbox: %+v", c)
	}
}
