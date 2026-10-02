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
		actions: make(map[string]*gio.SimpleAction),
		toasts:  adw.NewToastOverlay(),
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
	p.wireToday()
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
}
