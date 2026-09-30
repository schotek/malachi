// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The arithmetic of the conversation view that does not need AppKit: which
// cards are near enough to the viewport to hold a body and a web view, how
// a card's web view follows the height of the document it shows, the order
// the pane shows the model's items in (what opened the conversation first,
// then the rest newest first), the timeline in the gutter beside the cards
// (which item gets an avatar and which a dot, where the line runs) and
// where the parts of a card's header go at the width it has. The pane
// (ConversationViewController, ConversationRow, ConversationCardView, the
// sized mode of MessageWebView) applies it. Swift-first, like the view; the
// GTK pane ported it (ui/internal/window/conversation_layout.go), and the
// order shown is a port of what it added there (`convDisplayOrder`).

/// The conversation view's rules for keeping cards cheap: every card is a
/// native view, but a body is fetched only for the cards within
/// `liveScreens` screens of the viewport, and a web view exists only for at
/// most `maxLiveWebViews` of those, the nearest first.
public enum ConversationLayout {
    /// How many viewport heights above and below the visible part of the
    /// stack count as near.
    public static let liveScreens = 2.0
    /// The most web views the pane keeps at once.
    public static let maxLiveWebViews = 8
    /// The height a card's body is given until it is known.
    public static let estimatedBodyHeight = 160.0
    /// The height a web view starts from before its document reports one.
    public static let initialWebHeight = 120.0

    /// A vertical stretch of the stack, in the document's coordinates (top
    /// down).
    public struct Span: Sendable, Equatable {
        public var minY: Double
        public var maxY: Double

        public init(_ minY: Double, _ maxY: Double) {
            self.minY = minY
            self.maxY = max(minY, maxY)
        }

        /// How far the span is from `other`: 0 when they overlap.
        public func distance(to other: Span) -> Double {
            if maxY < other.minY {
                return other.minY - maxY
            }
            if minY > other.maxY {
                return minY - other.maxY
            }
            return 0
        }
    }

    /// What `live` decided: the items near the viewport (a body is fetched
    /// for them) and those of them that get a web view.
    public struct Live: Sendable, Equatable {
        public var near: Set<Int>
        public var web: Set<Int>

        public init(near: Set<Int> = [], web: Set<Int> = []) {
            self.near = near
            self.web = web
        }
    }

    /// The items of the stack at `frames` near `visible` (within `screens`
    /// viewport heights of it; an empty viewport counts the first screen
    /// from its top), and of those the ones whose body is HTML (`html`,
    /// same indices as `frames`) that get a web view: the nearest to the
    /// visible part first, ties by position, at most `cap`.
    public static func live(
        frames: [Span], html: [Bool], visible: Span, screens: Double = liveScreens, cap: Int = maxLiveWebViews
    ) -> Live {
        let height = max(visible.maxY - visible.minY, 1)
        let window = Span(visible.minY - screens * height, visible.maxY + screens * height)
        var out = Live()
        var candidates: [(index: Int, distance: Double)] = []
        for (i, f) in frames.enumerated() where f.distance(to: window) == 0 {
            out.near.insert(i)
            if i < html.count, html[i] {
                candidates.append((i, f.distance(to: visible)))
            }
        }
        candidates.sort { $0.distance != $1.distance ? $0.distance < $1.distance : $0.index < $1.index }
        out.web = Set(candidates.prefix(max(cap, 0)).map(\.index))
        return out
    }

    /// Where the viewport's top goes to keep an item in place: the item
    /// that was at `offset` below the viewport's top is at `itemTop` now;
    /// the result is clamped to the document (`documentHeight`, a viewport
    /// `viewportHeight` tall).
    public static func anchoredTop(itemTop: Double, offset: Double, documentHeight: Double, viewportHeight: Double) -> Double {
        let maxTop = max(0, documentHeight - viewportHeight)
        return min(max(0, itemTop + offset), maxTop)
    }

    /// Where the viewport's top goes for one page down (or up) of Space:
    /// the viewport's height less `overlap`, clamped to the document.
    public static func pageTop(
        from top: Double, up: Bool, viewportHeight: Double, documentHeight: Double, overlap: Double
    ) -> Double {
        let step = max(viewportHeight - overlap, viewportHeight / 2)
        let maxTop = max(0, documentHeight - viewportHeight)
        return min(max(0, top + (up ? -step : step)), maxTop)
    }
}

