// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

@testable import MalachiCore
import Testing

private typealias F = BoardFixture

/// `Board.canUnstar` (the context menu's Unstar) agrees with the detail's
/// `canUnstar`.
@Suite struct BoardUnstarRuleTests {
    @Test func agreesWithTheDetail() {
        for reason in [BoardReason.hotFlagged, .hotImportant, .youAddressed] {
            for done in [false, true] {
                for user in [nil, Board.State.info] {
                    var c = F.mk("c1", .hot, user: user, done: done)
                    c.ruleReason = reason
                    let d = try! #require(F.view([c]) { $0.filter = done ? .done : $0.filter }.detail)
                    #expect(Board.canUnstar(c) == d.canUnstar, "\(reason) done \(done) user \(String(describing: user))")
                    #expect(Board.canUnstar(c) == (reason == .hotFlagged && !done))
                }
            }
        }
    }
}
