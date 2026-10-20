// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"errors"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gio/v2"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/crm"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// Run with a display (xvfb-run, Broadway): MALACHI_GTK_SMOKE=1 go test
// ./internal/window -run 'Crm' -count=1 -v. The Save to eWay-CRM dialog
// over the invented suggestions of crmSampleLookup, the header bar's CRM
// button and actions of the main window over made-up accounts, and the
// bar; no daemon, nothing is saved.

func crmSmoke(t *testing.T) {
	t.Helper()
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the widget smoke test")
	}
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	adw.Init()
}

func crmSampleRef(n string) api.CrmItemRef {
	kinds := map[string]api.CrmItemKind{"1": api.CrmContact, "2": api.CrmContact, "3": api.CrmCompany,
		"4": api.CrmCompany, "5": api.CrmDeal, "6": api.CrmDeal, "7": api.CrmProject}
	return api.CrmItemRef{Kind: kinds[n], GUID: "00000000-0000-4000-8000-00000000000" + n}
}

func TestCrmDialogGTKSmoke(t *testing.T) {
	crmSmoke(t)
	w := &Window{log: slog.Default()}
	d := newCrmDialog(w, nil, "acc-m", []api.MessageID{"m1"}, "Offer · Jan Novák")
	if !d.loading.Visible() || d.save.Sensitive() || d.search.Sensitive() {
		t.Error("while crm.lookup runs: the spinner, Save and the search off")
	}
	d.loaded(crmSampleLookup(), nil)
	if d.loading.Visible() || !d.save.Sensitive() || !d.search.Sensitive() || !d.partial.Visible() {
		t.Error("ready: Save, the search and the partial hint")
	}
	if d.searchHint.Visible() || d.message.Visible() {
		t.Error("deals are listed: no search hint, no empty text")
	}
	if len(d.rows) != 7 {
		t.Fatalf("%d rows, want 7", len(d.rows))
	}
	for n, checked := range map[string]bool{"1": true, "2": true, "3": true, "4": false, "5": true, "6": false, "7": false} {
		if got := d.rows[crmSampleRef(n)].check.Active(); got != checked {
			t.Errorf("item %s checked %v, want %v", n, got, checked)
		}
	}
	if r := d.rows[crmSampleRef("5")]; r.superior == nil || !r.superior.Active() {
		t.Error("the one open deal is the superior item")
	}
	if d.rows[crmSampleRef("1")].superior != nil {
		t.Error("a contact has no Superior Item")
	}
	// Another deal checked is a plain link; its radio moves the marker.
	d.rows[crmSampleRef("6")].check.SetActive(true)
	if !d.model.Checked(crmSampleRef("6")) {
		t.Error("the check reached the model")
	}
	if sup, _ := d.model.Superior(); sup != crmSampleRef("5") {
		t.Errorf("superior %v after a plain check", sup)
	}
	d.rows[crmSampleRef("7")].superior.SetActive(true)
	if sup, _ := d.model.Superior(); sup != crmSampleRef("7") || !d.rows[crmSampleRef("7")].check.Active() ||
		d.rows[crmSampleRef("5")].superior.Active() {
		t.Errorf("the radio moved the marker: %v", sup)
	}
	d.private.SetActive(true)
	if !d.model.Private() {
		t.Error("Private reached the model")
	}
	// The same items again (a search that found nothing new) keep the rows.
	before := d.rows[crmSampleRef("1")].check
	d.model.Merge(api.CrmSearchResult{})
	d.render()
	if d.rows[crmSampleRef("1")].check != before {
		t.Error("the rows were rebuilt for the same items")
	}
	d.model.Merge(api.CrmSearchResult{Items: []api.CrmItem{{Kind: api.CrmContact, GUID: "00000000-0000-4000-8000-000000000008", Name: "Found"}}})
	d.render()
	if len(d.rows) != 8 || d.rows[crmSampleRef("6")].check.Active() != true {
		t.Error("a search result joins unchecked, checks stay")
	}

	failed := newCrmDialog(w, nil, "acc-m", []api.MessageID{"m1"}, "x")
	failed.loaded(api.CrmLookupResult{}, &api.Error{Code: api.CodeCrmSignInUnsupported, Data: api.CrmSignInUnsupportedData{Reason: api.CrmSignInPlan}})
	if !failed.message.Visible() || failed.message.Text() != crm.ErrorText(&api.Error{Code: api.CodeCrmSignInUnsupported,
		Data: api.CrmSignInUnsupportedData{Reason: api.CrmSignInPlan}}, i18n.Tr) || failed.save.Sensitive() {
		t.Errorf("a failed lookup: %q", failed.message.Text())
	}
	other := newCrmDialog(w, nil, "acc-m", []api.MessageID{"m1"}, "x")
	other.loaded(api.CrmLookupResult{}, errors.New("boom"))
	if !other.message.Visible() || other.message.Text() == "" {
		t.Error("a failed lookup has its sentence")
	}
	empty := newCrmDialog(w, nil, "acc-m", []api.MessageID{"m1"}, "x")
	empty.loaded(api.CrmLookupResult{AccountID: "e", Saved: []api.CrmSavedMessage{{MessageID: "m1"}}}, nil)
	if empty.message.Text() != crm.DialogTexts(false, i18n.Tr).Empty || !empty.save.Sensitive() {
		t.Error("nothing found: the empty text, and Save saves without links")
	}
}

