// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

import (
	"os"
	"runtime"
	"testing"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"

	"github.com/schotek/malachi/ui/internal/crm"
	"github.com/schotek/malachi/ui/internal/i18n"
)

// Run with a display (xvfb-run, Broadway): MALACHI_GTK_SMOKE=1 go test
// ./internal/accountwizard -run 'Eway' -count=1 -v. The eWay-CRM
// assistant's widgets over its flow, without a daemon: the address field
// and its check, and the edit mode on the sign-in page.
func TestEwayWizardGTKSmoke(t *testing.T) {
	if os.Getenv("MALACHI_GTK_SMOKE") != "1" {
		t.Skip("set MALACHI_GTK_SMOKE=1 with a GTK display to run the widget smoke test")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	adw.Init()

	w := NewEway(nil, nil)
	w.flow.start()
	tx := crm.WizardTexts(i18n.Tr)
	if w.Title() != tx.Title || w.serviceRow.Title() != tx.ServiceLabel {
		t.Errorf("texts %q %q", w.Title(), w.serviceRow.Title())
	}
	next := w.pages[crm.PageService].button
	if next.Sensitive() {
		t.Error("Next waits for an address")
	}
	w.serviceRow.SetText("http://hosting.eway-crm.com/acme")
	if next.Sensitive() || !w.serviceProblem.Visible() {
		t.Error("http is refused under the field")
	}
	w.serviceRow.SetText("hosting.eway-crm.com/acme")
	if !next.Sensitive() || w.serviceProblem.Visible() {
		t.Error("an address without a scheme can be looked up")
	}
	if !w.followCheck.Active() {
		t.Error("a new account follows saved conversations")
	}

	a := ewayAccount()
	a.Config.Eway.FollowThreads = false
	e := NewEwayEdit(nil, nil, a)
	e.flow.start()
	if e.Title() != tx.EditTitle || e.nameRow.Text() != "Acme" || e.userRow.Text() != "jana" {
		t.Errorf("edit: %q %q %q", e.Title(), e.nameRow.Text(), e.userRow.Text())
	}
	if e.addressRow.Subtitle() != "https://hosting.eway-crm.com/acme" || e.followCheck.Active() {
		t.Error("edit: the address fixed, the check box as stored")
	}
	if tag := e.nav.VisiblePage().Tag(); tag != "credentials" {
		t.Errorf("edit opens on %q", tag)
	}
	if e.passwordRow.Title() != tx.NewPasswordLabel {
		t.Error("edit: the password may stay empty")
	}
}
