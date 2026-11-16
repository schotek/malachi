// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package window

import (
	"github.com/diamondburned/gotk4-adwaita/pkg/adw"

	"github.com/schotek/malachi/ui/internal/accountservices"
	"github.com/schotek/malachi/ui/internal/calendar"
	"github.com/schotek/malachi/ui/internal/calendarctl"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/settings"
)

// The Calendar's offer to sync the calendar of an account that has it off
// (docs/plans/account-services.md §3.3; macos CalendarSidebarViewController
// and CalendarEmptyView): the sidebar's rows (calendar_sidebar.go
// calendarOfferRow), and the empty page's button, which turns sync on for
// the one account offered instead of opening Preferences. Turn On asks the
// consent question of Preferences → Calendar when the grant lacks the
// calendar.

// wireSyncOffer gives the controller the key account-services-dismissed
// and the consent question.
func (p *calendarPage) wireSyncOffer() {
	if st := p.w.settings; st != nil {
		p.ctl.Dismissed = st.AccountServicesDismissed
		p.ctl.SetDismissed = st.SetAccountServicesDismissed
		// Another window's Not Now, or the pruning of a removed account.
		st.OnChanged(settings.KeyAccountServicesDismissed, func() {
			if p.started {
				p.apply()
			}
		})
	}
	p.ctl.OnQuestion = p.askSyncConsent
	p.emptyButton.ConnectClicked(func() {
		// Without an offer the button is Open Preferences (its action).
		if p.emptyOffer != "" {
			p.ctl.TurnOnSync(p.emptyOffer)
		}
	})
}

// applyEmptySyncOffer makes the empty page's button Turn On for the one
// account offered, else Open Preferences.
func (p *calendarPage) applyEmptySyncOffer() {
	tr := i18n.Tr
	id, ok := p.ctl.EmptySyncOffer()
	if !ok {
		id = ""
	}
	p.emptyOffer = id
	if id != "" {
		p.emptyButton.SetActionName("")
		p.emptyButton.SetLabel(accountservices.TurnOn(tr))
		return
	}
	p.emptyButton.SetActionName("app.preferences")
	p.emptyButton.SetLabel(calendar.OpenPreferences(tr))
}

// askSyncConsent asks Turn On's consent question (Sign In… or Cancel), as
// Preferences → Calendar does.
func (p *calendarPage) askSyncConsent(q calendar.Question, answer func(calendarctl.Answer)) {
	a := adw.NewAlertDialog(q.Heading, q.Body)
	a.SetHeadingUseMarkup(false)
	a.SetBodyUseMarkup(false)
	a.AddResponse("cancel", i18n.T("_Cancel"))
	a.AddResponse("confirm", q.Confirm)
	a.SetResponseAppearance("confirm", adw.ResponseSuggested)
	a.SetDefaultResponse("confirm")
	a.SetCloseResponse("cancel")
	a.ConnectResponse(func(r string) {
		if r == "confirm" {
			answer(calendarctl.AnswerConfirm)
			return
		}
		answer(calendarctl.AnswerCancel)
	})
	a.Present(p.w)
}