func TestCrmControlsGTKSmoke(t *testing.T) {
	crmSmoke(t)
	b := data.Builder("window.ui")
	w := &Window{
		ApplicationWindow: b.GetObject("main_window").Cast().(*adw.ApplicationWindow),
		log:               slog.Default(),
		actions:           make(map[string]*gio.SimpleAction),
		toasts:            adw.NewToastOverlay(),
		loaded:            map[api.MessageID]*loadedMessage{},
		crmState:          newCrmState(),
	}
	defer w.Destroy()
	mail := api.Account{ID: "acc-m", Enabled: true, Config: api.AccountConfig{Name: "Work", Email: "me@example.invalid"}}
	eway := api.Account{ID: "acc-e", Enabled: true, Config: api.AccountConfig{Kind: api.AccountEway, Name: "acme",
		Eway: &api.EwayConfig{ServiceURL: "https://hosting.eway-crm.com/acme", Username: "jan"}}}
	w.model.accounts = []api.Account{mail}
	s := api.MessageSummary{ID: "m1", AccountID: "acc-m", FolderID: "f1", Subject: "Offer"}
	subjects := []crmSubject{{s: s}}
	c := newCrmControls(w, "win", &w.ApplicationWindow.Window, b, func(a *gio.SimpleAction) {
		w.AddAction(a)
		w.actions[a.Name()] = a
	}, func() []crmSubject { return subjects }, w.Toast)

	c.refresh()
	if c.save.Visible() || c.saved.Visible() || w.actions[crmActionSave].Enabled() || c.section.NItems() != 0 {
		t.Error("without an eWay-CRM account nothing shows")
	}
	w.model.accounts = []api.Account{mail, eway}
	c.refresh()
	texts := crm.ButtonFor(crm.ButtonNotSaved, i18n.Tr)
	if !c.save.Visible() || c.saved.Visible() || c.save.TooltipText() != texts.Tooltip || !w.actions[crmActionSave].Enabled() {
		t.Error("not saved: Save to eWay-CRM")
	}
	if c.section.NItems() != 1 || w.actions[crmActionOpen].Enabled() {
		t.Errorf("More Actions: %d items", c.section.NItems())
	}

	w.crmState.inFlight.Begin("acc-m", []api.MessageID{"m1"})
	c.refresh()
	if !c.saveSpinner.Visible() || c.saveIcon.Visible() || w.actions[crmActionSave].Enabled() ||
		c.save.TooltipText() != crm.ButtonFor(crm.ButtonSaving, i18n.Tr).Tooltip {
		t.Error("saving: the spinner, Save off")
	}
	w.crmState.inFlight.End("acc-m", []api.MessageID{"m1"})

	rec := &api.CrmRecord{AccountID: "acc-e", ItemGUID: "00000000-0000-4000-8000-000000000009",
		URL:   "https://open.eway-crm.com/?ws=x&l=y",
		Items: []api.CrmItem{{Kind: api.CrmContact, GUID: "00000000-0000-4000-8000-000000000001", Name: "Novák, Jan"}}}
	w.crmState.setOverride("acc-m", "m1", rec)
	c.refresh()
	if c.save.Visible() || !c.saved.Visible() || !w.actions[crmActionOpen].Enabled() ||
		!w.actions[crmActionEditLinks].Enabled() || !w.actions[crmActionRemove].Enabled() || w.actions[crmActionSave].Enabled() {
		t.Error("saved: In eWay-CRM with its menu")
	}
	if c.section.NItems() != 3 {
		t.Errorf("More Actions of a saved message: %d items", c.section.NItems())
	}

	subjects = []crmSubject{{s: s, conv: true}}
	c.refresh()
	if c.save.Visible() || c.saved.Visible() || c.section.NItems() != 0 {
		t.Error("a conversation's row offers nothing")
	}
	s2 := s
	s2.ID = "m2"
	subjects = []crmSubject{{s: s}, {s: s2}}
	c.refresh()
	if !c.save.Visible() || c.save.TooltipText() != crm.SaveMenuLabel(2, i18n.Tr) || !w.actions[crmActionSave].Enabled() {
		t.Errorf("several rows: %q", c.save.TooltipText())
	}

	// The message window has the same widgets, the bar and the More
	// Actions section.
	mb := data.Builder("message_window.ui")
	g := gio.NewSimpleActionGroup()
	mc := newCrmControls(w, "msg", &w.ApplicationWindow.Window, mb, func(a *gio.SimpleAction) { g.AddAction(a) },
		func() []crmSubject { return []crmSubject{{s: s}} }, w.Toast)
	mc.refresh()
	if !mc.saved.Visible() || g.LookupAction(crmActionRemove) == nil || mc.section.NItems() != 3 {
		t.Error("the message window's controls")
	}
	if mbar := bindCrmBar(mb, func(string) {}); mbar == nil {
		t.Error("the message window has the bar")
	} else if mbar.show(rec, nil); !mbar.box.Visible() {
		t.Error("the message window's bar shows")
	}
	if bindCrmBar(data.Builder("embedded_window.ui"), func(string) {}) != nil {
		t.Error("an attached message has no bar")
	}

	bar := newCrmBar(func(string) {})
	bar.show(rec, nil)
	if !bar.box.Visible() || !strings.Contains(bar.names.Text(), "Novák, Jan") || !bar.open.Visible() {
		t.Error("the bar of a saved message")
	}
	bar.show(nil, nil)
	if bar.box.Visible() {
		t.Error("the bar hides without a record")
	}
}