// MARK: The order shown

extension ConversationLayout {
    /// conversation_layout.go `convDisplay`: the stack as the pane shows it
    /// (`displayOrder`): the items in order, which of them opened the
    /// conversation (`root`, an index into `items`; -1 when it is not
    /// shown), and whether that card starts folded to its header.
    public struct Display: Sendable, Equatable {
        public var items: [Conversation.Item]
        public var root: Int
        public var rootFolded: Bool

        public init(items: [Conversation.Item] = [], root: Int = -1, rootFolded: Bool = false) {
            self.items = items
            self.root = root
            self.rootFolded = rootFolded
        }
    }

    /// conversation_layout.go `convDisplayOrder`: the order the pane shows
    /// the model's items in, as Jira shows an issue: what opened the
    /// conversation first (`root`: the issue's description, or the oldest
    /// message of a mail conversation that thread.get did not cut), then
    /// the rest newest first, so that the newest is what the pane opens on
    /// right under it, and the row of older members left out last. The
    /// opening card starts folded while another message card follows it (a
    /// conversation of one message and its status changes shows that
    /// message whole). The model (`Conversation.Model`) keeps the items
    /// oldest first; `items` is not changed.
    public static func displayOrder(_ items: [Conversation.Item]) -> Display {
        var d = Display()
        d.items.reserveCapacity(items.count)
        let opening = root(items)
        if opening >= 0 {
            d.items.append(items[opening])
            d.root = 0
        }
        var truncated: [Conversation.Item] = []
        for i in items.indices.reversed() where i != opening {
            if items[i].kind == .truncated {
                truncated.append(items[i])
                continue
            }
            d.items.append(items[i])
            if items[i].kind == .message {
                d.rootFolded = opening >= 0
            }
        }
        d.items.append(contentsOf: truncated)
        return d
    }

    /// conversation_layout.go `convRoot`: the index in `items` (the
    /// model's, oldest first) of the item that opened the conversation: the
    /// description of a Jira issue wherever it is, else the oldest member
    /// when it is a message card and no older member is left out (no
    /// truncated row before it); -1 for none.
    public static func root(_ items: [Conversation.Item]) -> Int {
        if let i = items.firstIndex(where: { $0.kind == .message && $0.message?.issue?.item == .description }) {
            return i
        }
        if let first = items.first, first.kind == .message {
            return 0
        }
        return -1
    }
}

// MARK: The timeline

extension ConversationLayout {
    /// The measures of the conversation's column, in points: a gutter with
    /// the timeline (a line through the avatars of the messages and the
    /// dots of the events), and beside it the cards.
    public enum Metrics {
        /// The widest the column gets, the gutter included.
        public static let maxWidth = 900.0
        /// Between the pane's edge and the column, on both sides.
        public static let sideInset = 16.0
        /// The avatar of a message, which is also the gutter's width.
        public static let avatar = 28.0
        /// Between the gutter and the cards.
        public static let gutterGap = 12.0
        /// Between two items of the stack.
        public static let itemGap = 12.0
        /// The dot of an event and of the row of older messages.
        public static let dot = 7.0
        /// The line's width.
        public static let line = 1.0
        /// Between the line's end and the avatar or dot it runs to.
        public static let lineBreak = 3.0
        /// A card's corner radius and the padding inside it.
        public static let cardRadius = 10.0
        public static let cardPaddingV = 10.0
        public static let cardPaddingH = 14.0
        /// A header narrower than this shows the short date of the list
        /// instead of the full date and time.
        public static let compactHeader = 420.0
    }

    /// What marks an item on the line.
    public enum Marker: Sendable, Equatable {
        /// The sender's avatar, its top at the top of the card.
        case avatar
        /// A small dot beside the first line of the text.
        case dot
    }

    /// An item's piece of the timeline.
    public struct Rail: Sendable, Equatable {
        public var marker: Marker
        /// The avatar is tinted with the accent colour: the user's own
        /// message (`Conversation.Item.mine`). Never set for a dot.
        public var accent: Bool
        /// The line runs from the item above down to the marker, and from
        /// the marker down to the item below.
        public var above: Bool
        public var below: Bool

