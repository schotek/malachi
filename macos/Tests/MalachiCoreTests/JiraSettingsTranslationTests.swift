// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The Czech side of ui/internal/jira/settings.go: the cases
// settings_test.go checks with a fake catalogue (a context entry must win
// over the plain msgid), here against the catalogues `make locale`
// generates from po/. That the port asks for exactly the msgids of the Go
// package, and that each has a Czech translation, is checked for the
// whole of MalachiCore/Jira by JiraTranslationTests.

/// The generated catalogues (`MALACHI_LOCALE_DIR`, as `make test-macos`
/// exports it); nil skips the tests.
private let settingsLocaleDir: URL? = {
    guard let dir = ProcessInfo.processInfo.environment[Catalogue.localeDirEnv], !dir.isEmpty else { return nil }
    let url = URL(fileURLWithPath: dir, isDirectory: true)
    return Catalogue.availableLanguages(in: url).contains("cs") ? url : nil
}()

private let settingsLocaleHint: Comment = "run `make -C macos locale` and export MALACHI_LOCALE_DIR"

struct JiraSettingsTranslationTests {
    private var cs: Catalogue { Catalogue.load(from: settingsLocaleDir!, languages: ["cs"]) }

    /// settings_test.go TestSettingsTexts and TestStatusGroups in Czech.
    @Test(.enabled(if: settingsLocaleDir != nil, settingsLocaleHint))
    func sectionsAndRows() {
        let cs = cs
        #expect(cs.translate("Jira Account") == "Účet Jira")
        #expect(cs.translate("Replace Token…") == "Nahradit token…")
        #expect(cs.translate("Show Status and Assignee Changes") == "Zobrazovat změny stavu a řešitele")
        #expect(cs.translate("Closed Statuses") == "Uzavřené stavy")
        #expect(cs.translate("Notification E-mails") == "Notifikační e-maily")
        #expect(cs.translate("Comments Posted by Bots") == "Komentáře od botů")
        #expect(cs.translate("Bot Accounts") == "Účty botů")
        #expect(cs.translate("Hidden Lines") == "Skryté řádky")
        #expect(cs.translate("Name Prefixes") == "Předpony jmen")
        #expect(cs.context("status category", "Done") == "Hotovo")
        #expect(cs.context("status category", "To Do") == "K řešení")
        #expect(cs.context("status category", "Other") == "Ostatní")
    }

    /// settings_test.go TestSuggestions and TestCheckEntry in Czech: the
    /// entry and the reason go into the sentence as they are.
    @Test(.enabled(if: settingsLocaleDir != nil, settingsLocaleHint))
    func formattedSentences() {
        let cs = cs
        #expect(cs.translate("Add %s", [Jira.suggestedBotName]) == "Přidat Issue Sync – Synchronization for Jira")
        #expect(cs.translate("Add %s", [Jira.suggestedMetadataFilter]) == #"Přidat ^Remote comment create date:.*$"#)
        #expect(cs.translate("This pattern is not valid: %s", ["missing closing )"]) == "Tento vzor není platný: missing closing )")
        // A reason with a per cent sign is text, not a format.
        #expect(cs.translate("Add %s", ["100 %d %s"]) == "Přidat 100 %d %s")
    }

    @Test(.enabled(if: settingsLocaleDir != nil, settingsLocaleHint))
    func plurals() {
        let cs = cs
        #expect(cs.plural("Select at most %d status", "Select at most %d statuses", API.Limits.maxJiraStatuses)
                    == "Vyberte nejvýš 64 stavů")
        #expect(cs.plural("Select at most %d status", "Select at most %d statuses", 1) == "Vyberte nejvýš 1 stav")
        #expect(cs.plural("Select at most %d status", "Select at most %d statuses", 3) == "Vyberte nejvýš 3 stavy")
        #expect(cs.plural("The list holds at most %d entry", "The list holds at most %d entries", API.Limits.maxJiraListEntries)
                    == "Seznam pojme nejvýš 32 položek")
        #expect(cs.plural("The list holds at most %d entry", "The list holds at most %d entries", 1) == "Seznam pojme nejvýš 1 položku")
        #expect(cs.plural("The list holds at most %d entry", "The list holds at most %d entries", 2) == "Seznam pojme nejvýš 2 položky")
    }
}
