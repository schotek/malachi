// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The user's arrangement of the main window's toolbars
/// (ui/internal/toolbar, ported 1:1; docs/plans/toolbar-customization.md,
/// decisions T1–T11): the catalog of each toolbar's items (`catalog`), the
/// default arrangement (`defaultLayout`), the rules of the sections an
/// item may stand in (`canInsert`), how a stored arrangement becomes a
/// valid one (`normalize`), what the toolbar shows of it (`present`), the
/// actions More Actions adds (`missingActions`) and where ⌘F puts a search
/// field the user took off (`searchIndex`). The arrangement is stored as
/// `Layout.tokens` in the key `key(_:)` names, empty for the default.
///
/// The ids are the toolbars' `NSToolbarItem.Identifier`s (MainToolbar.ID,
/// BoardToolbar.ID, CalendarToolbar.ID); AppKit's own items are the neutral
/// tokens `space`, `flexibleSpace` and `toggleSidebar`, and a section's
/// separator is a token starting with "|" (the client maps "|list" of Mail
/// to `.sidebarTrackingSeparator` and so on). The labels are the toolbar
/// items' own.
public enum Toolbar {
    /// One of the main window's customizable toolbars.
    public enum Bar: String, CaseIterable, Sendable {
        case mail
        case boardList = "board-list"
        case boardFlow = "board-flow"
        case calendar
    }

    /// The settings key (gschema, type as) of `bar`'s arrangement.
    public static func key(_ bar: Bar) -> String { "toolbar-" + bar.rawValue }

    /// The settings key of the display mode, one for every toolbar (O4).
    public static let displayModeKey = "toolbar-display-mode"

    /// What the toolbar's items show (the gschema enum ToolbarDisplayMode).
    public enum DisplayMode: String, CaseIterable, Sendable {
        case icon
        case iconAndLabel = "icon-and-label"
        case label
    }

    /// toolbar.ParseDisplayMode: anything else is `.icon`.
    public static func parseDisplayMode(_ s: String) -> DisplayMode {
        DisplayMode(rawValue: s) ?? .icon
    }

    /// An item of a toolbar.
    public struct ID: RawRepresentable, Hashable, Sendable, ExpressibleByStringLiteral, CustomStringConvertible {
        public let rawValue: String
        public init(rawValue: String) { self.rawValue = rawValue }
        public init(_ rawValue: String) { self.rawValue = rawValue }
        public init(stringLiteral value: String) { rawValue = value }
        public var description: String { rawValue }

        // AppKit's own; a space may stand any number of times.
        public static let space: ID = "space"
        public static let flexibleSpace: ID = "flexibleSpace"
        public static let toggleSidebar: ID = "toggleSidebar"

        // Mail.
        public static let mode: ID = "mode"
        public static let newMessage: ID = "newMessage"
        public static let contacts: ID = "contacts"
        public static let filter: ID = "filter"
        public static let refresh: ID = "refresh"
        public static let reply: ID = "reply"
        public static let replyAll: ID = "replyAll"
        public static let forward: ID = "forward"
        public static let archive: ID = "archive"
        public static let trash: ID = "trash"
        public static let crm: ID = "crm"
        public static let star: ID = "star"
        public static let assistant: ID = "assistant"
        public static let moreActions: ID = "moreActions"
        public static let search: ID = "search"
        public static let markRead: ID = "markRead"
        public static let junk: ID = "junk"
        public static let moveTo: ID = "moveTo"
        public static let print: ID = "print"
        public static let undo: ID = "undo"
        public static let loadPictures: ID = "loadPictures"

        // The Board.
        public static let boardStyle: ID = "boardStyle"
        public static let boardAccount: ID = "boardAccount"
        public static let boardTriage: ID = "boardTriage"
        public static let boardDone: ID = "boardDone"
        public static let boardRemind: ID = "boardRemind"
        public static let boardArchive: ID = "boardArchive"
        public static let boardTrash: ID = "boardTrash"
        public static let boardReply: ID = "boardReply"

        // The Calendar.
        public static let calendarToday: ID = "calendarToday"
        public static let calendarStep: ID = "calendarStep"
        public static let calendarView: ID = "calendarView"
        public static let calendarSearch: ID = "calendarSearch"
        public static let calendarNewEvent: ID = "calendarNewEvent"
        public static let calendarAssistant: ID = "calendarAssistant"

        /// Whether this is a section's separator.
        public var isSeparator: Bool { rawValue.hasPrefix("|") }
        /// Whether this is a space, which may stand anywhere, any number of times.
        public var isSpace: Bool { self == .space || self == .flexibleSpace }
    }

