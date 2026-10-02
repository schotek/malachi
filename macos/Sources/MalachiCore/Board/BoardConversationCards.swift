// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// What the cards of the board detail's conversation show: the folded
// preview, the plain text excerpt `board.get` gave, or the message's
// sanitised HTML in a locked web view of its own (`message.body`, the web
// view of Mail's conversation cards in its sized mode); how many web views
// the detail keeps and which card gives way; which board refreshes change
// the cards at all; and how far the detail scrolls when a card above the
// viewport changes its height. AppKit (BoardConversationBlock,
// BoardMessageCardView) only applies it. Swift-first, like the board.

import Foundation

extension Board {
    /// The state of the detail's conversation cards for the case on show
    /// (`apply`), oldest first like `Detail.messages`.
    ///
    /// A card is one member (`Member`: its message id and the excerpt the
    /// daemon gave for it). The newest opens, the older ones start folded
    /// to a preview. An open card of a member with an id asks for its body
    /// once (`needsBody`, `asked`); what came back (`answered`) decides:
    /// HTML shows in a web view while the detail is live (`shows`), any
    /// other answer (no HTML part, HTML withheld, a body not stored, a
    /// failure, a message gone) leaves the excerpt, silently. Members
    /// without an id (the invented samples) never ask and stay text.
    ///
    /// At most `maxLive` open cards hold HTML: opening one more, or HTML
    /// arriving for one more, folds back the card opened least recently,
    /// never the newest and never the card just opened.
    public struct ConversationCards: Sendable, Equatable {
        /// The most web views the detail's conversation keeps. Mail keeps 8
        /// for a whole pane of cards; the detail shows one case beside the
        /// inline reply editor, itself a web view, and its older cards
        /// start folded: the newest and a few the user opened, so that 4
        /// plus the editor stay well under Mail's figure.
        public static let maxLiveWebViews = 4

        /// One card: the message and the excerpt shown for it. The same id
        /// with another excerpt is the same card with new content (the
        /// daemon rebuilt the message in place, as for a Jira item): see
        /// `apply`.
        public struct Member: Sendable, Hashable {
            /// nil for the invented samples.
            public var id: MessageID?
            public var text: String

            public init(id: MessageID?, text: String) {
                self.id = id
                self.text = text
            }
        }

        /// What decides whether a refresh of the board touches the cards:
        /// the case and its members, nothing else of the detail.
        public struct Key: Sendable, Equatable {
            public var caseID: CaseID
            public var members: [Member]

            public init(caseID: CaseID, members: [Member]) {
                self.caseID = caseID
                self.members = members
            }
        }

        /// The key of `d`'s conversation.
        public static func key(_ d: Detail) -> Key {
            Key(caseID: d.id, members: d.messages.map { Member(id: $0.id, text: $0.text) })
        }

        /// What message.body gave for a member.
        public enum Body: Sendable, Equatable {
            /// Not asked for yet.
            case unknown
            /// Asked for; no answer yet (the excerpt shows meanwhile).
            case asked
            /// Sanitised HTML: the web view.
            case html
            /// Anything else: the excerpt stays.
            case text
        }

        /// What a card shows.
        public enum Shows: Sendable, Equatable {
            /// The three-line preview of the excerpt: no body, no web view.
            case folded
            /// The whole excerpt.
            case text
            /// The sanitised HTML in a web view.
            case web
        }

        /// What `apply` changed, for the view.
        public enum Change: Sendable, Equatable {
            /// Nothing: no card is made, moved or let go.
            case none
            /// Another case (or the first): every card is made anew.
            case reset
            /// The same case with other members: `kept[i]` is the index in
            /// the previous members of the card that stays as member `i`
            /// (its view and its web view with it); a member without an
            /// entry gets a new card, a previous card no entry names goes.
            ///
            /// A kept card may have another excerpt (same id, the daemon
            /// rebuilt the message): the view updates its text. `reload`
            /// lists the open kept cards whose body must be fetched again
            /// and shown in place, their previous body staying meanwhile.
            case members(kept: [Int: Int], reload: [Int])
        }

