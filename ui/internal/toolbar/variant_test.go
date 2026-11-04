// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package toolbar

import (
	"reflect"
	"testing"
)

var clients = map[string]Variant{"macOS": {}, "GTK": GTK, "Windows": Windows}

func TestClientDefaultsAreNormal(t *testing.T) {
	for name, v := range clients {
		for _, bar := range Bars {
			d := DefaultFor(v, bar)
			if got := NormalizeFor(v, bar, d.Tokens()); !got.Equal(d) {
				t.Errorf("%s %s: NormalizeFor(DefaultFor) = %v, want %v", name, bar, got, d)
			}
			if got := NormalizeFor(v, bar, nil); !got.Equal(d) {
				t.Errorf("%s %s: NormalizeFor(nil) = %v", name, bar, got)
			}
			for _, id := range d {
				if IsSpace(id) || IsSeparator(id) {
					continue
				}
				if _, ok := LookupFor(v, bar, id); !ok {
					t.Errorf("%s %s: default item %s is not in the client's catalog", name, bar, id)
				}
			}
			if got := StoredFor(v, bar, nil, d); len(got) != 0 {
				t.Errorf("%s %s: StoredFor(default) = %v, want empty", name, bar, got)
			}
		}
	}
}

func TestZeroVariantIsTheCatalog(t *testing.T) {
	for _, bar := range Bars {
		if !DefaultFor(Variant{}, bar).Equal(Default(bar)) {
			t.Errorf("%s: DefaultFor(zero) differs from Default", bar)
		}
		if !reflect.DeepEqual(CatalogFor(Variant{}, bar), Catalog(bar)) {
			t.Errorf("%s: CatalogFor(zero) differs from Catalog", bar)
		}
	}
	// StoredFor with the zero variant is Stored, whatever was stored.
	for _, l := range []Layout{Default(BarMail), ids("mode |list refresh |message reply junk"), nil} {
		for _, stored := range [][]string{nil, ids("mode |list filter |message star").Tokens()} {
			if got, want := StoredFor(Variant{}, BarMail, stored, l), Stored(BarMail, l); !reflect.DeepEqual(got, want) {
				t.Errorf("StoredFor(zero, %v, %v) = %v, want %v", stored, l, got, want)
			}
		}
	}
}

func TestGTKDefaultsAreTodaysHeaderBars(t *testing.T) {
	want := map[Bar]string{
		BarMail: "mode newMessage flexibleSpace |list refresh flexibleSpace search " +
			"|message reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions",
		BarBoardList: "mode boardStyle flexibleSpace |list boardTriage boardAccount " +
			"|detail boardDone boardRemind boardArchive boardTrash flexibleSpace boardReply",
		BarBoardFlow: "mode boardStyle flexibleSpace boardTriage boardAccount",
		BarCalendar: "mode |main toggleSidebar calendarToday calendarStep calendarView flexibleSpace " +
			"calendarSearch calendarNewEvent calendarAssistant",
	}
	for bar, w := range want {
		if got := DefaultFor(GTK, bar); !got.Equal(ids(w)) {
			t.Errorf("GTK %s default = %v", bar, got)
		}
	}
}

func TestWindowsDefaultsAreTodaysBars(t *testing.T) {
	want := map[Bar]string{
		BarMail: "mode newMessage |list refresh filter " +
			"|message reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions search",
		BarBoardList: "mode boardStyle flexibleSpace |list boardAccount boardTriage " +
			"|detail boardDone boardRemind boardArchive boardTrash flexibleSpace boardReply",
		BarBoardFlow: "mode boardStyle flexibleSpace boardAccount boardTriage",
		BarCalendar: "mode |main toggleSidebar calendarToday calendarStep calendarView flexibleSpace " +
			"calendarSearch calendarNewEvent calendarAssistant",
	}
	for bar, w := range want {
		if got := DefaultFor(Windows, bar); !got.Equal(ids(w)) {
			t.Errorf("Windows %s default = %v", bar, got)
		}
	}
}

