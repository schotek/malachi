// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"log/slog"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// Run in a dedicated test process with a display, for example Broadway:
// MALACHI_GTK_SMOKE=1 GDK_BACKEND=broadway BROADWAY_DISPLAY=:5 \
// go test ./internal/window -run '^TestBoardGTKSmoke$' -count=1
// Only invented samples are used: no settings, daemon, account or Claude.
func TestBoardGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the widget smoke test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	w := &Window{
		ApplicationWindow: data.Builder("window.ui").GetObject("main_window").Cast().(*adw.ApplicationWindow),
		log:               slog.Default(), mode: board.ModeBoard,
		actions:           make(map[string]*gio.SimpleAction),
		modeSwitchBoardOn: true,
		toasts:            adw.NewToastOverlay(),
	}
	defer w.Destroy()
	w.registerBoardActions()
	now := time.Now()
	src := board.NewDummySource(true, now, time.Local, i18n.Tr)
	p := &boardPage{w: w, src: src}
	p.ctl = board.NewController(src, board.ControllerOptions{
		Env: board.Env{Tr: i18n.Tr, Dates: boardEnv{}, Loc: time.Local},
		Now: func() time.Time { return now },
	})
	b := data.Builder("board_page.ui")
	p.bind(b)
	p.bindColumns(b)
	p.bindToday(b)
	p.bindPanel(b)
	w.boardPage = p
	p.wire()
	p.wireDetailHeader()
	p.wireMenuButton()
	p.wireToday()
	p.wireKeys()
	p.wirePanel()
	p.wireTriage()
	p.ctl.OnChange = p.onChange
	w.SetContent(p.root)
	w.SetDefaultSize(1280, 900)
	w.Present()
	flush := func() {
		ctx := glib.MainContextDefault()
		for i := 0; i < 100 && ctx.Pending(); i++ {
			ctx.Iteration(false)
		}
	}
	for _, style := range []board.Style{board.StyleList, board.StyleColumns, board.StyleToday, board.StyleList} {
		p.ctl.SetStyle(style)
		p.applyInlineDetail()
		p.applyAll()
		flush()
		if got := p.stack.VisibleChildName(); got != style.Nick() {
			t.Fatalf("style %s shows %q", style.Nick(), got)
		}
		for _, c := range src.Snapshot().Cases {
			if c.Done() {
				continue
			}
			p.ctl.Select(c.ID)
			p.applyAll()
			flush()
			if p.ctl.View().Detail == nil || p.detailStack.VisibleChildName() != "detail" {
				t.Fatalf("case %s has no rendered detail in %s", c.ID, style.Nick())
			}
			if p.statePill == nil {
				t.Fatal("detail has no focus target after sending or discarding")
			}
			if p.panel.inPanel != (style != board.StyleList) {
				t.Fatalf("detail is in the wrong parent for %s", style.Nick())
			}
		}
	}
	smokeBoardFixes(t, w, p, src, flush)
	// Even with a global triage owner, a sample board must not reach its
	// controller. A bare owner makes such an accidental access fail here.
	w.assist = &Assistant{board: &BoardTriage{}}
	p.renderTriageButton()
	p.onTriageClicked()
	w.assist = nil
	if os.Getenv("MALACHI_GTK_CARDS_SMOKE") == "1" {
		smokeBoardConversationCards(t, p, flush)
	}
	if os.Getenv("MALACHI_GTK_EDITOR_SMOKE") == "1" {
		smokeBoardReplyEditor(t, p, src, flush)
	}
	src.Replace(board.EmptySnapshot())
	p.applyAll()
	flush()
	if p.stack.VisibleChildName() != "empty" {
		t.Fatal("empty source did not replace the board")
	}
	smokeModeSwitch(t, w, p, flush)
}

