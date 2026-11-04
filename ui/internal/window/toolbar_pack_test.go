// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"reflect"
	"strings"
	"testing"

	"github.com/schotek/malachi/ui/internal/toolbar"
)

func tbIDs(s string) []toolbar.ID {
	var out []toolbar.ID
	for _, f := range strings.Fields(s) {
		out = append(out, toolbar.ID(f))
	}
	return out
}

func tbShown(s string) []toolbar.Shown {
	var out []toolbar.Shown
	for _, id := range tbIDs(s) {
		out = append(out, toolbar.Shown{ID: id})
	}
	return out
}

func crmSpaced(id toolbar.ID) bool { return id == toolbar.CRM }

// The header bars' plans of an untouched toolbar are today's header bars
// of window.blp, board_page.blp and calendar_page.blp, item by item in
// screen order (T12): an untouched toolbar must not change.
func TestToolbarPackDefaultsAreTodaysHeaderBars(t *testing.T) {
	all := toolbar.Features{Have: map[toolbar.Feature]bool{
		toolbar.FeatureCRM: true, toolbar.FeatureAssistant: true, toolbar.FeatureTriage: true,
	}}
	cases := []struct {
		bar        toolbar.Bar
		secs       []toolbar.Section
		start, end string
		gaps       string
	}{
		{toolbar.BarMail, []toolbar.Section{toolbar.SectionSidebar}, "mode newMessage", "", ""},
		{toolbar.BarMail, []toolbar.Section{toolbar.SectionList}, "refresh", "search", ""},
		{toolbar.BarMail, []toolbar.Section{toolbar.SectionMessage}, "reply replyAll forward",
			"archive trash crm star assistant moreActions", "trash"},
		{toolbar.BarBoardList, []toolbar.Section{toolbar.SectionSidebar, toolbar.SectionList}, "mode boardStyle", "boardTriage boardAccount", ""},
		{toolbar.BarBoardList, []toolbar.Section{toolbar.SectionDetail}, "boardDone boardRemind boardArchive boardTrash", "boardReply", ""},
		{toolbar.BarBoardFlow, []toolbar.Section{toolbar.SectionMain}, "mode boardStyle", "boardTriage boardAccount", ""},
		{toolbar.BarCalendar, []toolbar.Section{toolbar.SectionSidebar, toolbar.SectionMain},
			"mode toggleSidebar calendarToday calendarStep calendarView", "calendarSearch calendarNewEvent calendarAssistant", ""},
	}
	for _, c := range cases {
		for _, f := range []toolbar.Features{all, {}} {
			l := toolbar.NormalizeFor(gtkVariant, c.bar, nil)
			p := packHeader(sectionEntries(c.bar, toolbar.PresentFor(gtkVariant, c.bar, l, f), c.secs), crmSpaced)
			if !reflect.DeepEqual(p.Start, tbIDs(c.start)) || !reflect.DeepEqual(p.End, tbIDs(c.end)) {
				t.Errorf("%s %v: start %v end %v, want %q | %q", c.bar, c.secs, p.Start, p.End, c.start, c.end)
			}
			var gaps []toolbar.ID
			for _, id := range append(append([]toolbar.ID(nil), p.Start...), p.End...) {
				if p.Gap[id] {
					gaps = append(gaps, id)
				}
			}
			if !reflect.DeepEqual(gaps, tbIDs(c.gaps)) {
				t.Errorf("%s %v: gaps after %v, want %q", c.bar, c.secs, gaps, c.gaps)
			}
		}
	}
}

func TestPackHeader(t *testing.T) {
	cases := []struct {
		name, in, start, end, gaps string
	}{
		{"no flexible space: all at the start", "a b space c", "a b c", "", "b"},
		{"the last flexible space splits", "a flexibleSpace b flexibleSpace c d", "a b", "c d", ""},
		{"a space with nothing before it on its side", "space a flexibleSpace space b", "a", "b", ""},
		{"two spaces are one gap", "a space space b", "a b", "", "a"},
		{"the CRM slot spaces itself", "a space crm space b", "a crm b", "", "a"},
		{"a separator is nothing", "a |list b flexibleSpace c", "a b", "c", ""},
		{"empty", "", "", "", ""},
		{"only a flexible space", "flexibleSpace", "", "", ""},
	}
	for _, c := range cases {
		p := packHeader(tbShown(c.in), crmSpaced)
		if !reflect.DeepEqual(p.Start, tbIDs(c.start)) || !reflect.DeepEqual(p.End, tbIDs(c.end)) {
			t.Errorf("%s: start %v end %v, want %q | %q", c.name, p.Start, p.End, c.start, c.end)
		}
		var gaps []toolbar.ID
		for _, id := range append(append([]toolbar.ID(nil), p.Start...), p.End...) {
			if p.Gap[id] {
				gaps = append(gaps, id)
			}
		}
		if !reflect.DeepEqual(gaps, tbIDs(c.gaps)) {
			t.Errorf("%s: gaps %v, want %q", c.name, gaps, c.gaps)
		}
	}
	// Hidden entries are packed all the same (T7).
	in := []toolbar.Shown{{ID: toolbar.Star}, {ID: toolbar.CRM, Hidden: true}}
	if p := packHeader(in, crmSpaced); len(p.Start) != 2 {
		t.Errorf("a hidden entry was not packed: %v", p.Start)
	}
}