func TestGTKOmitsAndExtras(t *testing.T) {
	for _, c := range []struct {
		bar Bar
		id  ID
	}{{BarMail, Filter}, {BarMail, ToggleSidebar}, {BarBoardList, ToggleSidebar}} {
		if _, ok := LookupFor(GTK, c.bar, c.id); ok {
			t.Errorf("GTK %s has %s", c.bar, c.id)
		}
		if CanInsertFor(GTK, c.bar, c.id, SectionList) {
			t.Errorf("GTK may insert %s into %s", c.id, c.bar)
		}
		for _, id := range AllowedFor(GTK, c.bar, Features{}) {
			if id == c.id {
				t.Errorf("GTK offers %s in %s", c.id, c.bar)
			}
		}
	}
	if _, ok := LookupFor(GTK, BarCalendar, ToggleSidebar); !ok {
		t.Errorf("GTK's calendar lacks its sidebar toggle")
	}
	if !CanInsertFor(GTK, BarMail, Search, SectionList) || !CanInsertFor(GTK, BarMail, Search, SectionMessage) {
		t.Errorf("GTK's search may stand in the list and the message sections")
	}
	if CanInsert(BarMail, Search, SectionList) {
		t.Errorf("macOS's search may not stand in the list section")
	}
	it, _ := LookupFor(GTK, BarMail, Search)
	if it.Home() != SectionMessage {
		t.Errorf("the extra section changed the home: %s", it.Home())
	}
	// macOS's arrangement read on GTK: its filter and sidebar toggle go.
	got := NormalizeFor(GTK, BarMail, Default(BarMail).Tokens())
	want := ids("mode flexibleSpace |list newMessage contacts refresh flexibleSpace " +
		"|message reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions search")
	if !got.Equal(want) {
		t.Errorf("macOS default on GTK = %v", got)
	}
	// And GTK's read on macOS: the search goes home.
	got = Normalize(BarMail, DefaultFor(GTK, BarMail).Tokens())
	if got[len(got)-1] != Search {
		t.Errorf("GTK default on macOS = %v", got)
	}
}

func TestPresentForLeavesOutOmitted(t *testing.T) {
	l := ids("mode |list filter toggleSidebar refresh |message reply crm")
	got := PresentFor(GTK, BarMail, l, Features{})
	want := []Shown{{ID: Mode}, {ID: Separator(SectionList)}, {ID: Refresh}, {ID: Separator(SectionMessage)}, {ID: Reply}, {ID: CRM, Hidden: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PresentFor(GTK) = %v", got)
	}
	if got := PresentFor(Windows, BarMail, l, Features{}); len(got) != len(l) {
		t.Errorf("PresentFor(Windows) left something out: %v", got)
	}
}

func TestMissingActionsFor(t *testing.T) {
	l := DefaultFor(GTK, BarMail)
	if got := MissingActionsFor(GTK, BarMail, l, Features{Have: map[Feature]bool{FeatureCRM: true}}); len(got) != 0 {
		t.Errorf("GTK's default misses %v", got)
	}
	l = ids("mode |list |message reply")
	got := MissingActionsFor(Windows, BarMail, l, Features{})
	want := []ID{ReplyAll, Forward, Archive, Trash, Star}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MissingActionsFor(Windows) = %v, want %v", got, want)
	}
}

func TestStoredForKeepsWhatTheClientLacks(t *testing.T) {
	cases := []struct {
		name   string
		stored string
		edited string
		want   string
	}{
		{
			"a first edit on GTK keeps macOS's filter and sidebar toggle where the default had them",
			"",
			"mode newMessage flexibleSpace |list refresh flexibleSpace search |message reply archive",
			"mode newMessage flexibleSpace |list toggleSidebar filter refresh flexibleSpace search |message reply archive",
		},
		{
			"the filter follows its neighbour",
			"mode |list refresh filter contacts |message reply",
			"mode |list contacts refresh |message reply",
			"mode |list contacts refresh filter |message reply",
		},
		{
			"an anchor moved to another section: the nearest one before it in the section",
			"mode |list refresh filter |message reply",
			"mode refresh |list |message reply",
			"mode refresh |list filter |message reply",
		},
		{
			"an anchor the user removed: the nearest one before it",
			"mode newMessage filter |list refresh |message reply",
			"mode |list refresh |message reply",
			"mode filter |list refresh |message reply",
		},
		{
			"two omitted items keep their order",
			"mode |list refresh toggleSidebar filter |message",
			"mode |list refresh |message reply",
			"mode |list refresh toggleSidebar filter |message reply",
		},
		{
			"items the stored list lacks are not added",
			"mode |list refresh |message reply",
			"mode |list refresh |message forward",
			"mode |list refresh |message forward",
		},
		{
			"GTK's default over a stored list without the omitted items is not empty",
			"mode |list refresh |message reply",
			"mode newMessage flexibleSpace |list refresh flexibleSpace search |message reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions",
			"mode newMessage flexibleSpace |list refresh flexibleSpace search |message reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions",
		},
		{
			"GTK's default over a stored list with the default's omitted items is empty",
			"mode filter |list toggleSidebar |message reply",
			"mode newMessage flexibleSpace |list refresh flexibleSpace search |message reply replyAll forward flexibleSpace archive trash space crm space star assistant moreActions",
			"",
		},
		{
			"unknown and duplicate stored tokens go",
			"mode |list filter bogus filter |message",
			"mode |list refresh |message",
			"mode |list filter refresh |message",
		},
	}
	for _, c := range cases {
		got := StoredFor(GTK, BarMail, ids(c.stored).Tokens(), ids(c.edited))
		if want := ids(c.want).Tokens(); !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("%s:\n got %v\nwant %v", c.name, got, want)
		}
		// GTK reads back what it edited.
		if back := NormalizeFor(GTK, BarMail, got); !back.Equal(NormalizeFor(GTK, BarMail, ids(c.edited).Tokens())) {
			t.Errorf("%s: GTK reads back %v", c.name, back)
		}
		// The client's own save again changes nothing.
		if again := StoredFor(GTK, BarMail, got, ids(c.edited)); !reflect.DeepEqual(again, got) {
			t.Errorf("%s: a second save gives %v", c.name, again)
		}
	}
}

