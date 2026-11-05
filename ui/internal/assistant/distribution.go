// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistant

// WebsiteFeatures holds the texts with which the Mac App Store build of
// the macOS client says what Malachi Mail from malachimail.app adds: its
// App Sandbox lets it run neither the user's Claude Code nor Codex
// (docs/plans/mac-app-store.md §3.3). A group at the top of Settings → AI
// and a link in the status bar before Report an Issue open one dialog.
// GTK and Windows have no such build and never show them; the macOS
// client reads the msgids from here (Windows: windows/parity-exclusions.txt).
// The dialog only opens the website, it never downloads or installs
// anything (App Review Guidelines 2.4.5).
type WebsiteFeatures struct {
	GroupTitle  string
	GroupBody   string
	More        string
	StatusLink  string
	DialogTitle string
	DialogIntro string
	// Features are the dialog's list, one line each.
	Features []string
	Free     string
	Data     string
	Open     string
	Close    string
}

// WebsiteFeaturesTexts returns the texts of the pointer to the version
// from the website.
func WebsiteFeaturesTexts(tr Translator) WebsiteFeatures {
	return WebsiteFeatures{
		// TRANSLATORS: Group title in Settings → AI of Malachi Mail from the Mac App Store.
		GroupTitle: tr.T("More Assistant Features"),
		// TRANSLATORS: Settings → AI of Malachi Mail from the Mac App Store; malachimail.app is the website and stays as it is.
		GroupBody: tr.T("Malachi Mail from malachimail.app also has the assistant right in the app with Claude or ChatGPT, the board's triage and suggested replies. This version from the App Store can't run them because it runs in a sandbox."),
		// TRANSLATORS: Button opening the list of what the version from the website adds.
		More: tr.T("More…"),
		// TRANSLATORS: Link in the status bar of Malachi Mail from the Mac App Store, before Report an Issue.
		StatusLink: tr.T("More AI Features"),
		// TRANSLATORS: Dialog title; malachimail.app is the website and stays as it is.
		DialogTitle: tr.T("Features of the Version from malachimail.app"),
		DialogIntro: tr.T("This version from the Mac App Store runs in the macOS sandbox. Because of it, it can't start Claude Code or Codex installed on your Mac, so it lacks:"),
		Features: []string{
			tr.T("The assistant in the app's panel with Claude Code or ChatGPT (Codex)"),
			tr.T("Rewriting and writing a message with the assistant, and searching in your own words"),
			tr.T("The board's triage, by hand and automatically, and Suggest Reply"),
			tr.T("Handing an attachment to Claude as a file"),
		},
		Free: tr.T("Both versions are free. The version from the website updates itself, this one through the App Store."),
		Data: tr.T("Accounts and settings don't move between the two versions yet: after switching, add your accounts again."),
		// TRANSLATORS: Button opening the website in the browser; malachimail.app stays as it is.
		Open:  tr.T("Open malachimail.app"),
		Close: tr.T("Close"),
	}
}
