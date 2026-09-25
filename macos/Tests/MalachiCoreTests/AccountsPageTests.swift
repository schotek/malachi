// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The counterpart of ui/internal/window/accounts_page_test.go.
@Suite struct AccountsPageTests {
    @Test func accountRowTitleTest() {
        var a = testAccount("a", name: "Work", email: "me@example.invalid")
        #expect(accountRowTitle(a) == "Work")
        a.config.name = ""
        #expect(accountRowTitle(a) == "me@example.invalid")
    }

    @Test func accountStatusTextTest() {
        let cases: [SyncStatus: String] = [
            .idle: "",
            .disabled: "Paused",
            .syncing: "Syncing…",
            .offline: "Offline",
            .authRequired: "Sign-in required",
            .error: "Error",
            "unknown": "",
        ]
        for (status, want) in cases {
            #expect(accountStatusText(status) == want, "\(status)")
        }
    }

    @Test func insertIndexTest() {
        // A list a b c d: the index the dragged row ends up at, once it is taken
        // out of the list. from == result means "no change".
        let cases: [(String, Int, Int, Bool, Int)] = [
            ("onto itself, upper half", 1, 1, true, 1),
            ("onto itself, lower half", 1, 1, false, 1),
            ("onto the row above, upper half", 2, 1, true, 1),
            // Below the row above is where it already is: a no-op (want == from).
            ("onto the row above, lower half", 2, 1, false, 2),
            ("onto the row below, upper half", 1, 2, true, 1),
            ("onto the row below, lower half", 1, 2, false, 2),
            ("first onto last, lower half", 0, 3, false, 3),
            ("last onto first, upper half", 3, 0, true, 0),
            ("down two rows", 0, 2, false, 2),
            ("up two rows", 3, 1, true, 1),
        ]
        for (name, from, target, above, want) in cases {
            #expect(insertIndex(from: from, target: target, above: above) == want, Comment(rawValue: name))
        }
    }

    @Test func moveAccountTest() {
        func list(_ names: String...) -> [Account] {
            names.map { testAccount($0) }
        }
        func ids(_ accounts: [Account]) -> String {
            accounts.map(\.id.rawValue).joined()
        }
        let cases: [(String, Int, Int, String)] = [
            ("down one", 0, 1, "bacd"),
            ("down to the end", 0, 3, "bcda"),
            ("up one", 3, 2, "abdc"),
            ("up to the head", 3, 0, "dabc"),
            ("middle to middle", 1, 2, "acbd"),
            ("no move", 2, 2, "abcd"),
            ("out of range low", 0, -1, "abcd"),
            ("out of range high", 0, 4, "abcd"),
        ]
        for (name, from, to, want) in cases {
            let input = list("a", "b", "c", "d")
            #expect(ids(moveAccount(input, from: from, to: to)) == want, Comment(rawValue: name))
            #expect(ids(input) == "abcd", "\(name): the input was modified")
        }
        let single = moveAccount(list("a"), from: 0, to: 0)
        #expect(single.count == 1)
        #expect(single.first?.id == "a")
        #expect(moveAccount([], from: 0, to: 1).isEmpty)
    }
}