        public init(marker: Marker, accent: Bool = false, above: Bool = false, below: Bool = false) {
            self.marker = marker
            self.accent = accent
            self.above = above
            self.below = below
        }
    }

    /// The timeline of `items` (in the order shown, `displayOrder`), one
    /// piece each: a message has its sender's avatar (accent-tinted when it
    /// is the user's own), an event and the row of older messages a dot.
    /// The line runs between the markers: from the first item's to the last
    /// item's, so a conversation of one item has none.
    public static func rails(_ items: [Conversation.Item]) -> [Rail] {
        items.enumerated().map { i, item in
            let message = item.kind == .message
            return Rail(
                marker: message ? .avatar : .dot, accent: message && item.mine, above: i > 0,
                below: i < items.count - 1)
        }
    }

    /// The width of the cards in a pane `pane` wide: the column less the
    /// insets and the gutter; never negative.
    public static func cardWidth(pane: Double) -> Double {
        max(0, min(pane, Metrics.maxWidth) - 2 * Metrics.sideInset - Metrics.avatar - Metrics.gutterGap)
    }

    /// Whether the cards of a pane `pane` wide show the short date (their
    /// header is narrower than `Metrics.compactHeader`).
    public static func compactDates(pane: Double) -> Bool {
        cardWidth(pane: pane) - 2 * Metrics.cardPaddingH < Metrics.compactHeader
    }
}

// MARK: A card's header

/// Where the parts of a card's header go at the width the card has: the
/// fold arrow of the card that opened the conversation, the unread dot, the
/// sender and the recipients' disclosure, then the badges (who relayed the
/// comment, Internal, Edited), and at the trailing edge the date, with the
/// hover buttons before it.
///
/// The date keeps its place and its width. The sender gives way first (its
/// name is truncated), down to `minSender`. The badges follow the sender on
/// the first line while they fit before the date; the first that does not
/// starts a second line, where the others follow it and one that still
/// does not fit is truncated (and left out below `minBadge`). The hover
/// buttons take room only while they show: a sender that reaches under
/// them is truncated further then, so the disclosure stays in reach; the
/// badges stay where they are (the buttons cover them, never the other way
/// round), so showing the buttons never changes the header's height.
public struct ConversationHeaderLayout: Sendable, Equatable {
    /// Between the parts of the first line.
    public static let spacing = 6.0
    /// Between two badges, and between the sender's group and a badge.
    public static let badgeSpacing = 8.0
    /// Between the date and what comes before it.
    public static let dateSpacing = 8.0
    /// The narrowest the sender's name gets (or its own width, if less).
    public static let minSender = 40.0
    /// The narrowest a truncated badge gets before it is left out.
    public static let minBadge = 24.0

    /// The natural widths of the parts; 0 for a part that is not shown.
    public struct Parts: Sendable, Equatable {
        /// The fold arrow, before everything else.
        public var fold: Double
        public var dot: Double
        public var sender: Double
        public var disclosure: Double
        /// In order: who relayed the comment, Internal, Edited.
        public var badges: [Double]
        public var date: Double
        public var buttons: Double

        public init(
            fold: Double = 0, dot: Double = 0, sender: Double = 0, disclosure: Double = 0, badges: [Double] = [],
            date: Double = 0, buttons: Double = 0
        ) {
            self.fold = fold
            self.dot = dot
            self.sender = sender
            self.disclosure = disclosure
            self.badges = badges
            self.date = date
            self.buttons = buttons
        }
    }

    /// A part's place: from `x`, `width` wide, on `line` (0 the first).
    public struct Slot: Sendable, Equatable {
        public var x: Double
        public var width: Double
        public var line: Int

        public init(_ x: Double, _ width: Double, line: Int = 0) {
            self.x = x
            self.width = width
            self.line = line
        }

        public var maxX: Double { x + width }
    }

    /// nil: the part is not shown.
    public var fold: Slot?
    public var dot: Slot?
    public var sender: Slot?
    public var disclosure: Slot?
    /// As `Parts.badges`.
    public var badges: [Slot?] = []
    public var date: Slot?
    /// Where the hover buttons go, shown or not.
    public var buttons: Slot?
    /// One, or two when a badge went to the second line.
    public var lines = 1

