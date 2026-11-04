// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Several listeners for one of the board's application-wide objects (the
// board preferences, the triage run, its schedule): each `observe` returns a
// token that removes its handler. Swift-first, like the board.

import Foundation

/// Removes a handler installed by an `observe`; dropping the token does
/// not.
@MainActor
public final class BoardObserverToken {
    private var remove: (@MainActor () -> Void)?

    init(remove: @escaping @MainActor () -> Void) {
        self.remove = remove
    }

    public func cancel() {
        let r = remove
        remove = nil
        r?()
    }
}

/// The handlers, called in the order they were added.
@MainActor
final class BoardObservers {
    private var handlers: [Int: @MainActor () -> Void] = [:]
    private var next = 0

    func add(_ f: @escaping @MainActor () -> Void) -> BoardObserverToken {
        let id = next
        next += 1
        handlers[id] = f
        return BoardObserverToken { [weak self] in
            self?.handlers[id] = nil
        }
    }

    func notify() {
        // By number: a handler may cancel its own token, or another's.
        for id in handlers.keys.sorted() {
            handlers[id]?()
        }
    }
}