        public let maxLive: Int
        public private(set) var caseID: CaseID?
        public private(set) var members: [Member] = []
        /// Same indices as `members`.
        private var folds: [Bool] = []
        private var bodies: [Member: Body] = [:]
        /// The members in the order they were opened, the latest last.
        private var opened: [Member] = []

        public init(maxLive: Int = ConversationCards.maxLiveWebViews) {
            self.maxLive = max(maxLive, 1)
        }

        /// The index of the newest member, -1 when there is none.
        public var newest: Int { members.count - 1 }

        // MARK: Applying the detail

        /// Takes the case's members as `key` has them. Another case starts
        /// over: the newest open, the rest folded, nothing asked for. The
        /// same case keeps every member that is still there with its fold
        /// and its body (matched in order, so a sample's repeated excerpt
        /// keeps its own card; a member with the same id and another
        /// excerpt is matched next and keeps its card too); a new member opens when it is the newest
        /// and starts folded otherwise.
        public mutating func apply(_ key: Key) -> Change {
            guard key.caseID == caseID else {
                caseID = key.caseID
                members = key.members
                folds = members.indices.map { $0 != newest }
                bodies = [:]
                opened = members.last.map { [$0] } ?? []
                return .reset
            }
            guard key.members != members else { return .none }
            var free: [Member: [Int]] = [:]
            for (i, m) in members.enumerated() {
                free[m, default: []].append(i)
            }
            var kept: [Int: Int] = [:]
            var used = Set<Int>()
            for (i, m) in key.members.enumerated() {
                if var slots = free[m], !slots.isEmpty {
                    let old = slots.removeFirst()
                    free[m] = slots
                    kept[i] = old
                    used.insert(old)
                }
            }
            // The same id with another excerpt: the daemon rebuilt the
            // message in place. The card stays (its fold, its place, its
            // web view); only its text and, when open, its body are new.
            var changed: [Int] = []
            for (i, m) in key.members.enumerated() where kept[i] == nil {
                guard let id = m.id,
                      let old = members.indices.first(where: { !used.contains($0) && members[$0].id == id })
                else { continue }
                kept[i] = old
                used.insert(old)
                changed.append(i)
            }
            var newFolds: [Bool] = []
            var carried: [Member: Body] = [:]
            var renamed: [Member: Member] = [:]
            var reload: [Int] = []
            let last = key.members.count - 1
            for (i, m) in key.members.enumerated() {
                guard let old = kept[i] else {
                    newFolds.append(i != last)
                    if i == last {
                        opened.removeAll { $0 == m }
                        opened.append(m)
                    }
                    continue
                }
                newFolds.append(folds[old])
                guard changed.contains(i) else { continue }
                // A body known for the old excerpt is kept for an open card
                // (the view shows it until the new one arrives) and asked
                // again; anything else asks the usual way.
                renamed[members[old]] = m
                if !folds[old], let b = bodies[members[old]], b == .html || b == .text {
                    carried[m] = b
                    reload.append(i)
                }
            }
            let present = Set(key.members)
            opened = opened.map { renamed[$0] ?? $0 }
            members = key.members
            folds = newFolds
            bodies = bodies.filter { present.contains($0.key) }
            for (m, b) in carried {
                bodies[m] = b
            }
            opened.removeAll { !present.contains($0) }
            return .members(kept: kept, reload: reload)
        }

        // MARK: Reading

        /// Whether card `i` starts or is folded.
        public func isFolded(_ i: Int) -> Bool {
            folds.indices.contains(i) ? folds[i] : false
        }

        /// Whether card `i` folds at all: every card but the newest.
        public func foldable(_ i: Int) -> Bool {
            members.indices.contains(i) && i != newest
        }

        /// Whether card `i` offers its fold arrow. A card that can show its
        /// message formatted (`canFetch`: it has an id and the detail can
        /// ask for bodies) always does, opening is how the user gets the
        /// formatted message; otherwise only while the excerpt is longer
        /// than the folded preview (`long`, nil until measured: no arrow).
        public func arrow(_ i: Int, long: Bool?, canFetch: Bool) -> Bool {
            guard foldable(i) else { return false }
            if canFetch, members[i].id != nil {
                return true
            }
            return long == true
        }

        /// What message.body gave for card `i`.
        public func body(_ i: Int) -> Body {
            guard members.indices.contains(i) else { return .unknown }
            return bodies[members[i]] ?? .unknown
        }

