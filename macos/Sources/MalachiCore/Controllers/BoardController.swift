// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The board, the controller half: what the user chose to look at (the
// style, the filters, the selection, the "Why is this here?" box) and the
// view model built from it and the source's snapshot (`Board.view`). No
// AppKit: the board page and its styles lay the view model out, ask for
// changes here and redraw what `onChange` names. What the user decides
// about a case (its state, done, a remind, archive, a discarded draft, a
// promise ticked off) goes to the source, which keeps it; the view model
// follows once the source reports it. Selecting a case asks the source for
// its conversation (`BoardSource.loadMessages`); the detail says it loads
// until it arrives. The source's toasts (a refused write, what Archive did)
// reach the page through `onToast`.
//
// Swift-first: the GTK window ports it to ui/internal/board with the
// board itself.

/// The board's view state and view model. One observer (`onChange`): the
/// board page, which fans the changes out to its children.
@MainActor
public final class BoardController {
    /// What changed, for the page. Several at once are possible.
    public struct Changes: OptionSet, Sendable {
        public let rawValue: Int
        public init(rawValue: Int) { self.rawValue = rawValue }

        /// The cases shown changed (rows, sections, columns, the Today
        /// page, the counts, or the selected case's detail itself).
        public static let content = Changes(rawValue: 1 << 0)
        /// Another case is selected, none is, the panel opens or closes, or
        /// the "Why is this here?" box opens or closes (`state.revealsWhy`).
        public static let selection = Changes(rawValue: 1 << 1)
        /// `state.style` changed.
        public static let style = Changes(rawValue: 1 << 2)
        /// `state.filter` or `state.account` changed.
        public static let filters = Changes(rawValue: 1 << 3)
    }

    public let source: any BoardSource
    /// What the user looks at; `selection` is always the resolved one
    /// (`view.selection`).
    public private(set) var state: Board.ViewState
    public private(set) var view: Board.View
    /// Called after every change of `state` or `view`, with what changed.
    public var onChange: (@MainActor (Changes) -> Void)?
    /// Called with a short sentence for a toast: a write the source could
    /// not make (undone by then), or what Archive did.
    public var onToast: (@MainActor (String) -> Void)?

    private let now: @MainActor () -> Date
    private let calendar: Calendar
    /// The style the board opens in the first time it shows in a run (the
    /// settings' `board-default-style`), asked for at that moment.
    private let defaultStyle: @MainActor () -> Board.Style
    /// Whether the board has shown in this run (`boardWillShow`).
    public private(set) var hasShown = false
    /// Where the selection goes when the selected case leaves what is
    /// shown after the user's own write (done, reopened, moved out of the
    /// filter): computed before the write, used by the first report of the
    /// source that follows it (which a source may send later) and then
    /// dropped, whatever that report holds. A write that changes nothing
    /// notes none, and any view state the user asks for meanwhile drops it.
    private var departure: (id: Board.CaseID, next: Board.CaseID?)?
    /// Changes not yet delivered to `onChange`, and whether a delivery is
    /// under way: a listener that calls the controller from `onChange` gets
    /// that change after its own call returns, not inside it.
    private var pending: Changes = []
    private var notifying = false
    /// The case and version whose conversation was last asked for, and
    /// the phase then: asked again when another case is selected or the
    /// case changed, not on every report. A failed load is asked again
    /// when the user selects the case again or asks to (`retryMessages`),
    /// and when the board came back from a failure (a reconnect).
    private var requested: (id: Board.CaseID, version: Int64, phase: Board.Phase)?

    /// Installs itself as the source's `onChange`. `defaultStyle` is the
    /// style of the first show (`boardWillShow`); until then the board
    /// holds the List.
    public init(
        source: any BoardSource,
        now: @escaping @MainActor () -> Date = { Date() },
        calendar: Calendar = .current,
        defaultStyle: @escaping @MainActor () -> Board.Style = { .list }
    ) {
        self.source = source
        self.now = now
        self.calendar = calendar
        self.defaultStyle = defaultStyle
        var state = Board.ViewState()
        state.selection = Board.resolveSelection(source.snapshot, state)
        self.state = state
        view = Board.view(source.snapshot, state, now: now(), calendar: calendar)
        source.onChange = { [weak self] in
            self?.refresh()
        }
        source.onError = { [weak self] text in
            self?.onToast?(text)
        }
        source.onNotice = { [weak self] text in
            self?.onToast?(text)
        }
        requestMessages()
    }

    /// How far the source's data is (`view.phase` once built).
    public var phase: Board.Phase { source.snapshot.phase }

    /// The remind presets for now (`Board.remindPresets`).
    public func remindPresets() -> [Board.RemindPreset] {
        Board.remindPresets(now: now(), calendar: calendar)
    }

    // MARK: What the user looks at

