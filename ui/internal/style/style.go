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
	// The folder sidebar is navigation, not content: it lists many one-line
	// entries and is therefore denser than libadwaita's action rows. Most of
	// the height a row gets is not the label but the 6px libadwaita puts
	// above and below the title box, so that is what comes down first; the
	// row's own floor follows, and the title goes to 88% so the shorter rows
	// do not look crowded. The unread badge keeps the .caption size it has,
	// being outside the title box. Only this list is affected: the message
	// list shares the navigation-sidebar class (window.blp).
	b.WriteString("list.folder-list { padding: 3px 0; }\n")
	b.WriteString("list.folder-list row.folder-row { min-height: 24px; padding-top: 1px; padding-bottom: 1px; }\n")
	b.WriteString("row.folder-row > box.header { min-height: 0; padding-top: 0; padding-bottom: 0; }\n")
	b.WriteString("row.folder-row > box.header > box.title { margin-top: 2px; margin-bottom: 2px; font-size: 88%; }\n")
	// Every icon of the sidebar at once: the folder icons, the fold arrows of
	// rows and headings alike, and the stars.
	b.WriteString("list.folder-list image { -gtk-icon-size: 14px; }\n")
	// The fold arrow and the pin star of a sidebar row. A default button
	// would push the row past the height above, so both are squeezed to the
	// size of their icon.
	b.WriteString("button.folder-twisty, button.folder-star { min-width: 18px; min-height: 18px; padding: 0; margin: 0; }\n")
	// The star waits for the pointer or the keyboard focus. Opacity rather
	// than visibility, so nothing moves under the pointer.
	b.WriteString("row.folder-row button.folder-star { opacity: 0; transition: opacity 150ms; }\n")
	b.WriteString("row.folder-row:hover button.folder-star, row.folder-row:focus-within button.folder-star { opacity: 1; }\n")
	// In the tree a filled star stays visible: it is what says the folder is
	// pinned. In the Favourites section every row is pinned by definition, so
	// a column of stars would say nothing and the star waits there too.
	b.WriteString("row.folder-row:not(.favourite) button.folder-star.starred { opacity: 1; }\n")
	// The remote-image bar above a message: a neutral tint that reads as a
	// notice in light and dark alike (Adw.Banner has room for one button).
	b.WriteString("box.remote-bar { background-color: alpha(@window_fg_color, 0.06); padding: 6px 12px; }\n")
	// Compose header fields (compose.blp): one line each, so the entries
	// and the account drop-down lose the height a stand-alone input would
	// claim and the card supplies the padding the rows no longer have.
	b.WriteString("box.compose-headers > box { padding: 0 12px; }\n")
	b.WriteString("box.compose-headers entry, box.compose-headers dropdown > button { min-height: 30px; }\n")
	b.WriteString("box.compose-headers dropdown > button { padding-left: 0; }\n")
	// Attachment chips under the message headers: two have to fit side by
	// side in a narrow pane, so the buttons drop libadwaita's roomy
	// padding, the labels go down a size and the menu arrow is barely
	// wider than its icon. The name itself is capped in Go (chipNameChars).
	b.WriteString("box.attachment-chip button, button.chip-action { min-height: 0; padding: 3px 8px; }\n")
	b.WriteString("box.attachment-chip image, button.chip-action image { -gtk-icon-size: 14px; }\n")
	b.WriteString("box.attachment-chip label.chip-name, button.chip-action label { font-size: 90%; }\n")
	b.WriteString("box.attachment-chip label.chip-size { font-size: 80%; }\n")
	b.WriteString("box.attachment-chip menubutton.chip-arrow > button { min-width: 16px; padding-left: 2px; padding-right: 2px; }\n")
	// The All / Unread / Flagged switch above the message list: it is a
	// filter, not the column's heading, so it stays out of the way. The
	// toggles lose the height and the roomy padding a stand-alone button
	// claims, the label goes down a size, and the bold weight Adwaita
	// gives buttons comes off — three short words in bold read as a title
	// bar. CSS node names come from AdwToggleGroup (toggle-group > toggle).
	b.WriteString("toggle-group.message-filter toggle { min-height: 0; padding: 3px 12px; }\n")
	b.WriteString("toggle-group.message-filter toggle label { font-size: 90%; font-weight: normal; }\n")
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