        /// Card `i` should ask for its body now: it is open, has an id and
        /// was not asked yet.
        public func needsBody(_ i: Int) -> Bool {
            guard members.indices.contains(i), members[i].id != nil, !folds[i] else { return false }
            return body(i) == .unknown
        }

        /// What card `i` shows; `live` says the detail may hold web views
        /// now (it is in a window and not hidden).
        public func shows(_ i: Int, live: Bool) -> Shows {
            guard members.indices.contains(i) else { return .text }
            if folds[i] {
                return .folded
            }
            return live && body(i) == .html ? .web : .text
        }

        /// The open cards holding HTML, which the limit counts.
        public var webCards: [Int] {
            members.indices.filter { !folds[$0] && body($0) == .html }
        }

        /// The index of `id`'s card, if the case has one.
        public func index(of id: MessageID) -> Int? {
            members.firstIndex { $0.id == id }
        }

        // MARK: Changes

        /// Card `i` asked for its body.
        public mutating func asked(_ i: Int) {
            guard members.indices.contains(i), body(i) == .unknown else { return }
            bodies[members[i]] = .asked
        }

        /// The body of card `i` arrived (or changed: the remote images were
        /// loaded elsewhere): HTML or not. Returns the cards the limit
        /// folded back, for the view to fold.
        ///
        /// `html: false` for a card that cannot show HTML after all (its
        /// web view is unavailable) takes it out of the count at once.
        public mutating func answered(_ i: Int, html: Bool) -> [Int] {
            guard members.indices.contains(i) else { return [] }
            bodies[members[i]] = html ? .html : .text
            guard html, !folds[i] else { return [] }
            return enforce(protecting: i)
        }

        /// The user folds or opens card `i` (never the newest). Returns the
        /// cards the limit folded back, for the view to fold: opening a card
        /// whose body is known to be HTML may push one out.
        public mutating func setFolded(_ i: Int, _ folded: Bool) -> [Int] {
            guard foldable(i), folds[i] != folded else { return [] }
            folds[i] = folded
            let m = members[i]
            opened.removeAll { $0 == m }
            if folded {
                return []
            }
            opened.append(m)
            // A card whose HTML is still to come counts once it arrives
            // (`answered`).
            return body(i) == .html ? enforce(protecting: i) : []
        }

        /// Folds the open HTML cards beyond `maxLive`, the least recently
        /// opened first (one never opened by the user counts as older than
        /// any opened), sparing the newest and `protected`.
        private mutating func enforce(protecting protected: Int) -> [Int] {
            var folded: [Int] = []
            var web = webCards
            while web.count > maxLive {
                let candidates = web.filter { $0 != newest && $0 != protected }
                guard let victim = candidates.min(by: { rank($0) < rank($1) }) else { break }
                folds[victim] = true
                opened.removeAll { $0 == members[victim] }
                folded.append(victim)
                web.removeAll { $0 == victim }
            }
            return folded.sorted()
        }

        /// The order of opening of card `i`: -1 when it is not in `opened`,
        /// ties by position.
        private func rank(_ i: Int) -> (Int, Int) {
            (opened.firstIndex(of: members[i]) ?? -1, i)
        }

        // MARK: Scrolling

        /// How far the detail's viewport top moves when a card's height
        /// changes by `delta` (a web view's height arriving, a card folded
        /// back by the limit): everything below the card's bottom moves by
        /// `delta`, so a card that ended at or above the viewport's top
        /// (`cardMaxY` ≤ `viewportTop`, the document's coordinates top
        /// down, before the change) moves what the user sees, and the
        /// viewport follows it; a card in view or below moves nothing
        /// above it. The result is clamped to the document after the change
        /// (`documentHeight`, a viewport `viewportHeight` tall).
        public static func compensatedTop(
            viewportTop: Double, cardMaxY: Double, delta: Double, documentHeight: Double, viewportHeight: Double
        ) -> Double {
            let move = cardMaxY <= viewportTop + 0.5 ? delta : 0
            let maxTop = max(0, documentHeight - viewportHeight)
            return min(max(0, viewportTop + move), maxTop)
        }
    }
}
