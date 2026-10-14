// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The board's view model: a pure function from a snapshot, the view state
// (style, filters, selection) and the date to everything the three styles
// show — the list's sections, the columns, the Today page, the navigation
// column and the detail of the selected case. Every string of a case is
// cleaned here (`cleanLine`, `cleanBlock`), so the views only lay out
// plain text. The selection rules live here too, so the controller and
// the tests share them.
//
// Swift-first: written here first; the GTK window ports it to
// ui/internal/board with the board itself.

import Foundation

extension Board {
    /// How the board lays the cases out. The raw value is the index of the
    /// toolbar switch's segment and the tag of the View menu's item.
    public enum Style: Int, Sendable, CaseIterable {
        case list
        case columns
        case today

        /// The style's nick in the settings (`board-default-style`,
        /// `board-last-style`); the raw value is an index and is never
        /// stored.
        public var nick: String {
            switch self {
            case .list: "list"
            case .columns: "columns"
            case .today: "today"
            }
        }
    }

    /// The style a stored nick names (the key `board-last-style`); an
    /// unknown or empty one, and "last", is the List.
    public static func parseStyle(_ nick: String) -> Style {
        Style.allCases.first { $0.nick == nick } ?? .list
    }

    /// A value of Board View (the key `board-default-style`): the style the
    /// board shows in, or the one used last.
    public enum DefaultStyle: Hashable, Sendable {
        /// The style the user had last (`board-last-style`).
        case last
        case style(Style)

        /// The choice's value in the settings: "last", "list", "columns",
        /// "today".
        public var nick: String {
            switch self {
            case .last: Board.nickLast
            case .style(let s): s.nick
            }
        }
    }

    /// Board View's choices in Settings' order: Last Used, List, Columns,
    /// Today.
    public static let defaultStyles: [DefaultStyle] = [.last] + Style.allCases.map { .style($0) }

    /// The choice a stored nick of `board-default-style` names: "last" is
    /// Last Used, a style's nick that style, anything else (empty, unknown)
    /// Last Used, the key's default.
    public static func parseDefaultStyle(_ nick: String) -> DefaultStyle {
        Style.allCases.first { $0.nick == nick }.map { .style($0) } ?? .last
    }

    /// The style the board takes as it shows. Once the user picked a style
    /// in this run (`pickedThisRun`) the board keeps it (`current`); before
    /// that it takes Board View: the style used last (`lastStyle`, the key
    /// `board-last-style`) for Last Used, else the chosen one, so a change
    /// of Board View applies the next time the board shows unless the user
    /// already picked a style. The style never changes while the board
    /// shows.
    public static func styleOnShow(
        defaultStyle: DefaultStyle, lastStyle: Style, current: Style, pickedThisRun: Bool
    ) -> Style {
        if pickedThisRun {
            return current
        }
        switch defaultStyle {
        case .last: return lastStyle
        case .style(let s): return s
        }
    }

    /// The account filter the board takes as it shows: the one saved (the
    /// key `board-account-filter`) while that account is still among
    /// `accounts`, else every account.
    public static func filterOnShow(saved: String, accounts: [AccountInfo]) -> AccountFilter {
        guard !saved.isEmpty, let a = accounts.first(where: { $0.id.rawValue == saved }) else { return .all }
        return .account(a.id)
    }

    /// Which cases the list shows.
    public enum Filter: Hashable, Sendable {
        case all
        case state(State)
        case done
        /// The cases off the board until a reminder.
        case snoozed
    }

    /// The navigation's filters in order: Overview, the four states,
    /// Snoozed, Done.
    public static let filters: [Filter] = [.all] + State.allCases.map { .state($0) } + [.snoozed, .done]

    /// Which account's cases the board shows.
    public enum AccountFilter: Hashable, Sendable {
        case all
        case account(AccountID)
    }

    /// What the user chose to look at. Not case data: the source keeps
    /// that.
    public struct ViewState: Sendable, Equatable {
        public var style: Style = .list
        public var filter: Filter = .all
        public var account: AccountFilter = .all
        public var selection: CaseID? = nil
        /// The detail's "Why is this here?" box is open.
        public var revealsWhy = false
        /// The list style has room for the detail beside it; without, the
        /// detail is the sliding panel.
        public var inlineDetail = true

        public init() {}
    }

