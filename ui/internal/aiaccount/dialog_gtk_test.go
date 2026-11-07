// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package aiaccount

import (
	"os"
	"runtime"
	"testing"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// Run with a display (xvfb-run, Broadway): MALACHI_GTK_SMOKE=1 go test
// ./internal/aiaccount -run GTKSmoke -count=1 -v. The dialog's widgets
// over the controller, without a daemon: the rows of an
// OpenAI-compatible server (the address with its hint, the http warning,
// the optional key, the consent), Save flagging the first problem, the
// models listed in the popovers, and the edit mode (the stored key with
// Replace Key…, the provider fixed, no consent asked again).
func TestAIAccountDialogGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the widget smoke test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()

	tx := Texts(i18n.Tr)
	d := New(nil, nil, api.AIOpenAICompatible, nil)
	d.ctl.Start()
	if d.Title() != tx.AddTitle || d.windowTitle.Title() != tx.AddTitle || d.save.Label() != tx.Add {
		t.Errorf("texts %q %q %q", d.Title(), d.windowTitle.Title(), d.save.Label())
	}
	if d.providerRow.Subtitle() != d.ctl.ProviderName() || !d.addressRow.Visible() || !d.addressHint.Visible() {
		t.Error("a compatible server has the address row and its hint")
	}
	if d.keyRow.Title() != tx.KeyOptionalLabel || d.keyStoredRow.Visible() || d.keepKeyButton.Visible() {
		t.Error("a new server's key is optional and typed")
	}
	if !d.consentGroup.Visible() || d.consentGroup.Title() != d.ctl.Consent.Heading || d.consentRow.Title() != d.ctl.Consent.Checkbox {
		t.Error("a new account asks the consent")
	}
	if d.httpBanner.Revealed() {
		t.Error("no warning before an address")
	}
	d.addressRow.SetText("http://10.0.0.5:11434/v1")
	if !d.httpBanner.Revealed() || d.httpBanner.Title() != HTTPWarning(i18n.Tr, "10.0.0.5") {
		t.Errorf("http outside this computer warns: %v %q", d.httpBanner.Revealed(), d.httpBanner.Title())
	}
	d.addressRow.SetText("http://localhost:11434/v1")
	if d.httpBanner.Revealed() {
		t.Error("a loopback address does not warn")
	}
	if d.modelButton.Sensitive() {
		t.Error("the model popover waits for the models")
	}

	// Until a test reached the server, the models, the consent and Save
	// are greyed; a successful test unlocks them.
	if d.modelGroup.Sensitive() || d.consentGroup.Sensitive() || d.save.Sensitive() {
		t.Error("unlocked before a test")
	}
	d.ctl.test = &TestOutcome{OK: true}
	d.refresh()
	if !d.modelGroup.Sensitive() || !d.consentGroup.Sensitive() || !d.save.Sensitive() {
		t.Error("still locked after a successful test")
	}

	// Save with an empty name flags the name row and says why.
	d.onSave()
	if d.flaggedRow != d.nameRow || !d.nameRow.HasCSSClass("error") || !d.banner.Revealed() || d.banner.Title() != tx.NameMissing {
		t.Errorf("Save without a name: flagged %v, banner %v %q", d.flaggedRow == d.nameRow, d.banner.Revealed(), d.banner.Title())
	}
	d.nameRow.SetText("Ollama")
	if d.banner.Revealed() || d.nameRow.HasCSSClass("error") {
		t.Error("typing the name clears the problem")
	}
	d.onSave()
	if d.flaggedRow != d.modelRow || d.banner.Title() != tx.ModelMissing {
		t.Errorf("Save without a model: flagged %v, banner %q", d.flaggedRow == d.modelRow, d.banner.Title())
	}
	d.modelRow.SetText("llama3")
	d.onSave()
	if d.flaggedRow != d.consentRow || d.banner.Title() != tx.ConsentMissing {
		t.Errorf("Save without the consent: flagged %v, banner %q", d.flaggedRow == d.consentRow, d.banner.Title())
	}

	// The models a test listed go into both popovers.
	d.ctl.models = []api.AIModel{{ID: "llama3"}, {ID: "qwen2.5", DisplayName: "Qwen 2.5"}}
	d.refresh()
	if !d.modelButton.Sensitive() || !d.boardModelMenu.Sensitive() || len(d.shownModels) != 2 {
		t.Errorf("models listed: %d", len(d.shownModels))
	}
	row, ok := d.modelList.RowAtIndex(1).Cast().(*adw.ActionRow)
	if !ok || row.Title() != "qwen2.5" || row.Subtitle() != "Qwen 2.5" {
		t.Errorf("the second model's row: %v", ok)
	}
	// The popover is as wide as its ids, within bounds, not the ellipsised
	// minimum of a row (B2 finding 2026-11-07).
	for _, b := range []*gtk.MenuButton{d.modelButton, d.boardModelMenu} {
		sc, ok := b.Popover().Child().(*gtk.ScrolledWindow)
		if !ok || !sc.PropagateNaturalWidth() || sc.MinContentWidth() != modelPopoverMinWidth || sc.MaxContentWidth() != modelPopoverMaxWidth {
			t.Errorf("the model popover's width: %v", ok)
		}
		if _, nat, _, _ := sc.Measure(gtk.OrientationHorizontal, -1); nat < modelPopoverMinWidth {
			t.Errorf("the model popover's natural width %d", nat)
		}
	}

	// The edit mode of a stored Anthropic account.
	a := api.Account{ID: "acc_1", Enabled: true, Config: api.AccountConfig{Name: "Work key", Kind: api.AccountAI,
		AI: &api.AIConfig{Provider: api.AIAnthropic, Model: "claude-sonnet-4-5", BoardModel: "claude-haiku-4-5",
			Consent: &api.AIConsent{Version: api.AssistantConsentVersion}}}}
	e := New(nil, nil, api.AIOpenAICompatible, &a)
	if e.Title() != tx.EditTitle || e.save.Label() != tx.Save || e.providerRow.Subtitle() != tx.tr.T("Anthropic API") {
		t.Errorf("edit: %q %q %q", e.Title(), e.save.Label(), e.providerRow.Subtitle())
	}
	if e.addressRow.Visible() || e.addressHint.Visible() || e.consentGroup.Visible() {
		t.Error("edit: no address for Anthropic, no consent asked again")
	}
	if !e.keyStoredRow.Visible() || e.keyRow.Visible() || e.keyStoredRow.Title() != tx.KeyStored || e.keyButton.Label() != tx.ReplaceKey {
		t.Error("edit: the stored key")
	}
	if e.nameRow.Text() != "Work key" || e.modelRow.Text() != "claude-sonnet-4-5" || e.boardModelRow.Text() != "claude-haiku-4-5" {
		t.Errorf("edit: fields %q %q %q", e.nameRow.Text(), e.modelRow.Text(), e.boardModelRow.Text())
	}
	e.ReplaceKey()
	if e.keyStoredRow.Visible() || !e.keyRow.Visible() || e.keyRow.Title() != tx.NewKeyLabel || !e.keepKeyButton.Visible() {
		t.Error("Replace Key… opens the field with Keep the Stored Key")
	}
	e.ctl.KeepKey()
	if !e.keyStoredRow.Visible() || e.keyRow.Visible() {
		t.Error("Keep the Stored Key closes the field")
	}
}