// smokeModeSwitch moves the board page into window.blp's own mode stack
// and switches through Mail, Board and Calendar the way setMode shows
// them (syncModeSwitches, the stack's page by the mode's nick): both mode
// switches follow, the Calendar's page is the empty calendar_page_bin, and
// with Show the Board off the switches stay with the Board toggle
// insensitive.
func smokeModeSwitch(t *testing.T, w *Window, p *boardPage, flush func()) {
	b := data.Builder("window.ui")
	host := b.GetObject("main_window").Cast().(*adw.ApplicationWindow)
	defer host.Destroy()
	w.SetContent(nil)
	b.GetObject("board_page_bin").Cast().(*adw.Bin).SetChild(p.root)
	w.modeStack = b.GetObject("mode_stack").Cast().(*gtk.Stack)
	w.modeSwitchMail = b.GetObject("mode_switch").Cast().(*adw.ToggleGroup)
	w.calendarPageBin = b.GetObject("calendar_page_bin").Cast().(*adw.Bin)
	wireModeSwitch(w, w.modeSwitchMail)
	host.SetDefaultSize(1280, 900)
	host.Present()
	flush()
	if w.calendarPageBin.Child() != nil {
		t.Fatal("calendar_page_bin is not empty")
	}
	for _, m := range []board.Mode{board.ModeMail, board.ModeBoard, board.ModeCalendar, board.ModeMail} {
		w.mode = m
		w.syncModeSwitches(m)
		w.modeStack.SetVisibleChildName(m.Nick())
		flush()
		if got := w.modeStack.VisibleChildName(); got != m.Nick() {
			t.Fatalf("mode %s shows page %q", m.Nick(), got)
		}
		for _, g := range w.modeSwitches() {
			if got := g.ActiveName(); got != m.Nick() {
				t.Fatalf("mode %s: a switch shows %q", m.Nick(), got)
			}
			for _, n := range board.ModesWithCalendar {
				if g.ToggleByName(n.Nick()) == nil {
					t.Fatalf("a mode switch has no %q toggle", n.Nick())
				}
			}
		}
		if m == board.ModeCalendar {
			if w.modeStack.VisibleChild() == nil || !gtk.BaseWidget(w.calendarPageBin).IsAncestor(w.modeStack) {
				t.Fatal("the Calendar's page is not calendar_page_bin")
			}
		}
	}
	for _, on := range []bool{false, true} {
		w.setModeSwitchBoardEnabled(on)
		flush()
		for _, g := range w.modeSwitches() {
			if !g.Visible() {
				t.Fatal("a mode switch hid with Show the Board off")
			}
			if got := g.ToggleByName("board").Enabled(); got != on {
				t.Fatalf("Board toggle enabled = %v with Show the Board %v", got, on)
			}
			if !g.ToggleByName("calendar").Enabled() || !g.ToggleByName("mail").Enabled() {
				t.Fatal("Show the Board turned the Mail or Calendar toggle off")
			}
		}
	}
	b.GetObject("board_page_bin").Cast().(*adw.Bin).SetChild(nil)
}

// smokeBoardFixes checks the 2026-10-08 fixes on the samples: the Snoozed
// item in the navigation column, the badges of the detail, rows kept
// across a refresh, the D key, and Archive's toast with Undo.
func smokeBoardFixes(t *testing.T, w *Window, p *boardPage, src *board.InMemorySource, flush func()) {
	t.Helper()
	p.ctl.SetStyle(board.StyleList)
	p.applyInlineDetail()
	p.applyAll()
	flush()
	vm := p.ctl.View()
	if len(p.navRows) != len(vm.Nav)+len(vm.Accounts) {
		t.Fatalf("navigation column has %d rows, view model %d", len(p.navRows), len(vm.Nav)+len(vm.Accounts))
	}
	snoozed := false
	for _, n := range vm.Nav {
		snoozed = snoozed || n.Filter.Kind == board.FilterSnoozed
	}
	if !snoozed {
		t.Fatal("no Snoozed item in the navigation column")
	}

	var live board.CaseID
	for _, c := range src.Snapshot().Cases {
		if !c.Done() {
			live = c.ID
			break
		}
	}
	if live == "" {
		t.Fatal("the samples have no live case")
	}
	p.ctl.Select(live)
	flush()
	d := p.ctl.View().Detail
	children := 0
	for c := p.top.badges.FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		children++
	}
	if children != len(d.Badges) {
		t.Fatalf("detail shows %d badges, view model %d", children, len(d.Badges))
	}
	row := p.caseRows[live]
	pill := p.statePill
	p.ctl.Refresh()
	p.applyAll()
	flush()
	if p.caseRows[live] != row || p.statePill != pill {
		t.Fatal("a refresh replaced the selected row or the state pill")
	}

	if !p.boardKey(board.KeyDone) {
		t.Fatal("D did nothing on a live case")
	}
	flush()
	if c, _ := src.Snapshot().Case(live); !c.Done() {
		t.Fatal("D did not mark the case done")
	}
	p.ctl.Reopen(live)
	flush()

	var archived *board.ArchiveOutcome
	p.ctl.OnArchived = func(o board.ArchiveOutcome) {
		archived = &o
		w.boardArchived(o)
	}
	p.ctl.Select(live)
	p.ctl.Archive(live)
	flush()
	if archived == nil || archived.Case != live || archived.UndoLabel == "" {
		t.Fatalf("Archive gave no toast with Undo: %+v", archived)
	}
	p.ctl.UndoArchive(*archived)
	flush()
	if c, _ := src.Snapshot().Case(live); c.Done() {
		t.Fatal("Undo left the case done")
	}
	p.ctl.OnArchived = nil
}
