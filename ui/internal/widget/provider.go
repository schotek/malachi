// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/backend/pkg/api"
)

// Providers whose sign-in belongs to GNOME Online Accounts, as
// account.linked names them (docs/api.md §4.1). An account of theirs has
// no password to ask for and no servers of the user's to edit; the
// sign-in problems of such an account are fixed in Settings → Online
// Accounts, not here.
const (
	ProviderMicrosoft365 = "microsoft365"
	ProviderGoogle       = "google"
)

// AccountProvider says which Online Accounts provider an account signs
// in through: Microsoft 365 for a Graph account, the oauth2 provider for
// an IMAP account with a GOA token, "" for a password account.
func AccountProvider(cfg api.AccountConfig) string {
	switch {
	case cfg.Protocol() == api.AccountGraph:
		return ProviderMicrosoft365
	case cfg.OAuth2 != nil && cfg.OAuth2.Source == api.OAuth2SourceGOA:
		return cfg.OAuth2.Provider
	}
	return ""
}

// GOAOwned reports an account whose sign-in lives in GNOME Online
// Accounts.
func GOAOwned(cfg api.AccountConfig) bool { return AccountProvider(cfg) != "" }

// ProviderName is the provider's name as shown to the user. These are
// brand names and are not translated.
func ProviderName(provider string) string {
	switch provider {
	case ProviderMicrosoft365:
		return "Microsoft 365"
	case ProviderGoogle:
		return "Google"
	}
	return ""
}

// providerIconName is the icon GNOME Online Accounts installs for the
// provider, "" for an unknown one.
func providerIconName(provider string) string {
	switch provider {
	case ProviderMicrosoft365:
		return "goa-account-ms365-symbolic"
	case ProviderGoogle:
		return "goa-account-google-symbolic"
	}
	return ""
}

// ProviderIcon is the provider's icon when the theme has it, the generic
// mail icon otherwise (also for a password account).
func ProviderIcon(provider string) string {
	if name := providerIconName(provider); name != "" {
		if display := gdk.DisplayGetDefault(); display != nil && gtk.IconThemeGetForDisplay(display).HasIcon(name) {
			return name
		}
	}
	return "mail-unread-symbolic"
}
