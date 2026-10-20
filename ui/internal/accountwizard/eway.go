// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package accountwizard

import (
	"log/slog"
	"slices"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/data"
	"github.com/schotek/malachi/ui/internal/client"
	"github.com/schotek/malachi/ui/internal/crm"
	"github.com/schotek/malachi/ui/internal/i18n"
	"github.com/schotek/malachi/ui/internal/widget"
)

// ewayPageTags are the navigation page tags of eway_wizard.blp, by page.
var ewayPageTags = [...]string{crm.PageService: "service", crm.PageCredentials: "credentials"}

func ewayPageOfTag(tag string) (crm.WizardPage, bool) {
	i := slices.Index(ewayPageTags[:], tag)
	return crm.WizardPage(i), i >= 0
}

// ewayPageView is what both pages of the assistant have: the banner, the
// progress of a call under way and the button.
type ewayPageView struct {
	page          *adw.NavigationPage
	banner        *adw.Banner
	progress      *gtk.Box
	progressLabel *gtk.Label
	button        *gtk.Button
}

// EwayWizard is the assistant that adds an eWay-CRM account (kind eway,
// docs/eway.md §2.4), or edits one (NewEwayEdit): the pages Web Service
// Address → Sign In of the macOS sheet in an Adw.Dialog of the Jira
// assistant's shape. The flow, its texts and its calls are ewayFlow
// (eway_flow.go); this only shows what it delivers and feeds the fields
// back. The daemon's error sentences and the address are plain text.
type EwayWizard struct {
	*adw.Dialog

	// OnDone runs on the main loop after account.add or account.update
	// succeeded, before the dialog closes. May be nil.
	OnDone func(id api.AccountID, cfg api.AccountConfig)

	flow *ewayFlow
	log  *slog.Logger

	nav   *adw.NavigationView
	pages [2]ewayPageView

	serviceRow     *adw.EntryRow
	serviceProblem *gtk.Label
	addressRow     *adw.ActionRow
	nameRow        *adw.EntryRow
	userRow        *adw.EntryRow
	passwordRow    *adw.PasswordEntryRow
	followRow      *adw.ActionRow
	followCheck    *gtk.CheckButton

	// shown is the navigation stack as the view shows it (showPages).
	shown     []crm.WizardPage
	serviceOK bool
	busy      bool
	closed    bool
	started   bool
	// filling is set while the view writes a field itself, so that its
	// changed handler does not feed the write back to the flow.
	filling bool
}

// NewEway is the assistant that adds an eWay-CRM account. Present it with
// Present(parent).
func NewEway(c *client.Client, log *slog.Logger) *EwayWizard {
	return newEway(c, log, nil)
}

// NewEwayEdit opens on the sign-in page of account a (Preferences →
// Integrations → Edit, or the account's sign-in banner): name, user name,
// a new password (empty keeps the stored one), the check box; saved with
// account.update.
func NewEwayEdit(c *client.Client, log *slog.Logger, a api.Account) *EwayWizard {
	w := newEway(c, log, &a)
	w.SetFocus(w.passwordRow)
	return w
}