    /// A case in a list, a column or the Today page. No `selected` field on
    /// purpose: a selection change must not rebuild the rows.
    public struct Row: Sendable, Equatable, Identifiable {
        public var id: CaseID
        public var state: State
        public var person: String
        public var time: String
        public var title: String
        /// `title` is the assistant's (its annotation's title), not the
        /// subject: the views mark it (docs/api.md §4.13).
        public var titleIsAssistant: Bool
        public var snippet: String
        /// `snippet` is the assistant's summary, not the message's text.
        public var snippetIsAssistant: Bool
        public var account: String
        /// "" when the case is no issue.
        public var issueKey: String
        public var issueStatus: String
        public var issueStyle: Jira.StatusStyle
        /// "" without a due date.
        public var due: String
        /// When a snoozed case comes back ("Tomorrow at 09:00"); ""
        /// otherwise.
        public var remind: String
        /// The case is back from a reminder (`Case.reminded`); it is listed
        /// first in its state and carries the badge Reminded.
        public var reminded: Bool
        /// Its sender is someone the user never wrote to
        /// (`you.newContact`); badge New contact.
        public var newContact: Bool
        /// The badges' texts in order (Reminded, New contact); none when
        /// empty.
        public var badges: [String]
        public var attachments: Bool
        /// The message count from two on, "" below.
        public var countText: String
        public var unread: Bool
        /// The row's VoiceOver label ("Assistant:" before the assistant's
        /// title).
        public var spoken: String

        /// The row shows text the assistant wrote (its title or its
        /// summary as the snippet): the views put the assistant's mark in
        /// front of the title.
        public var marksAssistant: Bool { titleIsAssistant || snippetIsAssistant }
    }

    /// A commitment of the user's, with the case it comes from.
    public struct CommitmentRow: Sendable, Equatable, Identifiable {
        public var id: String
        public var caseID: CaseID
        public var text: String
        public var quote: String
        public var due: String
        /// The case's title.
        public var from: String
        /// `from` is the assistant's title of the case, not its subject.
        public var fromIsAssistant: Bool
        /// `from` for VoiceOver ("Assistant:" before the assistant's title).
        public var spokenFrom: String
    }

    public enum SectionKind: Hashable, Sendable {
        case state(State)
        /// Under the Snoozed filter: the cases that come back later, the
        /// soonest first.
        case snoozed
        case done
    }

    /// A section of the list.
    public struct Section: Sendable, Equatable {
        public var kind: SectionKind
        public var title: String
        public var rows: [Row]
    }

    /// A column of the Columns style.
    public struct Column: Sendable, Equatable {
        public var state: State
        public var title: String
        public var rows: [Row]
        public var emptyText: String
    }

    /// A filter in the navigation column.
    public struct NavItem: Sendable, Equatable {
        public var filter: Filter
        public var title: String
        /// The state's colour dot; nil for Overview and Done.
        public var dot: State?
        public var count: Int
        public var selected: Bool
    }

    /// An account in the navigation column and the account menu.
    public struct AccountItem: Sendable, Equatable {
        public var filter: AccountFilter
        public var title: String
        public var badge: String
        /// `title` with `badge` (`Text.titleWithBadge`), for a one-line
        /// menu.
        public var label: String
        /// The cases not done.
        public var count: Int
        public var selected: Bool
    }

    /// A message of the detail's conversation.
    public struct MessageCard: Sendable, Equatable {
        /// nil for the samples.
        public var id: MessageID?
        public var from: String
        public var when: String
        public var text: String
        public var mine: Bool
    }

