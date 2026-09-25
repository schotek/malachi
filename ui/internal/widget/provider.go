// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package widget

import (
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/signin"
)

// Which provider an account signs in with, and whether that sign-in lives
// in GNOME Online Accounts, in the backend's own browser sign-in or is a
// password, is package signin's call (signin.Provider, signin.KindOf);
// this file only picks the icon.

// providerIconName is the icon GNOME Online Accounts installs for the
// provider, "" for an unknown one.
func providerIconName(provider string) string {
	switch provider {
	case signin.ProviderMicrosoft365:
		return "goa-account-ms365-symbolic"
	case signin.ProviderGoogle:
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
