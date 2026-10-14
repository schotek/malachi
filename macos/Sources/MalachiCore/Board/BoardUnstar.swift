// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// When a case offers Unstar, for the places that have a case rather than
// its detail (the context menu of a row or a card). Swift-first, like the
// board.

extension Board {
    /// Unstar is offered: the case is on the board because of a star (rule
    /// reason `hot.flagged`, whatever state the user gave it) and is not
    /// done. `Detail.canUnstar` says the same for the selected case.
    public static func canUnstar(_ c: Case) -> Bool {
        c.ruleReason == .hotFlagged && !c.visibility.isDone
    }
}