    /// A part of a toolbar between two separators; a toolbar without
    /// separators is one section, `.main`.
    public enum Section: String, Sendable, CaseIterable {
        case sidebar, list, message, detail, main
    }

    /// The token of the separator that starts `s`.
    public static func separator(_ s: Section) -> ID { ID("|" + s.rawValue) }

    /// What a conditional item needs to be shown (T7).
    public enum Feature: String, Sendable {
        /// An eWay-CRM account exists.
        case crm
        /// The Assistant menu is on.
        case assistant
        /// The board's triage can run.
        case triage
        /// Printing exists in this client.
        case print
    }

    /// Visibility priorities as AppKit's `NSToolbarItem.VisibilityPriority`
    /// raw values: higher stays longer (T5).
    public enum Priority {
        public static let user = 2000
        public static let high = 1000
        public static let standard = 0
        public static let search = -500
        public static let low = -1000
    }

    /// An entry of a toolbar's catalog.
    public struct Item: Equatable, Sendable {
        public let id: ID
        /// Where the item may stand; the first is its home.
        public let sections: [Section]
        /// The user can neither remove nor move it (T4).
        public let fixed: Bool
        /// What it needs to be shown, nil for nothing.
        public let feature: Feature?
        /// An action More Actions offers while the toolbar does not show it (T8).
        public let inMore: Bool
        public let priority: Int

        init(_ id: ID, _ sections: [Section], fixed: Bool = false, feature: Feature? = nil,
             inMore: Bool = false, priority: Int = Priority.standard) {
            self.id = id
            self.sections = sections
            self.fixed = fixed
            self.feature = feature
            self.inMore = inMore
            self.priority = priority
        }

        /// The section `normalize` puts it in.
        public var home: Section { sections[0] }
    }

    private static let mailNav: [Section] = [.list, .sidebar]
    private static let boardNav: [Section] = [.list, .sidebar]
    private static let calNav: [Section] = [.main, .sidebar]

    private static func msg(_ id: ID, inMore: Bool, _ priority: Int = Priority.standard,
                            feature: Feature? = nil) -> Item {
        Item(id, [.message], feature: feature, inMore: inMore, priority: priority)
    }

    private static let catalogs: [Bar: [Item]] = [
        .mail: [
            Item(.mode, [.sidebar], fixed: true, priority: Priority.user),
            Item(.toggleSidebar, mailNav, priority: Priority.high),
            Item(.newMessage, mailNav),
            Item(.contacts, mailNav, priority: Priority.low),
            Item(.filter, mailNav),
            Item(.refresh, mailNav),
            msg(.reply, inMore: true),
            msg(.replyAll, inMore: true),
            msg(.forward, inMore: true),
            msg(.archive, inMore: true),
            msg(.trash, inMore: true),
            msg(.crm, inMore: true, feature: .crm),
            msg(.star, inMore: true),
            msg(.assistant, inMore: false, feature: .assistant),
            msg(.moreActions, inMore: false, Priority.high),
            msg(.search, inMore: false),
            // The palette's items (T10) are in the menus already: not inMore.
            msg(.markRead, inMore: false, Priority.low),
            msg(.junk, inMore: false, Priority.low),
            msg(.moveTo, inMore: false, Priority.low),
            msg(.print, inMore: false, Priority.low, feature: .print),
            msg(.undo, inMore: false, Priority.low),
            msg(.loadPictures, inMore: false, Priority.low),
        ],
        .boardList: [
            Item(.mode, [.sidebar], fixed: true, priority: Priority.user),
            Item(.toggleSidebar, boardNav, priority: Priority.high),
            Item(.boardStyle, boardNav, priority: Priority.high),
            Item(.contacts, boardNav, priority: Priority.low),
            Item(.boardAccount, boardNav),
            Item(.boardTriage, boardNav, feature: .triage, priority: Priority.low),
            Item(.boardDone, [.detail], priority: Priority.high),
            Item(.boardRemind, [.detail], priority: Priority.low),
            Item(.boardArchive, [.detail], priority: Priority.low),
            Item(.boardTrash, [.detail], priority: Priority.low),
            Item(.boardReply, [.detail]),
        ],
        .boardFlow: [
            Item(.mode, [.main], fixed: true, priority: Priority.user),
            Item(.boardStyle, [.main], priority: Priority.high),
            Item(.contacts, [.main], priority: Priority.low),
            Item(.boardAccount, [.main]),
            Item(.boardTriage, [.main], feature: .triage, priority: Priority.low),
        ],
        .calendar: [
            Item(.mode, [.sidebar], fixed: true, priority: Priority.user),
            Item(.toggleSidebar, calNav, priority: Priority.high),
            Item(.calendarToday, calNav, priority: Priority.high),
            Item(.calendarStep, calNav, priority: Priority.high),
            Item(.calendarView, calNav),
            Item(.calendarSearch, calNav, priority: Priority.search),
            Item(.contacts, calNav),
            Item(.calendarNewEvent, calNav, priority: Priority.high),
            Item(.calendarAssistant, calNav, feature: .assistant, priority: Priority.low),
            Item(.print, calNav, feature: .print, priority: Priority.low),
        ],
    ]

