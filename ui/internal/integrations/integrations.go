// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package integrations is the view logic of the two account pages of the
// preferences: Accounts, which lists the mail accounts (kind imap, the
// empty kind, and graph), and Integrations, which lists every other
// account — the services beside the mail, today Jira (kind jira), CalDAV
// calendars (kind caldav) and eWay-CRM (kind eway); a kind added later
// lands there by itself — and the catalogue of services that can be
// added.
//
// Both pages show rows of the same shape (Row): the switch, the editor
// and the sentence of the remove confirmation depend on the kind. The
// order of the accounts is one list owned by the daemon; a page that
// reorders its own rows sends the whole list (FullOrder), mail accounts
// first, so account.reorder never has to guess where the other page's
// accounts go.
//
// The package is pure (no GTK, no gettext, no cgo: the caller passes a
// Translator) so its rules are tested without a display and the other
// clients port it one to one: macOS MalachiCore/Model/Integrations.swift,
// Windows Malachi.Core/Model/Integrations.cs. GTK uses it with i18n.Tr.
package integrations

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
	"github.com/schotek/malachi/ui/internal/crm"
	"github.com/schotek/malachi/ui/internal/jira"
)

// Translator translates a msgid of the malachi domain. GTK passes
// i18n.Tr; the tests pass one that returns the msgid.
type Translator interface {
	T(msgid string) string
}

// ShowsInAccounts reports an account the Accounts page lists: a mail
// account (kind imap, also the empty kind, or graph).
func ShowsInAccounts(a api.Account) bool {
	switch a.Config.Protocol() {
	case api.AccountIMAP, api.AccountGraph:
		return true
	}
	return false
}

// ShowsInIntegrations reports an account the Integrations page lists:
// every account that is not a mail account.
func ShowsInIntegrations(a api.Account) bool { return !ShowsInAccounts(a) }

// Split divides accounts into the mail accounts and the integrations,
// each in the order of accounts.
func Split(accounts []api.Account) (mail, integrations []api.Account) {
	for _, a := range accounts {
		if ShowsInAccounts(a) {
			mail = append(mail, a)
		} else {
			integrations = append(integrations, a)
		}
	}
	return mail, integrations
}

// Page is one of the two account pages.
type Page int

// The pages.
const (
	PageAccounts Page = iota
	PageIntegrations
)

