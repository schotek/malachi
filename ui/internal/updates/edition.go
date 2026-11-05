// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package updates

// Edition is where this copy of Malachi Mail came from, as the About
// window says under the update line: the website's packages (the macOS
// DMG, the Windows installer, every Linux package and build), the Mac App
// Store or the Microsoft Store (docs/plans/mac-app-store.md). Each client
// knows its own: macOS from Info.plist (MalachiDistribution), Windows
// from its MSIX package identity, GTK always the website.
type Edition int

const (
	EditionWebsite Edition = iota
	EditionMacAppStore
	EditionMicrosoftStore
)

// EditionLine is the About window's line for e.
func EditionLine(e Edition, tr Translator) string {
	switch e {
	case EditionMacAppStore:
		// TRANSLATORS: In the About window: this copy came from Apple's Mac App Store.
		return tr.T("Version from the Mac App Store")
	case EditionMicrosoftStore:
		// TRANSLATORS: In the About window: this copy came from the Microsoft Store.
		return tr.T("Version from the Microsoft Store")
	default:
		// TRANSLATORS: In the About window: this copy came from the website; malachimail.app stays as it is.
		return tr.T("Version from malachimail.app")
	}
}