func newEway(c *client.Client, log *slog.Logger, editing *api.Account) *EwayWizard {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	log = log.With("component", "ewaywizard")
	b := data.Builder("eway_wizard.ui")
	label := func(id string) *gtk.Label { return b.GetObject(id).Cast().(*gtk.Label) }
	pageView := func(prefix string) ewayPageView {
		return ewayPageView{
			page:          b.GetObject("eway_" + prefix + "_page").Cast().(*adw.NavigationPage),
			banner:        b.GetObject("eway_" + prefix + "_banner").Cast().(*adw.Banner),
			progress:      b.GetObject("eway_" + prefix + "_progress").Cast().(*gtk.Box),
			progressLabel: label("eway_" + prefix + "_progress_label"),
			button:        b.GetObject("eway_" + prefix + "_next").Cast().(*gtk.Button),
		}
	}
	w := &EwayWizard{
		Dialog: b.GetObject("eway_wizard").Cast().(*adw.Dialog),
		log:    log,
		nav:    b.GetObject("eway_nav").Cast().(*adw.NavigationView),
		pages: [...]ewayPageView{
			crm.PageService:     pageView("service"),
			crm.PageCredentials: pageView("credentials"),
		},
		serviceRow:     b.GetObject("eway_service_row").Cast().(*adw.EntryRow),
		serviceProblem: label("eway_service_problem"),
		addressRow:     b.GetObject("eway_address_row").Cast().(*adw.ActionRow),
		nameRow:        b.GetObject("eway_name_row").Cast().(*adw.EntryRow),
		userRow:        b.GetObject("eway_user_row").Cast().(*adw.EntryRow),
		passwordRow:    b.GetObject("eway_password_row").Cast().(*adw.PasswordEntryRow),
		followRow:      b.GetObject("eway_follow_row").Cast().(*adw.ActionRow),
		followCheck:    b.GetObject("eway_follow_check").Cast().(*gtk.CheckButton),
		// The first page of a navigation view in a UI file is its root.
		shown: []crm.WizardPage{crm.PageService},
	}
	post := func(f func()) { glib.IdleAdd(f) }
	w.flow = newEwayFlow(c, post, i18n.Tr, widget.RPCErrorText, log, editing)
	w.setTexts(label("eway_service_hint"))
	w.wire()
	w.showPages(slices.Clone(w.flow.pages))
	return w
}

// setTexts fills in the fixed texts (every text of the dialog comes from
// the flow and ui/internal/crm) and the fields the flow starts with.
func (w *EwayWizard) setTexts(hint *gtk.Label) {
	f := w.flow
	t := f.texts
	w.SetTitle(f.title())
	for p := range w.pages {
		pv := w.pages[p]
		pv.page.SetTitle(f.pageTitle(crm.WizardPage(p)))
		pv.button.SetLabel(f.nextLabel(crm.WizardPage(p)))
		pv.banner.SetUseMarkup(false)
	}
	w.serviceRow.SetTitle(t.ServiceLabel)
	w.serviceRow.SetText(f.serviceInput)
	hint.SetText(t.ServiceHint)
	w.addressRow.SetTitle(t.ServiceLabel)
	w.addressRow.SetSubtitle(f.serviceURL())
	w.nameRow.SetTitle(t.NameLabel)
	w.userRow.SetTitle(t.UserLabel)
	w.userRow.SetText(f.userName())
	w.passwordRow.SetTitle(f.passwordLabel())
	w.followRow.SetTitle(t.FollowThreads)
	w.followRow.SetSubtitle(t.FollowThreadsHint)
	w.followCheck.SetActive(f.following())
}

func (w *EwayWizard) wire() {
	f := w.flow
	f.onPages = w.showPages
	f.onBusy = w.showBusy
	f.onServiceCheck = func(ok bool, problem string) {
		w.serviceOK = ok
		w.serviceProblem.SetText(problem)
		w.serviceProblem.SetVisible(problem != "")
		w.applyService()
	}
	f.onName = func(name string) {
		if w.nameRow.Text() != name {
			w.filling = true
			w.nameRow.SetText(name)
			w.filling = false
		}
	}
	f.onBanner = w.showBanner
	f.onProblem = w.showProblem
	f.onFocus = w.focus
	f.onDone = func(id api.AccountID, cfg api.AccountConfig) {
		if w.OnDone != nil {
			w.OnDone(id, cfg)
		}
		w.ForceClose()
	}

	w.serviceRow.ConnectChanged(func() { f.setService(w.serviceRow.Text()) })
	serviceNext := func() {
		f.setService(w.serviceRow.Text())
		f.next()
	}
	w.serviceRow.ConnectEntryActivated(serviceNext)
	w.pages[crm.PageService].button.ConnectClicked(serviceNext)

	w.nameRow.ConnectChanged(func() {
		if !w.filling {
			f.setName(w.nameRow.Text())
		}
	})
	syncCredentials := func() { f.setCredentials(w.userRow.Text(), w.passwordRow.Text()) }
	w.userRow.ConnectChanged(syncCredentials)
	w.passwordRow.ConnectChanged(syncCredentials)
	w.nameRow.ConnectEntryActivated(func() { w.userRow.GrabFocus() })
	w.userRow.ConnectEntryActivated(func() {
		syncCredentials()
		w.passwordRow.GrabFocus()
	})
	credentialsNext := func() {
		syncCredentials()
		f.setName(w.nameRow.Text())
		f.next()
	}
	w.passwordRow.ConnectEntryActivated(credentialsNext)
	w.pages[crm.PageCredentials].button.ConnectClicked(credentialsNext)
	w.followCheck.ConnectToggled(func() { f.setFollowThreads(w.followCheck.Active()) })

	// A pop of the user's own (the header's Back, Escape, a swipe) is the
	// flow's Back, which drops a call under way.
	w.nav.ConnectPopped(func(page *adw.NavigationPage) {
		p, ok := ewayPageOfTag(page.Tag())
		n := len(w.shown)
		if !ok || w.closed || n == 0 || w.shown[n-1] != p || f.top() != p {
			return
		}
		w.shown = w.shown[:n-1]
		f.back()
	})

	w.ConnectClosed(func() {
		w.closed = true
		f.close()
	})
}

