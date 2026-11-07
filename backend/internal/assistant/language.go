// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package assistant

import "strings"

// languageNames are the English names of the languages the clients ship a
// translation for (po/LINGUAS), by the primary subtag of their BCP 47 tag;
// a language added there belongs here too. The prompts say the name
// ("Answer in Czech"); English, which has no catalog, is "" and the
// prompts' default.
var languageNames = map[string]string{"cs": "Czech"}

// LanguageName is the English name of the UI language the prompts take
// (SystemPrompt, TriageSystemPrompt) for a BCP 47 tag the client sent
// ("cs", "cs-CZ", also a POSIX locale such as "cs_CZ.UTF-8"): the name of
// the language's primary subtag when the clients have a catalog for it,
// "" (English) for English, the POSIX "C" and any other language, as the
// GTK client's uiLanguage decides it.
func LanguageName(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(tag, "-_.@"); i >= 0 {
		tag = tag[:i]
	}
	return languageNames[tag]
}