    private static let sectionOrder: [Bar: [Section]] = [
        .mail: [.sidebar, .list, .message],
        .boardList: [.sidebar, .list, .detail],
        .boardFlow: [.main],
        .calendar: [.sidebar, .main],
    ]

    /// The toolbars before customization; the new items of T10 are in the
    /// palette only.
    private static let defaults: [Bar: [ID]] = [
        .mail: [
            .mode, .flexibleSpace,
            separator(.list), .toggleSidebar, .newMessage, .contacts, .filter, .refresh, .flexibleSpace,
            separator(.message), .reply, .replyAll, .forward, .flexibleSpace,
            .archive, .trash, .space, .crm, .space, .star, .assistant, .moreActions, .search,
        ],
        .boardList: [
            .mode, .flexibleSpace,
            separator(.list), .toggleSidebar, .boardStyle, .contacts, .flexibleSpace, .boardAccount, .boardTriage,
            separator(.detail), .boardDone, .boardRemind, .boardArchive, .boardTrash, .flexibleSpace, .boardReply,
        ],
        .boardFlow: [
            .mode, .boardStyle, .contacts, .flexibleSpace, .boardAccount, .boardTriage,
        ],
        .calendar: [
            .mode, .flexibleSpace,
            separator(.main), .toggleSidebar, .calendarToday, .calendarStep, .calendarView, .flexibleSpace,
            .calendarSearch, .contacts, .calendarNewEvent, .calendarAssistant,
        ],
    ]

    /// Every item `bar` can hold, spaces and separators aside, in the
    /// palette's order.
    public static func catalog(_ bar: Bar) -> [Item] { catalogs[bar] ?? [] }

    /// `bar`'s sections in order.
    public static func sections(_ bar: Bar) -> [Section] { sectionOrder[bar] ?? [] }

    /// `id`'s entry in `bar`'s catalog.
    public static func lookup(_ bar: Bar, _ id: ID) -> Item? {
        catalog(bar).first { $0.id == id }
    }

    /// An arrangement: item ids in order with the sections' separators.
    public typealias Layout = [ID]

    /// `bar`'s default arrangement.
    public static func defaultLayout(_ bar: Bar) -> Layout { defaults[bar] ?? [] }

    /// What the settings key keeps for a layout.
    public static func tokens(_ l: Layout) -> [String] { l.map(\.rawValue) }

    /// toolbar.Stored: nothing for `bar`'s default, so an untouched toolbar
    /// follows the default of later versions.
    public static func stored(_ bar: Bar, _ l: Layout) -> [String] {
        let n = normalize(bar, tokens(l))
        return n == defaultLayout(bar) ? [] : tokens(n)
    }

    /// toolbar.MaxTokens: what `normalize` reads of a stored list.
    public static let maxTokens = 128

    /// toolbar.Normalize: an empty list is the default; unknown ids, a
    /// second copy of an item and anything past `maxTokens` go; separators
    /// stand in order, each once, the missing ones where they belong; an
    /// item in a section it may not stand in goes to the end of its home
    /// section; a fixed item stands at the start of its home section.
    public static func normalize(_ bar: Bar, _ stored: [String]) -> Layout {
        let secs = sections(bar)
        guard !stored.isEmpty, !secs.isEmpty else { return defaultLayout(bar) }
        let input = stored.prefix(maxTokens)
        var index: [Section: Int] = [:]
        for (i, s) in secs.enumerated() {
            index[s] = i
        }
        var parts = [[ID]](repeating: [], count: secs.count)
        var moved = [[ID]](repeating: [], count: secs.count)
        var seen = Set<ID>()
        var cur = 0
        for tok in input {
            let id = ID(tok)
            if id.isSeparator {
                if let s = Section(rawValue: String(tok.dropFirst())), let i = index[s], i > cur {
                    cur = i
                }
                continue
            }
            if id.isSpace {
                parts[cur].append(id)
                continue
            }
            guard let it = lookup(bar, id), !seen.contains(id), !it.fixed else { continue }
            seen.insert(id)
            if it.sections.contains(secs[cur]) {
                parts[cur].append(id)
            } else if let h = index[it.home] {
                moved[h].append(id)
            }
        }
        var out: Layout = []
        for (i, s) in secs.enumerated() {
            if i > 0 {
                out.append(separator(s))
            }
            for id in defaultLayout(bar) {
                if let it = lookup(bar, id), it.fixed, it.home == s {
                    out.append(id)
                }
            }
            out += parts[i]
            out += moved[i]
        }
        return out
    }

