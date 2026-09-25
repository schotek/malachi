// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

struct PluralRulesTests {
    @Test func english() {
        #expect(PluralRules.index(language: "en", n: 0) == 1)
        #expect(PluralRules.index(language: "en", n: 1) == 0)
        #expect(PluralRules.index(language: "en", n: 2) == 1)
        #expect(PluralRules.index(language: "en", n: -1) == 0)
        #expect(PluralRules.category(language: "en", n: 1) == "one")
        #expect(PluralRules.category(language: "en", n: 5) == "other")
        #expect(PluralRules.categories(language: "en") == ["one", "other"])
    }

    @Test func czechAndSlovak() {
        for lang in ["cs", "sk", "cs-CZ", "cs_CZ", "CS"] {
            #expect(PluralRules.index(language: lang, n: 1) == 0)
            #expect(PluralRules.index(language: lang, n: 2) == 1)
            #expect(PluralRules.index(language: lang, n: 4) == 1)
            #expect(PluralRules.index(language: lang, n: 5) == 2)
            #expect(PluralRules.index(language: lang, n: 0) == 2)
            #expect(PluralRules.index(language: lang, n: 22) == 2)
        }
        #expect(PluralRules.category(language: "cs", n: 1) == "one")
        #expect(PluralRules.category(language: "cs", n: 3) == "few")
        #expect(PluralRules.category(language: "cs", n: 5) == "other")
    }

    @Test func polish() {
        #expect(PluralRules.category(language: "pl", n: 1) == "one")
        #expect(PluralRules.category(language: "pl", n: 2) == "few")
        #expect(PluralRules.category(language: "pl", n: 5) == "many")
        #expect(PluralRules.category(language: "pl", n: 12) == "many")
        #expect(PluralRules.category(language: "pl", n: 22) == "few")
        #expect(PluralRules.category(language: "pl", n: 0) == "many")
    }

    @Test func russianAndUkrainian() {
        for lang in ["ru", "uk"] {
            #expect(PluralRules.category(language: lang, n: 1) == "one")
            #expect(PluralRules.category(language: lang, n: 21) == "one")
            #expect(PluralRules.category(language: lang, n: 11) == "many")
            #expect(PluralRules.category(language: lang, n: 3) == "few")
            #expect(PluralRules.category(language: lang, n: 13) == "many")
            #expect(PluralRules.category(language: lang, n: 5) == "many")
        }
    }

    @Test func french() {
        #expect(PluralRules.index(language: "fr", n: 0) == 0)
        #expect(PluralRules.index(language: "fr", n: 1) == 0)
        #expect(PluralRules.index(language: "fr", n: 2) == 1)
    }

    @Test func romanian() {
        #expect(PluralRules.category(language: "ro", n: 1) == "one")
        #expect(PluralRules.category(language: "ro", n: 0) == "few")
        #expect(PluralRules.category(language: "ro", n: 19) == "few")
        #expect(PluralRules.category(language: "ro", n: 20) == "other")
        #expect(PluralRules.category(language: "ro", n: 101) == "few")
    }

    @Test func unknownLanguageUsesTheEnglishRule() {
        #expect(PluralRules.index(language: "tlh", n: 1) == 0)
        #expect(PluralRules.index(language: "tlh", n: 3) == 1)
        #expect(PluralRules.category(language: "", n: 3) == "other")
    }
}
