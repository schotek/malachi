// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/conversation/fold.go: every message card of the conversation
// folds to its header and a preview of its text (the summary's snippet) and
// opens again: its arrow, a click on the preview, or the one button above
// the conversation that folds or opens them all. A folded card holds no web
// view and asks for no body. Events and the row of older messages are no
// cards and do not fold.
//
// Each card starts as `defaultFolds` says; what the user chose for a card
// (`Folds`) holds until another conversation is shown, through every update
// of the model: a card that arrives meanwhile starts as its default. Go's
// "" ids are nil here (`opening`).

import Foundation

extension Conversation {
    /// conversation.Foldable: an item that folds: a message card.
    public static func foldable(_ it: Item) -> Bool {
        guard it.kind == .message, let id = it.message?.id else { return false }
        return !id.rawValue.isEmpty
    }

    /// conversation.DefaultFolds: how each foldable card of `items` (any
    /// order) starts, by message id, true for folded. `opening` is the card
    /// that opened the conversation (the pane's rule: the description of an
    /// issue, else the oldest member when no older one is left out; nil for
    /// none). A sent card starts folded: the user wrote it and knows it. The
    /// opening card starts folded while another card that is not a sent card
    /// follows it (a conversation of one message and its status changes, or
    /// of one message and the user's replies, shows that message whole).
    /// Every other card starts open. Should that leave every card folded,
    /// the newest opens, so that a conversation never opens on headers
    /// alone.
    public static func defaultFolds(_ items: [Item], opening: MessageID?) -> [MessageID: Bool] {
        // The cards, a repeated id once (its first card).
        var cards: [MessageSummary] = []
        var sentCards = Set<MessageID>()
        var seen = Set<MessageID>()
        var others = 0
        for it in items {
            guard foldable(it), let s = it.message, !seen.contains(s.id) else { continue }
            seen.insert(s.id)
            cards.append(s)
            if it.sent {
                sentCards.insert(s.id)
            } else if s.id != opening {
                others += 1
            }
        }
        var out: [MessageID: Bool] = [:]
        out.reserveCapacity(cards.count)
        var open = false
        var newest = -1
        for (i, s) in cards.enumerated() {
            let folded = sentCards.contains(s.id) || (s.id == opening && others > 0)
            out[s.id] = folded
            open = open || !folded
            if newest < 0 || before(cards[newest], s) {
                newest = i
            }
        }
        if !open, newest >= 0 {
            out[cards[newest].id] = false
        }
        return out
    }

    /// conversation.Folds: the fold state of the cards of the conversation
    /// on show: the user's choices, by message id, over `defaultFolds`. A
    /// new value is ready.
    public struct Folds: Sendable, Equatable {
        private var thread: ThreadID?
        private var chosen: [MessageID: Bool] = [:]

        public init() {}

        /// Called whenever the pane shows conversation `thread`, built anew
        /// or updated: the choices made for another conversation are
        /// forgotten, those for this one kept.
        public mutating func show(_ thread: ThreadID) {
            if thread != self.thread {
                self.thread = thread
                chosen = [:]
            }
        }

        /// Records the user's fold (true) or unfold of card `id`.
        public mutating func set(_ id: MessageID, _ folded: Bool) {
            if id.rawValue.isEmpty {
                return
            }
            chosen[id] = folded
        }

        /// Records `folded` for every foldable card of `items`: the button
        /// above the conversation (`foldAllOffer`). A card that arrives
        /// later starts as its default.
        public mutating func setAll(_ items: [Item], _ folded: Bool) {
            for it in items where Conversation.foldable(it) {
                if let id = it.message?.id {
                    set(id, folded)
                }
            }
        }

        /// Whether each foldable card of `items` is folded, by message id:
        /// the user's choice, else the default (`defaultFolds` with
        /// `opening`).
        public func state(_ items: [Item], opening: MessageID?) -> [MessageID: Bool] {
            var out = Conversation.defaultFolds(items, opening: opening)
            for id in out.keys {
                if let folded = chosen[id] {
                    out[id] = folded
                }
            }
            return out
        }
    }

    /// conversation.FoldAll: what the button above the conversation offers.
    public enum FoldAll: Sendable, Equatable {
        /// No button (fewer than two cards fold).
        case none
        /// Folds every card ("Collapse All").
        case collapse
        /// Opens every card ("Expand All").
        case expand

        /// What the offer sets every card to (`Folds.setAll`): true for
        /// Collapse All.
        public var folded: Bool {
            self == .collapse
        }

        /// The button's text; "" for `.none`.
        public var label: String {
            switch self {
            case .collapse:
                // TRANSLATORS: a button above a conversation in the reading pane; folds every message to its header.
                return L10n.T("Collapse All")
            case .expand:
                // TRANSLATORS: a button above a conversation in the reading pane; shows every message whole.
                return L10n.T("Expand All")
            case .none:
                return ""
            }
        }
    }

    /// conversation.FoldAllOffer: the button for the fold state of the
    /// cards (`Folds.state`): none with fewer than two cards, Collapse All
    /// while at least one card is open, else Expand All.
    public static func foldAllOffer(_ state: [MessageID: Bool]) -> FoldAll {
        if state.count < 2 {
            return .none
        }
        return state.values.contains(false) ? .collapse : .expand
    }
}
