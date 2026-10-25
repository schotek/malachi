// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/board"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/settings"
)

// The real window.blp and WebKit view: under a short body, text or HTML,
// the "•••" follows the body's last line and the filler takes the rest;
// under a long one the body takes the whole area and the button sits at
// its bottom. Made-up bodies, a client with no daemon behind it.
//
//	MALACHI_GTK_SMOKE=1 WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1 \
//	  go test ./internal/window -run '^TestBodyFitGTKSmoke$' -count=1 -v
//
// Run it on its own: after TestConversationChatGTKSmoke in the same
// process WebKit aborts while the builder makes any further viewer (it did
// so before the viewer measured its document, too).
func TestBodyFitGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the widget smoke test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()
	rpc := client.New(filepath.Join(t.TempDir(), "absent.sock"))
	defer rpc.Close()
	st := settings.NewMemory()
	b := data.Builder("window.ui")
	w := &Window{
		ApplicationWindow: b.GetObject("main_window").Cast().(*adw.ApplicationWindow),
		log:               slog.Default(), mode: board.ModeMail,
		client:        rpc,
		settings:      st,
		assist:        NewAssistant(st, slog.Default()),
		actions:       make(map[string]*gio.SimpleAction),
		toasts:        b.GetObject("toast_overlay").Cast().(*adw.ToastOverlay),
		messageStack:  b.GetObject("message_stack").Cast().(*gtk.Stack),
		messageList:   b.GetObject("message_list").Cast().(*gtk.ListBox),
		loaded:        map[api.MessageID]*loadedMessage{},
		findingDrafts: map[api.DraftID]bool{},
		openMessages:  map[api.MessageID]*MessageWindow{},
	}
	defer w.Destroy()
	w.issues = &issueActions{client: rpc, log: slog.Default(), post: func(fn func()) { glib.IdleAdd(fn) }, account: w.model.account}
	v := newMessageView(w, &w.ApplicationWindow.Window, b)
	defer v.close()
	w.messageStack.SetVisibleChildName("message")
	w.SetDefaultSize(1200, 800)
	w.Present()
	ctx := glib.MainContextDefault()
	run := func(d time.Duration) {
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			for i := 0; i < 100 && ctx.Pending(); i++ {
				ctx.Iteration(false)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	run(300 * time.Millisecond)

	page := gtk.BaseWidget(v.stack.Parent())
	row := gtk.BaseWidget(v.quotedRow)
	s := api.MessageSummary{ID: "m1", AccountID: "a", FolderID: "in", Subject: "FW: Finální adresa API"}
	long := strings.Repeat("Řádek dlouhé zprávy.\n", 300)
	for _, c := range []struct {
		name   string
		body   api.MessageBodyResult
		fitted bool
	}{
		{"text-short", api.MessageBodyResult{Text: "Děkuji za odpověď\n\nPT"}, true},
		{"html-short", api.MessageBodyResult{HasHTML: true, HTML: "<p>Děkuji za odpověď</p><p>PT</p>"}, true},
		{"text-long", api.MessageBodyResult{Text: long}, false},
		{"html-long", api.MessageBodyResult{HasHTML: true, HTML: "<p>" + strings.ReplaceAll(long, "\n", "</p><p>") + "</p>"}, false},
	} {
		body := c.body
		body.MessageID, body.BodyState, body.QuotedTrimmed = "m1", api.BodyFetched, true
		v.render(s, &loadedMessage{body: &body})
		run(3 * time.Second)
		pb, ok := page.ComputeBounds(page)
		rb, ok2 := row.ComputeBounds(page)
		if !ok || !ok2 || !row.Visible() {
			t.Fatalf("%s: no bounds of the page or the button row (visible %v)", c.name, row.Visible())
		}
		pageH, top, bottom := float64(pb.Height()), float64(rb.Y()), float64(rb.Y()+rb.Height())
		t.Logf("%s: page %.0f high, button row %.0f..%.0f, filler visible %v, html %dpx in %d room",
			c.name, pageH, top, bottom, v.fit.filler.Visible(), v.fit.px, v.fit.room)
		atBottom := pageH-bottom < 1
		if c.fitted == atBottom {
			t.Errorf("%s: button row at %.0f..%.0f of %.0f, want fitted %v", c.name, top, bottom, pageH, c.fitted)
		}
		if c.fitted && top > pageH/2 {
			t.Errorf("%s: the button row starts at %.0f of %.0f: not under the short body", c.name, top, pageH)
		}
	}
}