func TestStoredForRoundTripsBetweenClients(t *testing.T) {
	// macOS arranges, GTK edits, macOS reads: its filter and sidebar
	// toggle are where they were; GTK's search in the list section goes
	// to its home on macOS.
	mac := ids("mode |list toggleSidebar filter refresh |message reply archive search")
	stored := Stored(BarMail, mac)
	gtk := NormalizeFor(GTK, BarMail, stored)
	if want := ids("mode |list refresh |message reply archive search"); !gtk.Equal(want) {
		t.Fatalf("GTK reads %v", gtk)
	}
	edited := ids("mode |list refresh search |message archive reply")
	stored = StoredFor(GTK, BarMail, stored, edited)
	if want := ids("mode |list toggleSidebar filter refresh search |message archive reply").Tokens(); !reflect.DeepEqual(stored, want) {
		t.Fatalf("GTK stores %v", stored)
	}
	if got, want := Normalize(BarMail, stored), ids("mode |list toggleSidebar filter refresh |message archive reply search"); !got.Equal(want) {
		t.Errorf("macOS reads %v", got)
	}
	// Windows keeps the search where GTK put it (its extra section), and
	// a Windows save keeps the omitted items as they are (it omits none).
	if got := NormalizeFor(Windows, BarMail, stored); !got.Equal(ids2(stored...)) {
		t.Errorf("Windows reads %v", got)
	}
}

func TestStoredForOnEveryBar(t *testing.T) {
	// A GTK edit of the Board List keeps a sidebar toggle stored there.
	stored := ids("mode toggleSidebar |list boardStyle |detail boardDone").Tokens()
	got := StoredFor(GTK, BarBoardList, stored, ids("mode |list boardStyle boardAccount |detail boardDone"))
	want := ids("mode toggleSidebar |list boardStyle boardAccount |detail boardDone").Tokens()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("board-list = %v", got)
	}
	// Bars without omitted items store the edit as it is.
	for _, bar := range []Bar{BarBoardFlow, BarCalendar} {
		l := ids("mode")
		if bar == BarCalendar {
			l = ids("mode |main calendarToday")
		}
		if got := StoredFor(GTK, bar, nil, l); !reflect.DeepEqual(got, l.Tokens()) {
			t.Errorf("%s = %v", bar, got)
		}
	}
}

func TestStoredForPathological(t *testing.T) {
	// Junk stored: separators only, a token too long, a thousand spaces.
	junk := []string{"|message", "|list", "|bogus", "", "|", string(make([]byte, 5000))}
	for i := 0; i < 1000; i++ {
		junk = append(junk, "filter", "space")
	}
	got := StoredFor(GTK, BarMail, junk, DefaultFor(GTK, BarMail))
	back := NormalizeFor(GTK, BarMail, got)
	if !back.Equal(DefaultFor(GTK, BarMail)) {
		t.Errorf("GTK reads back %v", back)
	}
	n := 0
	for _, tok := range got {
		if tok == string(Filter) {
			n++
		}
	}
	if n > 1 {
		t.Errorf("filter merged %d times", n)
	}
	// An edited layout with omitted and unknown items: they are not the
	// client's to store.
	got = StoredFor(GTK, BarMail, nil, ids("mode filter bogus |list refresh |message"))
	if want := ids("mode |list toggleSidebar filter refresh |message").Tokens(); !reflect.DeepEqual(got, want) {
		t.Errorf("edited with omitted items = %v", got)
	}
}

func ids2(s ...string) Layout {
	out := make(Layout, len(s))
	for i, x := range s {
		out[i] = ID(x)
	}
	return out
}