    /// Columns and Today start with nothing selected; the list selects its
    /// first row when its detail is beside it.
    public func setStyle(_ s: Board.Style) {
        guard s != state.style else { return }
        var next = state
        next.style = s
        if s != .list {
            next.selection = nil
        }
        apply(next)
    }

    /// Clears the selection (the list then selects its first row).
    public func setFilter(_ f: Board.Filter) {
        guard f != state.filter else { return }
        var next = state
        next.filter = f
        next.selection = nil
        apply(next)
    }

    /// Clears the selection (the list then selects its first row).
    public func setAccount(_ a: Board.AccountFilter) {
        guard a != state.account else { return }
        var next = state
        next.account = a
        next.selection = nil
        apply(next)
    }

    /// Selects `id`, or nothing. A case that is not shown selects nothing;
    /// in the list with its detail beside it, nothing is its first row.
    public func select(_ id: Board.CaseID?) {
        if id != nil, id == state.selection, failedLoad(of: id) {
            // Selected again: its conversation is asked for again.
            requested = nil
        }
        var next = state
        next.selection = id
        apply(next)
    }

    /// Whether the list has room for the detail beside it. Folding the
    /// detail away also clears the selection, so the panel never slides in
    /// by itself when the window narrows; unfolding selects the first row.
    public func setInlineDetail(_ on: Bool) {
        guard on != state.inlineDetail else { return }
        var next = state
        next.inlineDetail = on
        if !on {
            next.selection = nil
        }
        apply(next)
    }

    /// Opens or closes the detail's "Why is this here?" box. Nothing without
    /// a selection.
    public func toggleWhy() {
        guard state.selection != nil else { return }
        var next = state
        next.revealsWhy.toggle()
        apply(next)
    }

    /// The list filtered to the cases waiting for the user (the Today
    /// page's "and N more").
    public func showWaitingForYou() {
        var next = state
        next.style = .list
        next.filter = .state(.you)
        if next.style != state.style || next.filter != state.filter {
            next.selection = nil
        }
        apply(next)
    }

    /// Builds the view model anew: after the source changed, or when the
    /// date may have (a new day moves the deadlines).
    public func refresh() {
        apply(state, user: false)
    }

    /// The board is about to show (the window enters Board mode, before
    /// its page is laid out): the first time in a run it takes the default
    /// style, later it keeps the user's last one (`Board.styleOnShow`).
    public func boardWillShow() {
        let first = !hasShown
        hasShown = true
        setStyle(Board.styleOnShow(current: state.style, defaultStyle: defaultStyle(), firstShow: first))
    }

    /// The board shows again (the window entered Board mode): a board that
    /// could not be listed is asked for again at once, then the view model
    /// is built anew (`refresh`).
    public func boardShown() {
        if phase.isFailure {
            source.refresh()
        }
        refresh()
    }

    /// The detail's Try Again: asks for the selected case's conversation
    /// again after it could not be loaded.
    public func retryMessages() {
        guard failedLoad(of: state.selection) else { return }
        requested = nil
        requestMessages()
    }

    // MARK: What the user decides about a case

    /// Moves the case to `s`. Moving it to the state it would have by
    /// itself (the assistant's, or the rules') puts it back to automatic.
    public func setState(_ s: Board.State, of id: Board.CaseID) {
        let snapshot = source.snapshot
        guard var c = snapshot.cases.first(where: { $0.id == id }) else { return }
        let current = c.userState
        c.userState = nil
        let automatic = Board.state(of: c, annotated: snapshot.annotated)
        let value = s == automatic ? nil : s
        write(id, changes: value != current) {
            source.setState(value, of: id)
        }
    }

    /// Marks the case done. The list selects the next row, else the
    /// previous one; Columns and Today select nothing.
    public func markDone(_ id: Board.CaseID) {
        write(id, changes: source.snapshot.cases.first { $0.id == id }?.done == false) {
            source.setDone(true, of: id)
        }
    }

    /// Puts a done case back on the board.
    public func reopen(_ id: Board.CaseID) {
        write(id, changes: source.snapshot.cases.first { $0.id == id }?.done == true) {
            source.setDone(false, of: id)
        }
    }

    /// Hides the case until `until`; nil puts a snoozed case back on the
    /// board. The selection moves on as after done.
    public func remind(_ id: Board.CaseID, until: Date?) {
        let c = source.snapshot.cases.first { $0.id == id }
        let changes: Bool
        if let until {
            changes = c.map { $0.visibility != .snoozed(until: until) } ?? false
        } else {
            changes = c?.visibility.remindAt != nil
        }
        write(id, changes: changes) {
            source.remind(until: until, of: id)
        }
    }

    /// Archives the case (where its account can) and marks it done; the
    /// toast says what it did. The selection moves on as after done.
    public func archive(_ id: Board.CaseID) {
        let c = source.snapshot.cases.first { $0.id == id }
        write(id, changes: c.map { !$0.visibility.isDone } ?? false) {
            source.archive(id)
        }
    }