// Present shows the dialog over parent; the first time, the flow delivers
// its initial state.
func (w *EwayWizard) Present(parent gtk.Widgetter) {
	w.Dialog.Present(parent)
	if w.started {
		return
	}
	w.started = true
	w.flow.start()
}

// showPages mirrors the flow's page stack in the navigation view: a push,
// a pop to a page, or a replacement (the edit dialog's first page).
func (w *EwayWizard) showPages(pages []crm.WizardPage) {
	cur := w.shown
	w.shown = pages
	n := len(pages)
	switch {
	case n == 0 || slices.Equal(pages, cur):
		return
	case n == len(cur)+1 && slices.Equal(pages[:n-1], cur):
		w.nav.PushByTag(ewayPageTags[pages[n-1]])
	case n < len(cur) && slices.Equal(cur[:n], pages):
		w.nav.PopToTag(ewayPageTags[pages[n-1]])
	default:
		tags := make([]string, n)
		for i, p := range pages {
			tags[i] = ewayPageTags[p]
		}
		w.nav.ReplaceWithTags(tags)
	}
	if pages[n-1] == crm.PageCredentials {
		// The address the detection found (or the edited account's).
		w.addressRow.SetSubtitle(w.flow.serviceURL())
	}
}

// showBusy shows a call under way (its progress text) or none: the pages
// wait with their fields and buttons insensitive; Back and the close
// button stay usable.
func (w *EwayWizard) showBusy(progress string) {
	w.busy = progress != ""
	for _, pv := range w.pages {
		if w.busy {
			pv.progressLabel.SetText(progress)
		}
		pv.progress.SetVisible(w.busy)
	}
	w.applyService()
	for _, wd := range []gtk.Widgetter{w.nameRow, w.userRow, w.passwordRow, w.followRow} {
		gtk.BaseWidget(wd).SetSensitive(!w.busy)
	}
	w.pages[crm.PageCredentials].button.SetSensitive(!w.busy)
}

// applyService is the address page's field and Next.
func (w *EwayWizard) applyService() {
	w.serviceRow.SetSensitive(!w.busy)
	w.pages[crm.PageService].button.SetSensitive(w.serviceOK && !w.busy)
}

// showBanner shows a page's banner; "" hides it.
func (w *EwayWizard) showBanner(p crm.WizardPage, text string) {
	b := w.pages[p].banner
	if text != "" {
		b.SetTitle(text)
	}
	b.SetRevealed(text != "")
}

// showProblem flags field and clears the others.
func (w *EwayWizard) showProblem(field crm.WizardField) {
	for _, fw := range []struct {
		field crm.WizardField
		row   gtk.Widgetter
	}{{crm.FieldService, w.serviceRow}, {crm.FieldUser, w.userRow}, {crm.FieldPassword, w.passwordRow}} {
		base := gtk.BaseWidget(fw.row)
		if fw.field == field {
			base.AddCSSClass("error")
		} else {
			base.RemoveCSSClass("error")
		}
	}
}

func (w *EwayWizard) focus(field crm.WizardField) {
	switch field {
	case crm.FieldService:
		w.serviceRow.GrabFocus()
	case crm.FieldUser:
		w.userRow.GrabFocus()
	case crm.FieldPassword:
		w.passwordRow.GrabFocus()
	}
}