    /// The selected case in full. An empty string hides its block.
    public struct Detail: Sendable, Equatable {
        public var id: CaseID
        public var accountID: AccountID
        /// nil for the samples.
        public var thread: ThreadID?
        /// What Reply answers and Show in Mail selects; nil for the samples.
        public var reply: ReplyTarget?
        public var latestMessage: MessageID?
        public var state: State
        public var stateTitle: String
        public var source: StateSource
        public var why: String
        /// Lines "Why is this here?" adds after `why` and `sourceText`:
        /// `Text.reasonReminded` for a case back from a reminder,
        /// `Text.reasonUserKeeps` when the user chose its state.
        public var whyNotes: [String]
        /// `why` is the assistant's reason, not the rules': the box leads
        /// it with the assistant's mark.
        public var whyIsAssistant: Bool
        public var sourceText: String
        public var account: String
        public var issue: IssueInfo?
        public var person: String
        public var time: String
        /// `person` and `time` as one line (`Text.personAndTime`).
        public var byline: String
        /// The row's (`Row`).
        public var reminded: Bool
        public var newContact: Bool
        public var badges: [String]
        public var title: String
        /// `title` is the assistant's, not the subject: the detail marks it.
        public var titleIsAssistant: Bool
        /// `title` for VoiceOver ("Assistant:" before the assistant's).
        public var spokenTitle: String
        /// "" = hidden: shown only when the assistant's title differs.
        public var subject: String
        public var due: String
        public var dueQuote: String
        public var summary: String
        public var tasks: [String]
        /// The suggested reply's plain text: the samples' static block
        /// shows it (the daemon's board edits the draft inline instead,
        /// `BoardReplyEditorController`).
        public var draft: String
        /// The suggested reply's draft, whatever its text (an empty draft
        /// is still edited inline); shown while the draft exists, also
        /// after the notes went stale.
        public var draftID: DraftID?
        /// Unstar is offered: the case is on the board because of a star
        /// (`hot.flagged`) and not done (`BoardSource.unflag`).
        public var canUnstar: Bool
        /// "" or `Text.staleNotes`: the assistant's notes no longer count.
        public var staleNote: String
        public var isDone: Bool
        public var isSnoozed: Bool
        /// "" or "Back on the board: Tomorrow at 09:00".
        public var remindText: String
        /// Archive moves messages; without, it only marks the case done.
        public var canArchive: Bool
        public var conversationTitle: String
        /// Oldest first; the last loaded while a newer version loads.
        public var messages: [MessageCard]
        /// The conversation has not arrived yet (`messagesNote` says so).
        public var messagesLoading: Bool
        /// "", `Text.messagesLoading` or `Text.messagesFailed`, shown in
        /// place of the cards.
        public var messagesNote: String
        /// The conversation could not be loaded: the note offers
        /// `Text.tryAgain` (`BoardController.retryMessages`).
        public var messagesRetry: Bool
    }

    /// The Today page's deadline groups; the raw value is the order.
    public enum DueGroupKind: Int, Sendable, CaseIterable {
        case overdue
        case today
        case tomorrow
        /// Two to seven days ahead ("Next 7 Days").
        case thisWeek
        case later
    }

    public struct DueItem: Sendable, Equatable {
        public var caseID: CaseID
        public var label: String
        public var title: String
        /// `title` is the assistant's, not the subject.
        public var titleIsAssistant: Bool
        /// `title` for VoiceOver ("Assistant:" before the assistant's).
        public var spokenTitle: String
        public var person: String
        public var quote: String
    }

    public struct DueGroup: Sendable, Equatable {
        public var kind: DueGroupKind
        public var title: String
        public var items: [DueItem]
    }

    public enum TileKind: Hashable, Sendable {
        case state(State)
        case commitments
    }

    /// A count tile of the Today page ("3 Hot").
    public struct Tile: Sendable, Equatable {
        public var kind: TileKind
        public var count: Int
        public var title: String
        /// The tile's count and title in one sentence (`Text.tileToolTip`).
        public var toolTip: String { Text.tileToolTip(self) }
    }

    /// The Today page.
    public struct Today: Sendable, Equatable {
        public var title: String
        /// `Text.todoPhrase` of the hot cases and those waiting for the
        /// user that need the user today (`Board.needsYouToday`).
        public var phrase: String
        /// The four states, and the commitments when annotations count.
        public var tiles: [Tile]
        public var hot: [Row]
        /// The first `youTopCount` cases waiting for the user.
        public var you: [Row]
        /// The cases waiting for the user beyond `you`.
        public var youMore: Int
        public var commitments: [CommitmentRow]
        /// The non-empty groups, in `DueGroupKind` order.
        public var dueGroups: [DueGroup]
        public var dueEmpty: String
    }

    /// Everything the board shows.
    public struct View: Sendable, Equatable {
        /// How far the source's data is.
        public var phase: Phase
        /// The empty board's texts for the phase (`isEmpty`).
        public var emptyTitle: String
        public var emptyBody: String
        /// A line above the cases while they are partial or old; "" when
        /// none.
        public var notice: String
        /// The triage's status (only carried for now) and its last run.
        public var triage: Triage
        public var run: Run?
        public var nav: [NavItem]
        public var accounts: [AccountItem]
        public var accountTitle: String
        /// The window's subtitle: "All Accounts · 23 cases".
        public var subtitle: String
        /// No case at all in the account scope (live, snoozed or done).
        public var isEmpty: Bool
        /// The list's non-empty sections.
        public var sections: [Section]
        public var sectionsEmptyText: String
        public var commitments: [CommitmentRow]
        public var showsCommitmentsInList: Bool
        /// Always the four states, whatever the filter.
        public var columns: [Column]
        public var today: Today
        public var detail: Detail?
        public var showsPanel: Bool
        public var statusLine: String
        /// Whether the assistant is on (the snapshot's `annotated`).
        public var assistantOn: Bool
        /// The selection, resolved (`resolveSelection`).
        public var selection: CaseID?
    }