// FullOrder is the order account.reorder gets when page was reordered to
// order: the mail accounts first, then the integrations. The reordered
// page's accounts follow order; the other page's keep their order in all.
// An id of order that is not in all, or that belongs to the other page,
// is ignored, a repeated one counts once, and an account of the page
// that order omits follows the listed ones in its order in all. The
// result names every account of all exactly once.
func FullOrder(all []api.Account, page Page, order []api.AccountID) []api.AccountID {
	mail, other := Split(all)
	ordered := func(group []api.Account, reordered bool) []api.AccountID {
		ids := make([]api.AccountID, 0, len(group))
		if !reordered {
			for _, a := range group {
				ids = append(ids, a.ID)
			}
			return ids
		}
		in := make(map[api.AccountID]bool, len(group))
		for _, a := range group {
			in[a.ID] = true
		}
		seen := make(map[api.AccountID]bool, len(group))
		for _, id := range order {
			if in[id] && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		for _, a := range group {
			if !seen[a.ID] {
				seen[a.ID] = true
				ids = append(ids, a.ID)
			}
		}
		return ids
	}
	out := ordered(mail, page == PageAccounts)
	return append(out, ordered(other, page == PageIntegrations)...)
}

// Icons of the kinds (GTK symbolic icon names; the other clients map
// them to their own). A mail account's row shows its provider's icon
// when the theme has one (widget.ProviderIcon); MailIcon is the fallback.
const (
	MailIcon   = "mail-unread-symbolic"
	JiraIcon   = "checkbox-checked-symbolic" // Adwaita has no ticket
	CalDAVIcon = "x-office-calendar-symbolic"
	EwayIcon   = crm.Icon // Adwaita has no CRM
)

// Service is one entry of the catalogue on the Integrations page.
type Service struct {
	Kind        api.AccountKind
	Name        string // the service's name; Jira and eWay-CRM are brands, not translated
	Description string
	Icon        string // GTK symbolic icon name
}

// Catalog is the services that can be added, in the order the page
// lists them.
func Catalog(tr Translator) []Service {
	return []Service{
		{
			Kind: api.AccountJira, Name: jiraName,
			// TRANSLATORS: Preferences → Integrations: what a Jira
			// account brings ("issues" as in Jira's own word).
			Description: tr.T("Issues read as mail, comments and status changes"),
			Icon:        JiraIcon,
		},
		{
			Kind: api.AccountCalDAV,
			// TRANSLATORS: Preferences → Integrations: the name of the
			// service in the list of services that can be added.
			Name:        tr.T("CalDAV Calendar"),
			Description: tr.T("Nextcloud, iCloud, Fastmail and other CalDAV servers"),
			Icon:        CalDAVIcon,
		},
		{
			Kind: api.AccountEway, Name: crm.Brand,
			// TRANSLATORS: Preferences → Integrations: what an eWay-CRM
			// account brings ("deals" as eWay-CRM's sales opportunities).
			Description: tr.T("Save messages to your CRM with their contacts, companies and deals"),
			Icon:        EwayIcon,
		},
	}
}

// jiraName is the brand, never translated.
const jiraName = "Jira"

// Editor is what a row's Edit button opens.
type Editor int

// The editors.
const (
	// EditorNone: the row has no Edit button (a kind this client does
	// not know).
	EditorNone Editor = iota
	// EditorMailWizard is the mail account assistant in its edit mode.
	EditorMailWizard
	// EditorJira is a Jira account's settings (the token assistant when
	// a sign-in route asks for the token).
	EditorJira
	// EditorCalendarPage switches the preferences to the Calendar page,
	// where a CalDAV account's server, password, certificate and
	// calendars are.
	EditorCalendarPage
	// EditorEway is the eWay-CRM account assistant in its edit mode
	// (ui/internal/accountwizard eway_flow.go): the user name, the
	// password and the name; the service address stays.
	EditorEway
)

// Row is what an account's row on either page shows besides its status:
// plain texts (nothing is markup), the icon, whether it has the Enabled
// switch (account.setEnabled pauses the sync, or an eWay-CRM account's
// saving; CalDAV has no pause), what
// its Edit button opens and is called, and the body of its remove
// confirmation.
type Row struct {
	ID           api.AccountID
	Kind         api.AccountKind
	Title        string
	Subtitle     string
	Icon         string
	OffersSwitch bool
	Editor       Editor
	EditLabel    string // the Edit button's tooltip
	RemoveBody   string
}

// RowFor is the row of account a.
//
// Title: the account name as the user typed it (nothing trimmed), else
// the Jira site's host, else the address. Subtitle: the host of the Jira
// site or of the CalDAV server, the host and path of the eWay-CRM service,
// else — and when the title shows the host already — the address.
func RowFor(a api.Account, tr Translator) Row {
	kind := a.Config.Protocol()
	r := Row{ID: a.ID, Kind: kind, Title: title(a), OffersSwitch: true}
	r.Subtitle = a.Config.Email
	if host := host(a); host != "" && host != r.Title {
		r.Subtitle = host
	}
	switch kind {
	case api.AccountIMAP, api.AccountGraph:
		r.Icon, r.Editor, r.EditLabel = MailIcon, EditorMailWizard, tr.T("Edit Account")
		// TRANSLATORS: %s is the account's e-mail address.
		r.RemoveBody = fmt.Sprintf(tr.T("%s will be removed from Malachi Mail. Mail on the server is not affected."), a.Config.Email)
	case api.AccountJira:
		r.Icon, r.Editor, r.EditLabel = JiraIcon, EditorJira, tr.T("Edit Account")
		// TRANSLATORS: %s is the account's name (or its site's host).
		r.RemoveBody = fmt.Sprintf(tr.T("%s will be removed from Malachi Mail. Issues on the Jira site are not affected."), r.Title)
	case api.AccountCalDAV:
		r.Icon, r.Editor, r.EditLabel = CalDAVIcon, EditorCalendarPage, calendarSettings(tr)
		r.OffersSwitch = false
		// TRANSLATORS: %s is the calendar account's name.
		r.RemoveBody = fmt.Sprintf(tr.T("%s will be removed from Malachi Mail. Calendars on the server are not affected."), r.Title)
	case api.AccountEway:
		r.Icon, r.Editor, r.EditLabel = EwayIcon, EditorEway, tr.T("Edit Account")
		// TRANSLATORS: %s is the eWay-CRM account's name.
		r.RemoveBody = fmt.Sprintf(tr.T("%s will be removed from Malachi Mail. Messages saved in eWay-CRM stay there."), r.Title)
	default:
		r.Icon = MailIcon
		// TRANSLATORS: %s is the account's name.
		r.RemoveBody = fmt.Sprintf(tr.T("%s will be removed from Malachi Mail."), r.Title)
	}
	return r
}

// title is the name of the row: the name, the Jira site's host, the
// address.
func title(a api.Account) string {
	if a.Config.Name != "" {
		return a.Config.Name
	}
	if h := jira.SiteHost(a.Config); h != "" {
		return h
	}
	return a.Config.Email
}

// host is the host the subtitle shows: a Jira account's site, a CalDAV
// account's server, an eWay-CRM account's service (with its path, which
// tells two instances on one host apart); empty for anything else.
func host(a api.Account) string {
	switch a.Config.Protocol() {
	case api.AccountJira:
		return jira.SiteHost(a.Config)
	case api.AccountCalDAV:
		if a.Config.CalDAV == nil {
			return ""
		}
		u, err := url.Parse(strings.TrimSpace(a.Config.CalDAV.ServerURL))
		if err != nil {
			return ""
		}
		return jira.Clean(strings.ToLower(u.Hostname()))
	case api.AccountEway:
		return crm.ServiceHost(a)
	}
	return ""
}

// PageTexts are the fixed texts of the Integrations page.
type PageTexts struct {
	Title            string // the page's title in the preferences' navigation
	Description      string // under the title
	ConnectedTitle   string // the group of the added services
	AvailableTitle   string // the group of the catalogue
	EmptyTitle       string // the connected group's placeholder row
	EmptySubtitle    string
	Add              string // a catalogue row's button
	CalendarSettings string // a CalDAV row's Edit tooltip
}

// Texts are the Integrations page's texts.
func Texts(tr Translator) PageTexts {
	return PageTexts{
		// TRANSLATORS: a page of the preferences: services beside the
		// mail, such as Jira, CalDAV calendars and eWay-CRM.
		Title:          tr.T("Integrations"),
		Description:    tr.T("Services beside your mail: issue trackers, calendars and CRM"),
		ConnectedTitle: tr.T("Connected Services"),
		AvailableTitle: tr.T("Available Services"),
		EmptyTitle:     tr.T("No integrations yet"),
		EmptySubtitle:  tr.T("Add one from the list below"),
		// TRANSLATORS: the button of a service in Preferences →
		// Integrations → Available Services.
		Add:              tr.T("Add…"),
		CalendarSettings: calendarSettings(tr),
	}
}

// calendarSettings names what a CalDAV row's Edit opens.
func calendarSettings(tr Translator) string {
	// TRANSLATORS: tooltip of a CalDAV account's Edit button in
	// Preferences → Integrations; it opens the Calendar page.
	return tr.T("Calendar Settings")
}