    /// Ticks a promise off, or reopens it.
    public func setCommitmentDone(_ id: String, done: Bool) {
        departure = nil
        source.setCommitmentDone(done, of: id)
    }

    /// Drops the assistant's suggested reply.
    public func discardDraft(_ id: Board.CaseID) {
        source.discardDraft(of: id)
    }

    /// The inline reply editor's Discard: deletes `draft` (the one the
    /// editor edits, whatever the case links by now) and answers when that
    /// is done; throws when it was refused (`BoardSource.discardDraft(_:account:of:)`).
    public func discardDraft(_ id: Board.CaseID, draft: DraftID, account: AccountID) async throws {
        try await source.discardDraft(draft, account: account, of: id)
    }

    /// Unstar: removes the star that keeps the case hot. The rules then
    /// decide where the case goes, so no departure is noted: the selection
    /// stays while the case is on the board.
    public func unflag(_ id: Board.CaseID) {
        source.unflag(id)
    }

    // MARK: Internals

    /// Runs the user's write on case `id`; when `id` is selected and the
    /// write `changes` the case, notes where the selection goes should the
    /// case leave what is shown. A write that changes nothing drops a
    /// departure noted before: no report of the source belongs to it.
    private func write(_ id: Board.CaseID, changes: Bool, _ body: () -> Void) {
        if changes && state.selection == id {
            departure = (id, Board.selectionAfterDone(id, source.snapshot, state))
        } else {
            departure = nil
        }
        body()
    }

    /// Makes `next` the state, its selection resolved, rebuilds the view
    /// model and tells the page what changed. `user` = the user asked for
    /// another view state (a pending departure no longer applies).
    private func apply(_ next: Board.ViewState, user: Bool = true) {
        let snapshot = source.snapshot
        var next = next
        if case .account(let id) = next.account, !snapshot.accounts.contains(where: { $0.id == id }) {
            // The account went away: its filter with it.
            next.account = .all
        }
        // Only the first report after the user's write may use the
        // departure; a later, unrelated one never does.
        let departure = user ? nil : self.departure
        self.departure = nil
        var resolved = Board.resolveSelection(snapshot, next)
        if let d = departure, next.selection == d.id, resolved != d.id {
            // The selected case left after the user's write.
            next.selection = d.next
            resolved = Board.resolveSelection(snapshot, next)
        }
        next.selection = resolved
        if next.selection != state.selection {
            next.revealsWhy = false
        }
        let view = Board.view(snapshot, next, now: now(), calendar: calendar)

        var changes: Changes = []
        if next.style != state.style {
            changes.insert(.style)
        }
        if next.filter != state.filter || next.account != state.account {
            changes.insert(.filters)
        }
        if next.selection != state.selection || next.revealsWhy != state.revealsWhy
            || view.showsPanel != self.view.showsPanel
        {
            changes.insert(.selection)
        }
        if Self.contentDiffers(view, self.view) {
            changes.insert(.content)
        }
        state = next
        self.view = view
        deliver(changes)
        requestMessages()
    }

    /// Asks the source for the selected case's conversation when another
    /// case is selected or the selected one changed since it last asked.
    private func requestMessages() {
        guard let id = state.selection, let c = source.snapshot.cases.first(where: { $0.id == id }) else {
            requested = nil
            return
        }
        let phase = source.snapshot.phase
        if let r = requested, r.id == id, r.version == c.version {
            // The same case: asked again only when its load failed and the
            // board has since come back from a failure (a reconnect).
            guard c.messagesFailed, r.phase != phase, r.phase.isFailure, !phase.isFailure else {
                requested = (id, c.version, phase)
                return
            }
        }
        requested = (id, c.version, phase)
        source.loadMessages(of: id)
    }

    /// The conversation of case `id` could not be loaded.
    private func failedLoad(of id: Board.CaseID?) -> Bool {
        guard let id else { return false }
        return source.snapshot.cases.first { $0.id == id }?.messagesFailed == true
    }

    /// Tells `onChange` about `changes`. Called again from inside
    /// `onChange`, it only collects them: the outer delivery hands them over
    /// once the listener returns, as many times as new ones came, each time
    /// with `view` as it is then.
    private func deliver(_ changes: Changes) {
        pending.formUnion(changes)
        guard !notifying else { return }
        notifying = true
        defer { notifying = false }
        while !pending.isEmpty {
            let next = pending
            pending = []
            onChange?(next)
        }
    }

    /// Whether the views differ beyond the selection: another selected
    /// case's detail is a selection change, the same case's is content.
    private static func contentDiffers(_ a: Board.View, _ b: Board.View) -> Bool {
        var a = a
        var b = b
        a.selection = nil
        b.selection = nil
        a.showsPanel = false
        b.showsPanel = false
        if a.detail?.id != b.detail?.id {
            a.detail = nil
            b.detail = nil
        }
        return a != b
    }
}