    /// The cases waiting for the user the Today page lists.
    public static let youTopCount = 5

    // MARK: The view

    public static func view(_ s: Snapshot, _ v: ViewState, now: Date, calendar: Calendar = .current) -> View {
        let scope = Scope(s, v)
        let ctx = Context(snapshot: s, now: now, calendar: calendar)
        let rowOf = { (c: Case) in ctx.row(c) }

        // The columns, and their counts for the navigation and the tiles.
        let columns = State.allCases.map { st in
            Column(
                state: st, title: Text.stateName(st), rows: scope.live.filter { scope.state($0) == st }.map(rowOf),
                emptyText: Text.columnEmpty(st)
            )
        }
        let count = { (st: State) in columns.first { $0.state == st }?.rows.count ?? 0 }

        // The list: the columns' rows under the filter.
        var sections: [Section] = []
        switch v.filter {
        case .all, .state:
            for col in columns where !col.rows.isEmpty && (v.filter == .all || v.filter == .state(col.state)) {
                sections.append(Section(kind: .state(col.state), title: col.title, rows: col.rows))
            }
        case .snoozed:
            if !scope.snoozed.isEmpty {
                sections.append(Section(kind: .snoozed, title: Text.snoozed, rows: scope.snoozed.map(rowOf)))
            }
        case .done:
            if !scope.finished.isEmpty {
                sections.append(Section(kind: .done, title: Text.done, rows: scope.finished.map(rowOf)))
            }
        }

        // The commitments: of live cases in the scope, when annotations
        // count; the first `Cap.commitments` shown, all of them counted.
        var commitments: [CommitmentRow] = []
        var commitmentCount = 0
        if s.annotated {
            let live = Dictionary(scope.live.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
            for k in s.commitments where k.state == .open {
                guard let c = live[k.caseID] else { continue }
                commitmentCount += 1
                guard commitments.count < Cap.commitments else { continue }
                let from = ctx.titled(c)
                commitments.append(
                    CommitmentRow(
                        id: k.id, caseID: c.id, text: cleanLine(k.text, max: Cap.commitment),
                        quote: cleanLine(k.quote, max: Cap.quote), due: k.due.map { ctx.dueLabel($0) } ?? "",
                        from: from.text, fromIsAssistant: from.assistant, spokenFrom: from.spoken
                    ))
            }
        }

        // The navigation column.
        var nav = [NavItem(filter: .all, title: Text.filterTitle(.all), dot: nil, count: scope.live.count, selected: v.filter == .all)]
        for st in State.allCases {
            nav.append(
                NavItem(
                    filter: .state(st), title: Text.filterTitle(.state(st)), dot: st, count: count(st),
                    selected: v.filter == .state(st)))
        }
        nav.append(
            NavItem(
                filter: .snoozed, title: Text.filterTitle(.snoozed), dot: nil, count: scope.snoozed.count,
                selected: v.filter == .snoozed))
        nav.append(
            NavItem(
                filter: .done, title: Text.filterTitle(.done), dot: nil, count: scope.finished.count,
                selected: v.filter == .done))

        var accounts = [
            AccountItem(
                filter: .all, title: Text.allAccounts, badge: "", label: Text.allAccounts,
                count: scope.unique.filter(\.visibility.isLive).count, selected: v.account == .all)
        ]
        for a in s.accounts {
            let name = cleanLine(a.name, max: Cap.account)
            let badge = cleanLine(a.badge, max: Cap.badge)
            accounts.append(
                AccountItem(
                    filter: .account(a.id), title: name, badge: badge, label: Text.titleWithBadge(name, badge),
                    count: scope.unique.filter { $0.visibility.isLive && $0.account == a.id }.count,
                    selected: v.account == .account(a.id)))
        }
        let accountTitle: String
        switch v.account {
        case .all: accountTitle = Text.allAccounts
        case .account(let id): accountTitle = ctx.accountName(id)
        }

        // The Today page.
        let hot = columns[0].rows
        let you = columns[1].rows
        var tiles = State.allCases.map { Tile(kind: .state($0), count: count($0), title: Text.stateName($0)) }
        if s.annotated {
            tiles.append(Tile(kind: .commitments, count: commitmentCount, title: Text.commitments))
        }
        let todo = scope.live.filter { c in
            let st = scope.state(c)
            return (st == .hot || st == .you) && needsYouToday(c, annotated: s.annotated, now: now, calendar: calendar)
        }.count
        let today = Today(
            title: Text.styleTitle(.today), phrase: Text.todoPhrase(todo), tiles: tiles, hot: hot,
            you: Array(you.prefix(youTopCount)), youMore: max(0, you.count - youTopCount), commitments: commitments,
            dueGroups: dueGroups(scope, ctx), dueEmpty: Text.dueEmpty
        )

        // The selection and its detail.
        let selection = scope.resolve(v.selection)
        let detail = selection.flatMap { id in scope.unique.first { $0.id == id } }.map { ctx.detail($0) }

        let isEmpty = scope.live.isEmpty && scope.finished.isEmpty && scope.snoozed.isEmpty
        return View(
            phase: s.phase, emptyTitle: Text.emptyTitle(s.phase), emptyBody: Text.emptyBody(s.phase),
            notice: Text.notice(s.phase, truncated: s.truncated), triage: s.triage, run: s.run, nav: nav, accounts: accounts, accountTitle: accountTitle,
            subtitle: accountTitle + " · " + Text.caseCount(scope.live.count),
            isEmpty: isEmpty, sections: sections,
            sectionsEmptyText: Text.sectionEmpty, commitments: commitments,
            showsCommitmentsInList: !commitments.isEmpty && v.filter == .all, columns: columns, today: today,
            detail: detail, showsPanel: selection != nil && (v.style != .list || !v.inlineDetail),
            statusLine: Text.statusLine(annotated: s.annotated, run: s.run), assistantOn: s.annotated,
            selection: selection
        )
    }

    /// The selection the board shows: `v.selection` while that case is
    /// shown (in the list: under its filter; in Columns and Today: live in
    /// the account scope), else none — except that the list with its
    /// detail beside it selects its first row.
    public static func resolveSelection(_ s: Snapshot, _ v: ViewState) -> CaseID? {
        Scope(s, v).resolve(v.selection)
    }

    /// The selection after the case `id` leaves the list (done, reopened
    /// or moved out of the filter), computed on the snapshot from before:
    /// the next row, else the previous one. Columns and Today select
    /// nothing; nil also when `id` is not in the list.
    public static func selectionAfterDone(_ id: CaseID, _ s: Snapshot, _ v: ViewState) -> CaseID? {
        guard v.style == .list else { return nil }
        let shown = Scope(s, v).shown
        guard let i = shown.firstIndex(of: id) else { return nil }
        if i + 1 < shown.count {
            return shown[i + 1]
        }
        return i > 0 ? shown[i - 1] : nil
    }

    /// The deadline group of `due`, by calendar days from `now`: before
    /// today, today, tomorrow, the rest of the next seven days, later.
    public static func dueGroup(_ due: Date, now: Date, calendar: Calendar) -> DueGroupKind {
        let days = dayDifference(from: now, to: due, calendar: calendar)
        switch days {
        case ..<0: return .overdue
        case 0: return .today
        case 1: return .tomorrow
        case 2...7: return .thisWeek
        default: return .later
        }
    }

    /// Whether the Today page's phrase counts case `c` (for the hot cases
    /// and those waiting for the user): new since yesterday's midnight in
    /// `calendar` (its latest activity), due today (its annotation counts
    /// and its deadline is today), or back from a reminder.
    public static func needsYouToday(_ c: Case, annotated: Bool, now: Date, calendar: Calendar) -> Bool {
        if c.reminded {
            return true
        }
        if dayDifference(from: c.date, to: now, calendar: calendar) <= 1 {
            return true
        }
        if let due = annotation(of: c, annotated: annotated)?.due,
           dayDifference(from: now, to: due, calendar: calendar) == 0
        {
            return true
        }
        return false
    }

    // MARK: Helpers

    static func dayDifference(from a: Date, to b: Date, calendar: Calendar) -> Int {
        calendar.dateComponents([.day], from: calendar.startOfDay(for: a), to: calendar.startOfDay(for: b)).day ?? 0
    }

    private static func dueGroups(_ scope: Scope, _ ctx: Context) -> [DueGroup] {
        guard scope.snapshot.annotated else { return [] }
        var dated: [(Case, Date)] = []
        for c in scope.live {
            if let due = annotation(of: c, annotated: true)?.due {
                dated.append((c, due))
            }
        }
        dated.sort { a, b in
            if a.1 != b.1 {
                return a.1 < b.1
            }
            return a.0.id.rawValue < b.0.id.rawValue
        }
        var groups: [DueGroupKind: [DueItem]] = [:]
        for (c, due) in dated {
            let kind = dueGroup(due, now: ctx.now, calendar: ctx.calendar)
            let title = ctx.titled(c)
            groups[kind, default: []].append(
                DueItem(
                    caseID: c.id, label: ctx.dueLabel(due), title: title.text, titleIsAssistant: title.assistant,
                    spokenTitle: title.spoken,
                    person: cleanLine(c.person, max: Cap.person),
                    quote: cleanLine(annotation(of: c, annotated: true)?.dueQuote ?? "", max: Cap.quote)))
        }
        return DueGroupKind.allCases.compactMap { k in
            groups[k].map { DueGroup(kind: k, title: Text.dueGroupTitle(k), items: $0) }
        }
    }

    /// The cases in the account scope, ordered, and what the list shows.
    private struct Scope {
        let snapshot: Snapshot
        let view: ViewState
        /// The snapshot's cases, the first of each id only: a source that
        /// repeats an id must not give two rows one identity.
        let unique: [Case]
        /// On the board, in the account scope: by state, then newest first.
        let live: [Case]
        /// Done, in the account scope: newest first.
        let finished: [Case]
        /// Snoozed, in the account scope: the soonest back first.
        let snoozed: [Case]

        init(_ s: Snapshot, _ v: ViewState) {
            snapshot = s
            view = v
            var seen = Set<CaseID>()
            unique = s.cases.filter { seen.insert($0.id).inserted }
            let inScope = unique.filter { c in
                switch v.account {
                case .all: return true
                case .account(let id): return c.account == id
                }
            }
            let rank = Dictionary(uniqueKeysWithValues: State.allCases.enumerated().map { ($1, $0) })
            let annotated = s.annotated
            live = inScope.filter(\.visibility.isLive).sorted { a, b in
                let ra = rank[Board.state(of: a, annotated: annotated)] ?? 0
                let rb = rank[Board.state(of: b, annotated: annotated)] ?? 0
                if ra != rb {
                    return ra < rb
                }
                // Back from a reminder first in its state.
                if a.reminded != b.reminded {
                    return a.reminded
                }
                return Scope.newer(a, b)
            }
            finished = inScope.filter(\.visibility.isDone).sorted(by: Scope.newer)
            snoozed = inScope.filter { $0.visibility.remindAt != nil }.sorted { a, b in
                let ra = a.visibility.remindAt ?? .distantFuture
                let rb = b.visibility.remindAt ?? .distantFuture
                return ra != rb ? ra < rb : Scope.newer(a, b)
            }
        }

        static func newer(_ a: Case, _ b: Case) -> Bool {
            a.date != b.date ? a.date > b.date : a.id.rawValue < b.id.rawValue
        }

        func state(_ c: Case) -> State {
            Board.state(of: c, annotated: snapshot.annotated)
        }

        /// The ids the current style shows, in order: the list's rows under
        /// its filter, or every live case for Columns and Today.
        var shown: [CaseID] {
            guard view.style == .list else { return live.map(\.id) }
            switch view.filter {
            case .all: return live.map(\.id)
            case .state(let st): return live.filter { state($0) == st }.map(\.id)
            case .snoozed: return snoozed.map(\.id)
            case .done: return finished.map(\.id)
            }
        }

        func resolve(_ selection: CaseID?) -> CaseID? {
            let shown = shown
            if let selection, shown.contains(selection) {
                return selection
            }
            return view.style == .list && view.inlineDetail ? shown.first : nil
        }
    }

    /// What every row and detail is built with: the accounts, the date and
    /// how to write it.
    private struct Context {
        let snapshot: Snapshot
        let now: Date
        let calendar: Calendar
        let locale: Locale
        let accounts: [AccountID: AccountInfo]

        init(snapshot: Snapshot, now: Date, calendar: Calendar) {
            self.snapshot = snapshot
            self.now = now
            self.calendar = calendar
            locale = calendar.locale ?? .current
            accounts = Dictionary(snapshot.accounts.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
        }

        var annotated: Bool { snapshot.annotated }

        func accountName(_ id: AccountID) -> String {
            cleanLine(accounts[id]?.name ?? "", max: Cap.account)
        }

        func subject(_ c: Case) -> String {
            subjectText(cleanLine(c.subject, max: Cap.title))
        }

        /// The assistant's title when its annotation counts, else the
        /// subject; `assistant` says which, `spoken` is the title for
        /// VoiceOver ("Assistant:" before the assistant's).
        func titled(_ c: Case) -> (text: String, assistant: Bool, spoken: String) {
            if let a = annotation(c) {
                let t = cleanLine(a.title, max: Cap.title)
                if !t.isEmpty {
                    return (t, true, Text.spokenAssistant(t))
                }
            }
            let s = subject(c)
            return (s, false, s)
        }

        /// The annotation, when it counts (`Board.annotation(of:annotated:)`).
        func annotation(_ c: Case) -> Annotation? {
            Board.annotation(of: c, annotated: annotated)
        }

        /// "Tomorrow at 09:00", "20 Oct at 09:00": when a snoozed case
        /// comes back.
        func remindLabel(_ at: Date) -> String {
            Text.dayAndTime(dueLabel(at), formatTime(at, locale: locale, calendar: calendar))
        }

        /// A case's badges' texts: Reminded, New contact.
        func badges(_ c: Case) -> [String] {
            var out: [String] = []
            if c.reminded {
                out.append(Text.reminded)
            }
            if c.newContact {
                out.append(Text.newContact)
            }
            return out
        }

        func dueLabel(_ due: Date) -> String {
            switch dueGroup(due, now: now, calendar: calendar) {
            case .today: return Text.dueGroupTitle(.today)
            case .tomorrow: return Text.dueGroupTitle(.tomorrow)
            default:
                let sameYear = calendar.component(.year, from: due) == calendar.component(.year, from: now)
                // The list's date formats (formatDate).
                return format(due, sameYear ? L10n.T("%-d %b") : L10n.T("%Y-%m-%d"))
            }
        }

        private func format(_ t: Date, _ strftime: String) -> String {
            let f = Strftime.formatter(strftime, locale: locale)
            f.calendar = calendar
            f.timeZone = calendar.timeZone
            return f.string(from: t)
        }

        func row(_ c: Case) -> Row {
            let st = Board.state(of: c, annotated: annotated)
            let person = cleanLine(c.person, max: Cap.person)
            let title = titled(c)
            var snippet = cleanLine(annotation(c)?.summary ?? "", max: Cap.snippet)
            let snippetIsAssistant = !snippet.isEmpty
            if snippet.isEmpty {
                snippet = cleanLine(c.snippet, max: Cap.snippet)
            }
            let issueKey = cleanLine(c.issue?.key ?? "", max: Cap.issueKey)
            let issueStatus = cleanLine(c.issue?.status ?? "", max: Cap.status)
            let due = annotation(c)?.due.map { dueLabel($0) } ?? ""
            let n = max(1, c.messageCount)

            let badges = badges(c)
            var spoken = [Text.stateName(st)] + badges + [person, title.spoken]
            if !due.isEmpty {
                spoken.append(Text.spokenDue(due))
            }
            if !issueKey.isEmpty {
                spoken.append(issueStatus.isEmpty ? issueKey : issueKey + ", " + issueStatus)
            }
            if n > 1 {
                spoken.append(Text.messageCount(n))
            }
            if let at = c.visibility.remindAt {
                spoken.append(Text.spokenRemind(remindLabel(at)))
            }
            if c.hasAttachments {
                spoken.append(Text.spokenAttachments)
            }
            if c.unread {
                spoken.append(Text.spokenUnread)
            }
            return Row(
                id: c.id, state: st, person: person, time: formatDate(c.date, now: now, locale: locale, calendar: calendar),
                title: title.text, titleIsAssistant: title.assistant, snippet: snippet,
                snippetIsAssistant: snippetIsAssistant, account: accountName(c.account), issueKey: issueKey,
                issueStatus: issueStatus, issueStyle: c.issue?.style ?? .plain, due: due,
                remind: c.visibility.remindAt.map { remindLabel($0) } ?? "", reminded: c.reminded,
                newContact: c.newContact, badges: badges, attachments: c.hasAttachments,
                countText: threadCountText(n), unread: c.unread, spoken: sentences(spoken)
            )
        }

        func detail(_ c: Case) -> Detail {
            let st = Board.state(of: c, annotated: annotated)
            let source = stateSource(of: c, annotated: annotated)
            let a = annotation(c)
            let title = titled(c)
            let subject = subject(c)

            var why = cleanLine(a?.why ?? "", max: Cap.reason)
            let whyIsAssistant = !why.isEmpty
            if why.isEmpty {
                why = Text.reason(c.ruleReason)
            }
            // The summary box is the assistant's: no snippet stands in for
            // it (the row's snippet does fall back, and the conversation
            // shows the text anyway).
            let summary = cleanBlock(a?.summary ?? "", max: Cap.summary)
            let issue = c.issue.map {
                IssueInfo(
                    key: cleanLine($0.key, max: Cap.issueKey), status: cleanLine($0.status, max: Cap.status),
                    style: $0.style)
            }
            let tasks = (a?.tasks ?? []).prefix(Cap.taskScan).lazy.map { cleanLine($0, max: Cap.task) }
                .filter { !$0.isEmpty }
            let messages = newest(c.messages ?? [], Cap.messages).map { m in
                MessageCard(
                    id: m.id, from: m.mine ? Text.you : cleanLine(m.from, max: Cap.person),
                    when: formatDate(m.date, now: now, locale: locale, calendar: calendar),
                    text: cleanBlock(m.text, max: Cap.message), mine: m.mine)
            }
            let messagesNote: String
            if c.messages != nil {
                messagesNote = ""
            } else {
                messagesNote = c.messagesFailed ? Text.messagesFailed : Text.messagesLoading
            }
            let draft = cleanBlock(c.draft?.text ?? "", max: Cap.draft)
            let stale = annotated && c.annotation?.stale == true
            var whyNotes: [String] = []
            if c.reminded {
                whyNotes.append(Text.reasonReminded)
            }
            if c.userState != nil {
                whyNotes.append(Text.reasonUserKeeps)
            }
            let person = cleanLine(c.person, max: Cap.person)
            let when = formatDateTime(c.date, locale: locale, calendar: calendar)
            return Detail(
                id: c.id, accountID: c.account, thread: c.thread, reply: c.reply, latestMessage: c.latestMessage,
                state: st, stateTitle: Text.stateName(st), source: source, why: why, whyNotes: whyNotes,
                whyIsAssistant: whyIsAssistant, sourceText: Text.sourceText(source, run: snapshot.run),
                account: accountName(c.account), issue: issue, person: person, time: when,
                byline: person.isEmpty ? when : when.isEmpty ? person : Text.personAndTime(person, when), reminded: c.reminded, newContact: c.newContact,
                badges: badges(c), title: title.text,
                titleIsAssistant: title.assistant, spokenTitle: title.spoken,
                subject: title.text == subject ? "" : subject, due: a?.due.map { dueLabel($0) } ?? "",
                dueQuote: a?.due == nil ? "" : cleanLine(a?.dueQuote ?? "", max: Cap.quote), summary: summary,
                tasks: Array(tasks.prefix(Cap.tasks)), draft: draft, draftID: c.draft?.id,
                canUnstar: Board.canUnstar(c),
                staleNote: stale ? Text.staleNotes : "", isDone: c.visibility.isDone,
                isSnoozed: c.visibility.remindAt != nil,
                remindText: c.visibility.remindAt.map { Text.snoozedUntil(remindLabel($0)) } ?? "",
                canArchive: c.canArchive, conversationTitle: Text.conversation(max(1, c.messageCount)),
                messages: messages, messagesLoading: c.messages == nil && !c.messagesFailed,
                messagesNote: messagesNote, messagesRetry: c.messages == nil && c.messagesFailed
            )
        }

        /// The newest `n` of `ms`, oldest first, as a stable sort by date
        /// would leave them (of equal dates, the later in `ms` is newer),
        /// without sorting all of `ms`.
        private func newest(_ ms: [CaseMessage], _ n: Int) -> [CaseMessage] {
            guard n > 0 else { return [] }
            // Indices into `ms`, ordered by date and index.
            var kept: [Int] = []
            kept.reserveCapacity(Swift.min(n, ms.count))
            for i in ms.indices {
                let d = ms[i].date
                if kept.count == n {
                    guard d >= ms[kept[0]].date else { continue }
                    kept.removeFirst()
                }
                var j = kept.count
                while j > 0, ms[kept[j - 1]].date > d {
                    j -= 1
                }
                kept.insert(i, at: j)
            }
            return kept.map { ms[$0] }
        }

        /// The parts as sentences: each ends with a full stop unless it
        /// ends with punctuation already; empty parts go.
        private func sentences(_ parts: [String]) -> String {
            parts.filter { !$0.isEmpty }.map { p in
                guard let last = p.last, !".?!…".contains(last) else { return p }
                return p + "."
            }.joined(separator: " ")
        }
    }
}
