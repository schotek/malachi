// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package accountservices holds the rules for the calendar and the
// contacts of a mail account (docs/plans/account-services.md): which of
// them an account can sync, the wizard's offer to switch them on before
// the first sign-in, and the fallback offer in the Calendar and the
// Contacts window for an account that can sync one and has it off, until
// the user says Not Now.
//
// Port of macos MalachiCore/AccountServices/AccountServices.swift (the
// reference) with its tests. It is pure: no gotk4, no RPC, no settings;
// the callers pass the account and the entries of the key
// account-services-dismissed.
//
// Only a graph account offers anything today: its capabilities say what it
// can have (calendar, contacts always), its configuration whether it is on
// (calendar.sync, graph.contacts.sync; nil is off). A CalDAV account
// decides both in its own wizard; an IMAP account (Gmail included) has
// neither yet.
package accountservices

import (
	"fmt"
	"slices"
	"strings"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Translator translates a msgid of the malachi domain: T a plain one, N a
// plural form for n, C one disambiguated by a context. GTK passes i18n.Tr;
// the tests pass one that returns the msgid.
type Translator interface {
	T(msgid string) string
	N(msgid, plural string, n int) string
	C(context, msgid string) string
}

// Service is what an account can sync besides its mail.
type Service int

// The services, in their order everywhere.
const (
	Calendar Service = iota
	Contacts
)

// Services are all services in their order.
var Services = []Service{Calendar, Contacts}

// String is the service's name in a dismissal entry ("calendar",
// "contacts").
func (s Service) String() string {
	if s == Contacts {
		return "contacts"
	}
	return "calendar"
}

// parseService reads a service's name; false for anything else.
func parseService(name string) (Service, bool) {
	for _, s := range Services {
		if s.String() == name {
			return s, true
		}
	}
	return 0, false
}

// Capability is the capability that says the account can have it.
func (s Service) Capability() api.AccountCapability {
	if s == Contacts {
		return api.CapabilityContacts
	}
	return api.CapabilityCalendar
}

// Offered are the services the account can sync, for the wizard and the
// fallback offer: an account of kind graph with the capability. caps nil
// (a daemon from before capabilities) is api.MailCapabilities, which has
// neither.
func Offered(cfg api.AccountConfig, caps []api.AccountCapability) []Service {
	if cfg.Protocol() != api.AccountGraph {
		return nil
	}
	if caps == nil {
		caps = api.MailCapabilities
	}
	var out []Service
	for _, s := range Services {
		if slices.Contains(caps, s.Capability()) {
			out = append(out, s)
		}
	}
	return out
}

// OfferedForNew is Offered for an account not added yet (the wizard has
// no capabilities): the daemon gives every graph account both
// (capabilitiesFor).
func OfferedForNew(cfg api.AccountConfig) []Service {
	return Offered(cfg, []api.AccountCapability{api.CapabilityCalendar, api.CapabilityContacts})
}

// IsOn is the service's switch in the configuration: calendar.sync,
// graph.contacts.sync (or a CalDAV account's caldav.contacts, as
// contactsctl.SyncOn).
func IsOn(cfg api.AccountConfig, s Service) bool {
	if s == Calendar {
		return cfg.Calendar != nil && cfg.Calendar.Sync
	}
	switch cfg.Protocol() {
	case api.AccountGraph:
		return cfg.Graph != nil && cfg.Graph.Contacts != nil && cfg.Graph.Contacts.Sync
	case api.AccountCalDAV:
		return cfg.CalDAV != nil && cfg.CalDAV.Contacts
	}
	return false
}

// Apply is cfg with the services of on switched on (true) or off (false);
// a service not in on stays as it is. Only a graph account changes.
// Switching on writes the same as Preferences
// (calendarctl.CalendarAccountController.SetSync, contactsctl's
// withSync): the calendar keeps its other settings. Switching off leaves
// an absent part absent (nil is off already). cfg is not changed: the
// parts written are copies.
func Apply(cfg api.AccountConfig, on map[Service]bool) api.AccountConfig {
	if cfg.Protocol() != api.AccountGraph {
		return cfg
	}
	for _, s := range Services {
		want, ok := on[s]
		if !ok {
			continue
		}
		switch s {
		case Calendar:
			if want || cfg.Calendar != nil {
				cal := api.CalendarConfig{}
				if cfg.Calendar != nil {
					cal = *cfg.Calendar
					cal.DisabledCalendars = slices.Clone(cal.DisabledCalendars)
				}
				cal.Sync = want
				cfg.Calendar = &cal
			}
		case Contacts:
			if want || (cfg.Graph != nil && cfg.Graph.Contacts != nil) {
				g := api.GraphConfig{}
				if cfg.Graph != nil {
					g = *cfg.Graph
				}
				g.Contacts = &api.ContactsConfig{Sync: want}
				cfg.Graph = &g
			}
		}
	}
	return cfg
}

// Off are the services the account can sync and has off (the fallback
// offer).
func Off(cfg api.AccountConfig, caps []api.AccountCapability) []Service {
	var out []Service
	for _, s := range Offered(cfg, caps) {
		if !IsOn(cfg, s) {
			out = append(out, s)
		}
	}
	return out
}

// DismissKey is the entry of account-services-dismissed for Not Now of s
// on the account: "<account id>:<service>".
func DismissKey(id api.AccountID, s Service) string { return string(id) + ":" + s.String() }

// ShowPrompt reports whether the Calendar (Calendar) or the Contacts
// window (Contacts) offers to switch the service on for the account: it is
// enabled, can sync the service, has it off, and Not Now was not said.
func ShowPrompt(a api.Account, s Service, dismissed []string) bool {
	return a.Enabled && slices.Contains(Off(a.Config, a.Capabilities), s) && !slices.Contains(dismissed, DismissKey(a.ID, s))
}

// Prompts are the accounts the Calendar or the Contacts window offers the
// service for, in their order.
func Prompts(accounts []api.Account, s Service, dismissed []string) []api.Account {
	var out []api.Account
	for _, a := range accounts {
		if ShowPrompt(a, s, dismissed) {
			out = append(out, a)
		}
	}
	return out
}

// Dismissing is dismissed with Not Now of s on the account added (once).
// dismissed is not changed.
func Dismissing(dismissed []string, id api.AccountID, s Service) []string {
	key := DismissKey(id, s)
	if slices.Contains(dismissed, key) {
		return dismissed
	}
	return append(slices.Clone(dismissed), key)
}

// Pruned is dismissed without the entries of accounts that no longer
// exist (an entry that is not "<id>:<service>" goes too). dismissed is
// not changed.
func Pruned(dismissed []string, accounts []api.AccountID) []string {
	var out []string
	for _, e := range dismissed {
		i := strings.LastIndexByte(e, ':')
		if i < 0 {
			continue
		}
		if _, ok := parseService(e[i+1:]); !ok {
			continue
		}
		if slices.Contains(accounts, api.AccountID(e[:i])) {
			out = append(out, e)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Texts
// ---------------------------------------------------------------------------

// GroupTitle heads the wizard's group of switches.
func GroupTitle(tr Translator) string {
	// TRANSLATORS: the account wizard's group of switches for the calendar
	// and the contacts of a new account.
	return tr.T("Also Sync from This Account")
}

// SwitchTitle is the wizard's switch of a service (Preferences' wording).
func SwitchTitle(s Service, tr Translator) string {
	if s == Contacts {
		return tr.T("Sync Contacts")
	}
	return tr.T("Sync This Account’s Calendars")
}

// LaterNote is the note under the wizard's group.
func LaterNote(tr Translator) string { return tr.T("You can change this later in Preferences.") }

// OffLine is the fallback offer's sentence for the account's name.
func OffLine(s Service, account string, tr Translator) string {
	if s == Contacts {
		// TRANSLATORS: %s is the name of a mail account
		return fmt.Sprintf(tr.T("Contacts sync is off for %s"), account)
	}
	// TRANSLATORS: %s is the name of a mail account
	return fmt.Sprintf(tr.T("Calendar sync is off for %s"), account)
}

// TurnOn is the fallback offer's button that switches the service on.
func TurnOn(tr Translator) string { return tr.T("Turn On") }

// NotNow is the fallback offer's button that hides it for the account.
func NotNow(tr Translator) string { return tr.T("Not Now") }

// MailOnly is the wizard's offer after a sign-in that asked for a service
// did not succeed.
func MailOnly(tr Translator) string { return tr.T("Try Again with Mail Only") }
