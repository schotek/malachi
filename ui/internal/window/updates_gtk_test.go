// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"context"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/updates"
	"github.com/schotek/malachi/ui/internal/updatesctl"
)

// The update banner's bar over bare widgets (no window, daemon or store):
// its buttons in the banner's order with the first suggested, the security
// look, the buttons kept while they do not change, hidden with no banner,
// and a click running the button's action.
// MALACHI_GTK_SMOKE=1 go test ./internal/window -run '^TestUpdateBarGTKSmoke$' -count=1
func TestUpdateBarGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the widget smoke test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()

	now := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	info := &api.UpdateInfo{Version: "1.0.1",
		URL: "https://github.com/schotek/malachi/releases/download/v1.0.1/x.deb"}
	update := updates.BannerFor(&api.UpdateStatus{Enabled: true, State: api.UpdateAvailable, Available: info}, now, i18n.Tr)
	sec := *info
	sec.Security = true
	security := updates.BannerFor(&api.UpdateStatus{Enabled: true, State: api.UpdateAvailable, Available: &sec}, now, i18n.Tr)

	v := &updateView{}
	b := v.newBar()
	var acted []updates.Action
	b.act = func(a updates.Action) { acted = append(acted, a) }

	b.show(updates.Banner{})
	if b.root.IsVisible() {
		t.Error("shown without a banner")
	}
	b.show(update)
	buttons := b.made
	if !b.root.IsVisible() || len(buttons) != 4 || b.title.Text() != update.Title || b.title.UseMarkup() {
		t.Fatalf("update banner: visible %v, %d buttons, title %q", b.root.IsVisible(), len(buttons), b.title.Text())
	}
	for i, x := range update.Buttons {
		if buttons[i].Label() != x.Label || buttons[i].HasCSSClass("suggested-action") != (i == 0) {
			t.Errorf("button %d: %q", i, buttons[i].Label())
		}
	}
	if b.root.HasCSSClass("update-security") {
		t.Error("an update has the security look")
	}
	b.show(update)
	if b.made[0] != buttons[0] {
		t.Error("the same buttons were made again")
	}
	buttons[2].Emit("clicked")
	if len(acted) != 1 || acted[0] != updates.ActionLater {
		t.Errorf("acted %v", acted)
	}
	b.show(security)
	if !b.root.HasCSSClass("update-security") || len(b.made) != 2 {
		t.Errorf("security banner: %d buttons", len(b.made))
	}
	var n int
	for c := gtk.BaseWidget(b.buttons).FirstChild(); c != nil; c = gtk.BaseWidget(c).NextSibling() {
		n++
	}
	if n != 2 {
		t.Errorf("%d buttons in the box, want 2", n)
	}
	b.show(update)
	if b.root.HasCSSClass("update-security") {
		t.Error("the security look stayed")
	}
}

// updateCaller answers every update call with status and records them.
type updateCaller struct {
	mu      sync.Mutex
	methods []string
	status  api.UpdateStatus
}

func (c *updateCaller) Call(_ context.Context, method string, _ any, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.methods = append(c.methods, method)
	*(result.(*api.UpdateStatus)) = c.status
	return nil
}

// queueLoop queues posted callbacks for drain on the test's thread.
type queueLoop struct {
	mu sync.Mutex
	q  []func()
}

func (l *queueLoop) Post(f func()) { l.mu.Lock(); l.q = append(l.q, f); l.mu.Unlock() }

func (l *queueLoop) After(time.Duration, func()) {}

func (l *queueLoop) drain(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for n > 0 {
		l.mu.Lock()
		q := l.q
		l.q = nil
		l.mu.Unlock()
		for _, f := range q {
			f()
			n--
		}
		if n > 0 && time.Now().After(deadline) {
			t.Fatalf("%d callbacks did not come", n)
		}
		time.Sleep(time.Millisecond)
	}
}

// Preferences → General → Updates over the real preferences.ui and a fake
// daemon, and the window.ui objects updates.go takes.
// MALACHI_GTK_SMOKE=1 go test ./internal/window -run '^TestUpdatePreferencesGTKSmoke$' -count=1
func TestUpdatePreferencesGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the widget smoke test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()

	wb := data.Builder("window.ui")
	if _, ok := wb.GetObject("update_bar").Cast().(*adw.Bin); !ok {
		t.Error("window.ui: update_bar is not an Adw.Bin")
	}
	if l, ok := wb.GetObject("no_accounts_update_note").Cast().(*gtk.Label); !ok || l.UseMarkup() {
		t.Error("window.ui: no_accounts_update_note")
	}

	caller := &updateCaller{status: api.UpdateStatus{Enabled: true, State: api.UpdateUpToDate}}
	loop := &queueLoop{}
	saved := appUpdates
	appUpdates = updatesctl.NewStore(caller, loop, "en", nil)
	defer func() { appUpdates = saved }()

	b := data.Builder("preferences.ui")
	d := &PreferencesDialog{log: slog.New(slog.DiscardHandler)}
	unbind := d.bindUpdates(b, nil)
	defer unbind()
	loop.drain(t, 1) // update.status: no status yet

	group := b.GetObject("update_group").Cast().(*adw.PreferencesGroup)
	enabled := b.GetObject("update_enabled").Cast().(*adw.SwitchRow)
	status := b.GetObject("update_status").Cast().(*adw.ActionRow)
	check := b.GetObject("update_check_now").Cast().(*gtk.Button)
	if group.Title() != updates.PrefsGroup(i18n.Tr) || enabled.Title() != updates.PrefsTitle(i18n.Tr) ||
		check.Label() != updates.CheckNow(i18n.Tr) {
		t.Errorf("texts %q %q %q", group.Title(), enabled.Title(), check.Label())
	}
	if !enabled.Active() || !enabled.Sensitive() || !check.Sensitive() ||
		status.Title() != updates.AboutLine(&caller.status, updateCheckedAt, i18n.Tr) {
		t.Errorf("up to date: active %v, check %v, line %q", enabled.Active(), check.Sensitive(), status.Title())
	}

	caller.mu.Lock()
	caller.status = api.UpdateStatus{State: api.UpdateDisabled}
	caller.mu.Unlock()
	enabled.SetActive(false)
	if enabled.Sensitive() {
		t.Error("the switch stays sensitive while its write runs")
	}
	loop.drain(t, 1)
	if enabled.Active() || !enabled.Sensitive() || check.Sensitive() {
		t.Errorf("disabled: active %v, sensitive %v, check %v", enabled.Active(), enabled.Sensitive(), check.Sensitive())
	}
	caller.mu.Lock()
	got := append([]string(nil), caller.methods...)
	caller.mu.Unlock()
	if len(got) != 2 || got[0] != api.MethodUpdateStatus || got[1] != api.MethodUpdateSetPrefs {
		t.Errorf("calls %v", got)
	}
}
