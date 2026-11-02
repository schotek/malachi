// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package unified

// Translator translates a msgid of the malachi domain: T a plain one, N
// a plural form for n, C one disambiguated by a context. GTK passes an
// adapter over i18n.T, i18n.N and i18n.C (i18n.Tr); the tests pass one
// that returns the msgid.
type Translator interface {
	T(msgid string) string
	N(msgid, plural string, n int) string
	C(context, msgid string) string
}

// Title is the item's name in the sidebar and the title of its list.
func Title(tr Translator) string {
	// TRANSLATORS: The sidebar item and the list of the inboxes of every mail account together.
	return tr.T("All Inboxes")
}

// PreferenceTitle is the title of the preference row (key unified-inbox).
func PreferenceTitle(tr Translator) string {
	// TRANSLATORS: A switch in Preferences; "All Inboxes" is the sidebar item's name.
	return tr.T("Show All Inboxes")
}

// PreferenceSubtitle is the subtitle of the preference row.
func PreferenceSubtitle(tr Translator) string {
	// TRANSLATORS: The subtitle of the "Show All Inboxes" switch in Preferences.
	return tr.T("In the sidebar when you have more than one mail account")
}
