// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

// WebsiteOnlyText is the sentence the Mac App Store build of the macOS
// client shows in Settings → AI instead of the rows of the In App target,
// the ChatGPT provider and the board's triage: its App Sandbox lets it
// run neither the user's Claude Code nor Codex (docs/plans/mac-app-store.md
// §3.3). GTK and Windows have no such build and never show it; the macOS
// client reads the msgid from here (Windows: windows/parity-exclusions.txt).
func WebsiteOnlyText(tr Translator) string {
	// TRANSLATORS: Settings → AI of Malachi Mail from the Mac App Store. "In App" is the third choice of "Open In" in the other builds; malachimail.app is the website and stays as it is.
	return tr.T("The In App assistant, the board's triage and Suggest Reply are only in Malachi Mail from malachimail.app.")
}
