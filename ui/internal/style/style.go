// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package style applies application-wide appearance settings: the libadwaita
// color scheme and a CSS provider for everything that cannot be expressed
// per widget (message body zoom and font, list decorations).
//
// Because the provider is installed on the display, reloading it restyles
// every open window at once; windows need no subscriptions of their own.
package style

import (
	"fmt"
	"strings"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"github.com/schotek/malachi/ui/internal/settings"
)

// Apply installs the color scheme and CSS from s and keeps them in sync with
// later changes. Call it once from the application's startup handler, after
// GTK and libadwaita are initialised.
func Apply(s *settings.Store) {
	sm := adw.StyleManagerGetDefault()
	applyScheme := func() { sm.SetColorScheme(adwColorScheme(s.ColorScheme())) }
	applyScheme()
	s.OnChanged(settings.KeyColorScheme, applyScheme)

	provider := gtk.NewCSSProvider()
	gtk.StyleContextAddProviderForDisplay(gdk.DisplayGetDefault(), provider,
		uint(gtk.STYLE_PROVIDER_PRIORITY_APPLICATION))
	reload := func() {
		provider.LoadFromString(CSS(s.TextZoom(), s.MonospacePlainText(), s.MonochromeAvatars()))
	}
	reload()
	for _, key := range []string{settings.KeyTextZoom, settings.KeyMonospacePlainText, settings.KeyMonochromeAvatars} {
		s.OnChanged(key, reload)
	}
}

func adwColorScheme(c settings.ColorScheme) adw.ColorScheme {
	switch c {
	case settings.ColorSchemeLight:
		return adw.ColorSchemeForceLight
	case settings.ColorSchemeDark:
		return adw.ColorSchemeForceDark
	default:
		return adw.ColorSchemeDefault
	}
}

// CSS renders the application stylesheet for the given settings. Percent
// font sizes are relative to the parent, so zoom composes with the user's
// system font. The provider has application priority, so its rules win over
// the libadwaita stylesheet (e.g. the per-sender avatar colour classes).
func CSS(textZoomPercent int, monospace, monochromeAvatars bool) string {
	var b strings.Builder
	b.WriteString(".unread-dot { min-width: 8px; min-height: 8px; border-radius: 4px; background-color: @accent_bg_color; }\n")
	// Folder rows are denser than libadwaita's action rows: the sidebar
	// lists many one-line entries.
	b.WriteString(".navigation-sidebar row.folder-row { min-height: 32px; padding-top: 2px; padding-bottom: 2px; }\n")
	b.WriteString("row.folder-row > box.header { min-height: 0; padding-top: 0; padding-bottom: 0; }\n")
	// Separators between messages (show-separators on the list in window.blp).
	// The sidebar style rounds its rows and insets them, which would bend the
	// separator into a shallow arc and leave a gap under it, so the message
	// rows are squared and flush; selection stays the sidebar's subtle tint,
	// now spanning the full width. The last row keeps no trailing line.
	b.WriteString("list.message-list > row { border-radius: 0; margin: 0; }\n")
	b.WriteString("list.message-list > row:last-child { border-bottom: none; }\n")
	// Account reordering in preferences: the insertion line is a box-shadow
	// rather than a border so the row does not change height, and therefore
	// does not twitch, while the pointer moves over it.
	b.WriteString(".drag-handle { color: alpha(@window_fg_color, 0.55); }\n")
	b.WriteString("row.account-row.dragging { opacity: 0.4; }\n")
	b.WriteString("row.account-row.drop-above { box-shadow: inset 0 2px 0 0 @accent_bg_color; }\n")
	b.WriteString("row.account-row.drop-below { box-shadow: inset 0 -2px 0 0 @accent_bg_color; }\n")
	fmt.Fprintf(&b, ".message-body { font-size: %d%%; }\n", textZoomPercent)
	if monospace {
		b.WriteString(".message-body { font-family: monospace; }\n")
	}
	if monochromeAvatars {
		// Neutral tint of the foreground colour: readable in light and dark.
		b.WriteString("avatar { background-image: none; background-color: alpha(@window_fg_color, 0.12); color: alpha(@window_fg_color, 0.8); }\n")
	}
	return b.String()
}