// A custom arrangement moves items between the Mail's header bars.
func TestToolbarPackCustomMail(t *testing.T) {
	l := toolbar.NormalizeFor(gtkVariant, toolbar.BarMail, []string{
		"mode", "contacts", "flexibleSpace", "newMessage",
		"|list", "search", "refresh",
		"|message", "markRead", "junk", "space", "moveTo", "flexibleSpace", "undo", "moreActions",
	})
	shown := toolbar.PresentFor(gtkVariant, toolbar.BarMail, l, toolbar.Features{})
	want := []struct {
		sec        toolbar.Section
		start, end string
	}{
		{toolbar.SectionSidebar, "mode contacts", "newMessage"},
		{toolbar.SectionList, "search refresh", ""},
		{toolbar.SectionMessage, "markRead junk moveTo", "undo moreActions"},
	}
	for _, w := range want {
		p := packHeader(sectionEntries(toolbar.BarMail, shown, []toolbar.Section{w.sec}), crmSpaced)
		if !reflect.DeepEqual(p.Start, tbIDs(w.start)) || !reflect.DeepEqual(p.End, tbIDs(w.end)) {
			t.Errorf("%s: start %v end %v, want %q | %q", w.sec, p.Start, p.End, w.start, w.end)
		}
		if w.sec == toolbar.SectionMessage && !p.Gap[toolbar.Junk] {
			t.Error("the space after Junk gave no gap")
		}
	}
}

func TestMailColumns(t *testing.T) {
	cases := []struct {
		w, panel     int
		outer, inner bool
		f, l, m      int
	}{
		{1200, 0, false, false, 240, 364, 596},
		{2000, 0, false, false, 320, 460, 1220},
		{1000, 0, false, false, 200, 304, 496},
		{800, 0, true, false, 800, 304, 496},
		{500, 0, true, true, 500, 500, 500},
		{1400, 392, false, false, 201, 306, 501},
		{100, 400, true, true, 0, 0, 0},
	}
	for _, c := range cases {
		f, l, m := mailColumns(c.w, c.panel, c.outer, c.inner)
		if f != c.f || l != c.l || m != c.m {
			t.Errorf("mailColumns(%d, %d, %v, %v) = %d %d %d, want %d %d %d", c.w, c.panel, c.outer, c.inner, f, l, m, c.f, c.l, c.m)
		}
	}
	if got := assistantPanelWidth(1400); got != 392 {
		t.Errorf("assistantPanelWidth(1400) = %d", got)
	}
}

func TestMoreActionItems(t *testing.T) {
	all := toolbar.Features{Have: map[toolbar.Feature]bool{toolbar.FeatureCRM: true, toolbar.FeatureAssistant: true}}
	// The untouched toolbar shows every More Actions action: nothing added.
	def := toolbar.NormalizeFor(gtkVariant, toolbar.BarMail, nil)
	if got := moreActionItems(def, all, nil); len(got) != 0 {
		t.Errorf("the default toolbar adds %v", got)
	}
	// Reply, Archive and eWay-CRM taken off: Reply and Archive come back,
	// eWay-CRM has its own section; the overflow follows, each once, and an
	// item without an action (the Assistant) is not offered.
	l := toolbar.NormalizeFor(gtkVariant, toolbar.BarMail, []string{
		"mode", "newMessage", "|list", "refresh", "search",
		"|message", "replyAll", "forward", "trash", "star", "assistant", "moreActions", "markRead", "moveTo",
	})
	got := moreActionItems(l, all, tbIDs("forward reply markRead assistant moveTo"))
	if want := tbIDs("reply archive forward markRead moveTo"); !reflect.DeepEqual(got, want) {
		t.Errorf("moreActionItems = %v, want %v", got, want)
	}
}