    /// The header of `parts` in `width`; `buttonsShown`: the hover buttons
    /// show (the pointer is over the card, or one of them has the focus).
    public init(_ parts: Parts, width: Double, buttonsShown: Bool) {
        let width = max(width.isFinite ? width : 0, 0)
        func shown(_ w: Double) -> Double { w.isFinite && w > 0 ? w : 0 }

        let dateWidth = min(shown(parts.date), width)
        if dateWidth > 0 {
            date = Slot(width - dateWidth, dateWidth)
        }
        // Where the first line's leading parts end at the latest.
        let room = dateWidth > 0 ? max(width - dateWidth - Self.dateSpacing, 0) : width

        var x = 0.0
        if shown(parts.fold) > 0 {
            fold = Slot(0, min(shown(parts.fold), room))
            x = shown(parts.fold) + Self.spacing
        }
        if shown(parts.dot) > 0 {
            dot = Slot(x, min(shown(parts.dot), max(room - x, 0)))
            x += shown(parts.dot) + Self.spacing
        }
        let disclosureWidth = shown(parts.disclosure)
        let after = disclosureWidth > 0 ? Self.spacing + disclosureWidth : 0
        let natural = shown(parts.sender)
        let least = min(natural, Self.minSender)
        // Without the buttons: the sender's width decides where the badges go.
        let resting = min(natural, max(room - x - after, least))

        let buttonsWidth = shown(parts.buttons)
        var senderWidth = resting
        if buttonsWidth > 0 {
            let leading = x + resting + after
            var left = max(room - buttonsWidth, 0)
            if buttonsShown, left < leading + Self.spacing {
                // The sender makes room, as far as it can; then the buttons
                // start after the disclosure, whatever they cover.
                senderWidth = min(resting, max(left - Self.spacing - x - after, least))
                left = max(left, x + senderWidth + after + Self.spacing)
            }
            buttons = Slot(left, buttonsWidth)
        }
        if natural > 0 {
            sender = Slot(x, senderWidth)
        }
        if disclosureWidth > 0 {
            disclosure = Slot(x + senderWidth + (natural > 0 ? Self.spacing : 0), disclosureWidth)
        }

        // The badges flow after the resting sender, so that the buttons
        // showing moves none of them.
        var cursor = natural > 0 || disclosureWidth > 0 ? x + resting + after : max(x - Self.spacing, 0)
        var line = 0
        for badge in parts.badges {
            let w = shown(badge)
            guard w > 0 else {
                badges.append(nil)
                continue
            }
            var start = cursor > 0 ? cursor + Self.badgeSpacing : 0
            var limit = line == 0 ? room : width
            if start + w > limit, line == 0 {
                line = 1
                start = 0
                limit = width
            }
            let fitted = min(w, max(limit - start, 0))
            if fitted < min(w, Self.minBadge) {
                badges.append(nil)
                continue
            }
            badges.append(Slot(start, fitted, line: line))
            cursor = start + fitted
        }
        lines = line + 1
    }
}

// MARK: An event's row

/// How the row of an event shares its width: the lines of the changes, who
/// made them, and the time at the trailing edge. The time keeps its width.
/// The name gives way first, down to `minSender`; only then do the lines
/// wrap, and where they would be left with less than `minLines`, the name
/// is left out (accessibility still hears it).
public struct ConversationEventLayout: Sendable, Equatable {
    /// Between the lines, the name and the time.
    public static let spacing = 8.0
    /// The widest and the narrowest the name gets (or its own width, if
    /// less).
    public static let maxSender = 200.0
    public static let minSender = 60.0
    /// The least the lines are left with beside a name.
    public static let minLines = 120.0

    /// The width the lines have, and wrap at.
    public var lines: Double
    /// The name's width; 0: it is not shown.
    public var sender: Double