    /// toolbar.SectionAt: the section of the item at index `i` of `l` (a
    /// separator belongs to the section it starts).
    public static func sectionAt(_ bar: Bar, _ l: Layout, _ i: Int) -> Section {
        guard var s = sections(bar).first else { return .main }
        var j = 0
        while j <= i, j < l.count {
            if l[j].isSeparator, let x = Section(rawValue: String(l[j].rawValue.dropFirst())) {
                s = x
            }
            j += 1
        }
        return s
    }

    /// toolbar.CanInsert: spaces go anywhere; separators and fixed items
    /// never move.
    public static func canInsert(_ bar: Bar, _ id: ID, _ s: Section) -> Bool {
        if id.isSpace {
            return true
        }
        if id.isSeparator {
            return false
        }
        guard let it = lookup(bar, id) else { return false }
        return !it.fixed && it.sections.contains(s)
    }

    /// toolbar.Immovable: the fixed items and the separators.
    public static func immovable(_ bar: Bar) -> [ID] {
        catalog(bar).filter(\.fixed).map(\.id) + sections(bar).dropFirst().map { separator($0) }
    }

    /// What the window has at the moment.
    public struct Features: Sendable {
        public var have: Set<Feature>
        /// Separators left out while their section's items still show.
        public var hiddenSeparators: Set<ID>
        /// Sections left out with their items.
        public var hiddenSections: Set<Section>

        public init(have: Set<Feature> = [], hiddenSeparators: Set<ID> = [], hiddenSections: Set<Section> = []) {
            self.have = have
            self.hiddenSeparators = hiddenSeparators
            self.hiddenSections = hiddenSections
        }
    }

    /// toolbar.Allowed: the palette's items, those of a missing feature left out.
    public static func allowed(_ bar: Bar, _ f: Features) -> [ID] {
        catalog(bar).filter { $0.feature.map(f.have.contains) ?? true }.map(\.id)
    }

    /// An entry of what the toolbar holds.
    public struct Shown: Equatable, Sendable {
        public let id: ID
        /// Kept in its place but not shown, its feature missing (T7).
        public let hidden: Bool

        public init(_ id: ID, hidden: Bool = false) {
            self.id = id
            self.hidden = hidden
        }
    }

    /// toolbar.Present: the hidden sections with their separators and the
    /// hidden separators left out, an item of a missing feature kept and hidden.
    public static func present(_ bar: Bar, _ l: Layout, _ f: Features) -> [Shown] {
        var out: [Shown] = []
        var s = sections(bar).first ?? .main
        for id in l {
            if id.isSeparator {
                if let x = Section(rawValue: String(id.rawValue.dropFirst())) {
                    s = x
                }
                if f.hiddenSections.contains(s) || f.hiddenSeparators.contains(id) {
                    continue
                }
                out.append(Shown(id))
                continue
            }
            if f.hiddenSections.contains(s) {
                continue
            }
            let hidden = lookup(bar, id)?.feature.map { !f.have.contains($0) } ?? false
            out.append(Shown(id, hidden: hidden))
        }
        return out
    }

    /// toolbar.MissingActions: the actions More Actions adds because `l`
    /// does not show them, in the catalog's order (T8).
    public static func missingActions(_ bar: Bar, _ l: Layout, _ f: Features) -> [ID] {
        let shown = Set(present(bar, l, f).filter { !$0.hidden }.map(\.id))
        return catalog(bar).filter { it in
            it.inMore && !shown.contains(it.id) && (it.feature.map(f.have.contains) ?? true)
        }.map(\.id)
    }

    /// toolbar.SearchItem: `bar`'s search field, nil for none.
    public static func searchItem(_ bar: Bar) -> ID? {
        switch bar {
        case .mail: .search
        case .calendar: .calendarSearch
        case .boardList, .boardFlow: nil
        }
    }

    /// toolbar.SearchIndex: where ⌘F puts the search field into `l` for the
    /// time of a search while the user took it off (O5); nil while `l` has
    /// it or `bar` has none.
    public static func searchIndex(_ bar: Bar, _ l: Layout) -> Int? {
        guard let id = searchItem(bar), !l.contains(id), let home = lookup(bar, id)?.home else { return nil }
        let order = sections(bar)
        let homeIndex = order.firstIndex(of: home) ?? 0
        for i in l.indices where l[i].isSeparator {
            if let k = order.firstIndex(of: sectionAt(bar, l, i)), k > homeIndex {
                return i
            }
        }
        return l.count
    }
}