    /// The row of lines `lines` wide at most when not wrapped, a name
    /// `sender` wide and a time `date` wide (0: not shown), in `width`.
    public init(width: Double, lines: Double, sender: Double, date: Double) {
        func shown(_ w: Double) -> Double { w.isFinite && w > 0 ? w : 0 }
        let width = shown(width)
        let date = min(shown(date), width)
        let room = max(width - (date > 0 ? date + Self.spacing : 0), 0)
        let want = min(shown(sender), Self.maxSender)
        let natural = shown(lines)
        guard want > 0 else {
            self.lines = room
            self.sender = 0
            return
        }
        let given = min(want, max(room - natural - Self.spacing, min(want, Self.minSender)))
        let left = room - given - Self.spacing
        if left < min(natural, Self.minLines) {
            self.lines = room
            self.sender = 0
        } else {
            self.lines = left
            self.sender = given
        }
    }
}

/// How the web view of a card follows the height of its document (the
/// sized mode of the reader's web view). The document reports its height in
/// CSS pixels whenever it changes (a ResizeObserver of the view's own
/// script); the view becomes that tall, at the page zoom, up to `maxHeight`
/// (beyond it the card scrolls inside). Content sized by the viewport
/// (`100vh`, `height: 100%`) would grow with every step the view grows: a
/// report the document made because the view's height changed
/// (`viewport`) that grows it again counts, and after `growthLimit` of
/// those in a row the height stays where it is (`frozen`) until the
/// document, the width or the zoom changes.
public struct WebHeightGovernor: Sendable, Equatable {
    /// The tallest a card's web view gets, in points.
    public static let maxHeight = 4000.0
    /// How many growths in a row caused by the view's own growth are
    /// accepted before the height is frozen.
    public static let growthLimit = 3

    public let maxHeight: Double
    public let growthLimit: Int
    /// The document's height as last reported, in CSS pixels; nil before
    /// the first report.
    public private(set) var css: Double?
    /// The page zoom the view shows the document at (1 = 100 %).
    public private(set) var zoom: Double
    /// The height the view was given, in points; nil before the first.
    public private(set) var applied: Double?
    public private(set) var frozen = false
    private var streak = 0

    public init(zoom: Double = 1, maxHeight: Double = WebHeightGovernor.maxHeight, growthLimit: Int = WebHeightGovernor.growthLimit) {
        self.zoom = zoom > 0 ? zoom : 1
        self.maxHeight = maxHeight
        self.growthLimit = growthLimit
    }

    /// The document's height in points at the current zoom; nil before the
    /// first report.
    public var content: Double? {
        css.map { ($0 * zoom).rounded(.up) }
    }

    /// Whether the document fits the view (nothing to scroll inside): the
    /// scroll wheel goes to the conversation.
    public var fits: Bool {
        guard let content, let applied else { return true }
        return content <= applied + 1
    }

    /// A new document: its height is measured from scratch. The view keeps
    /// the height it has until the first report.
    public mutating func reset() {
        css = nil
        frozen = false
        streak = 0
    }

    /// The view's width changed: the document reflows, and a frozen height
    /// is measured again.
    public mutating func widthChanged() {
        frozen = false
        streak = 0
    }

    /// The zoom changed to `zoom`: the height the view should get at once
    /// (the last report scaled), nil when there is none yet or it does not
    /// change. A frozen height is measured again.
    public mutating func setZoom(_ zoom: Double) -> Double? {
        let z = zoom > 0 ? zoom : 1
        guard z != self.zoom else { return nil }
        self.zoom = z
        frozen = false
        streak = 0
        return apply()
    }

    /// The document reported `css` pixels; `viewport` says the report
    /// followed a change of the view's height alone. The height the view
    /// should get, nil to keep the one it has.
    public mutating func report(css: Double, viewport: Bool) -> Double? {
        guard css.isFinite, css >= 0 else { return nil }
        self.css = css
        if frozen {
            return nil
        }
        let target = min(max((css * zoom).rounded(.up), 1), maxHeight)
        if viewport, let a = applied, target > a {
            streak += 1
            if streak > growthLimit {
                frozen = true
                return nil
            }
        } else {
            streak = 0
        }
        return apply()
    }

    /// Applies the last report at the current zoom.
    private mutating func apply() -> Double? {
        guard let content else { return nil }
        let target = min(max(content, 1), maxHeight)
        guard target != applied else { return nil }
        applied = target
        return target
    }
}
