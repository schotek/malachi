// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The rules behind the board's inline reply editor for one main window:
// when a pane is made, shown, kept, settled, closed or abandoned. The
// loader (`BoardReplyEditorController`) says which draft the selected case
// links; this type decides what happens to the panes that edit such
// drafts. The panes themselves (`ComposePane` in the app, fakes in the
// tests) are reached through `BoardReplyPane`; the window only makes them,
// puts their views into the detail and forwards their ends here.
//
// The theme of every rule: what the user typed, and the outcome of a Send,
// are never lost silently.
//
// Swift-first, like the board.

import Foundation
import os

/// What `BoardReplyPanes` needs of a pane.
@MainActor
public protocol BoardReplyPane: AnyObject {
    /// Saves everything typed (flushes the editor, saves while there are
    /// unsaved edits or a save is under way; waits for a send under way).
    /// True when nothing typed is unsaved. Never cleans up: the pane stays
    /// usable either way (`ComposeDraftController.settle`).
    func settle() async -> Bool
    /// Ends the pane for good once nothing is at stake (sent, discarded, or
    /// settled): the draft controller cleans up. Idempotent.
    func close()
    /// Forgets the pane without saving: its draft is gone. Idempotent.
    func abandon()
    /// Edits not yet saved, or a save under way.
    var hasUnsavedText: Bool { get }
    /// Send was pressed and has not answered yet.
    var isSending: Bool { get }
    /// The draft was deleted elsewhere (`draftNotFound`).
    var isLost: Bool { get }
    /// The reply's subject (or a comment's title), for a toast about a
    /// pane out of sight.
    var replyTitle: String { get }
}

/// The inline reply panes of one window over the loader.
///
/// The rules:
/// - **One live pane**, the selected case's, made when the loader has the
///   draft ready, or a parked pane of the same case and draft taken back.
/// - **Retired, never dropped**: when its case is no longer the one shown
///   (another selection, the case gone, Mail mode, a closed window, quit) or
///   the board shows its case linking another draft or none (not proof the
///   draft went), the live pane is parked and **settled** (saved). Only the
///   draft controller's `draftNotFound` (`End.lost`) abandons it.
/// - **Sending** panes are never settled, closed or released before the
///   send answered; the outcome is always delivered: success → the queued
///   toast and `ended(key)` wherever the user is; failure → the draft
///   controller's own toast and, out of sight, `Board.Text.replyNotSent`,
///   and the pane is kept so the user can come back to it.
/// - **Unsaved text is kept**: a parked pane whose settle failed stays,
///   whatever their number, and settles again with back-off (`Timing`);
///   its case shows it again with `Slot.pane(_, unsaved: true)`. Only
///   panes with nothing at stake (kept after a failed send, saved) are
///   bounded (`keptLimit`).
/// - **A pane taken back while it settles stays**: a settle that ends
///   while the user is typing in the pane again does not close it.
@MainActor
public final class BoardReplyPanes<Pane: BoardReplyPane> {
    public typealias Key = BoardReplyEditorController.Key

    /// What a detail's reply slot shows for a case.
    public enum Slot {
        /// Nothing of the panes': Suggest Reply, or the samples' block.
        case none
        /// The draft is loading.
        case loading
        /// The draft could not be opened; `retry` offers Try Again.
        case failed(retry: Bool)
        /// The case's pane; `unsaved`: what was typed could not be saved
        /// yet (the pane keeps trying).
        case pane(Pane, unsaved: Bool)
    }

    /// How a pane ended (`ComposePane.End` without the AppKit).
    public enum End: Equatable, Sendable {
        /// Queued; the text is the confirmation.
        case sent(String)
        case discarded
        case closed
        /// The draft was deleted elsewhere.
        case lost
    }

    /// The back-off of a kept pane's settles (tests shorten it).
    public struct Timing: Sendable {
        /// The wait before the first retry; doubled after every failure.
        public var retryFirst: Duration
        /// The longest wait between retries.
        public var retryMax: Duration

        public init(retryFirst: Duration = .seconds(5), retryMax: Duration = .seconds(120)) {
            self.retryFirst = retryFirst
            self.retryMax = retryMax
        }
    }

    /// At most this many panes with nothing at stake are kept (those kept
    /// after a failed send); the oldest is closed beyond that. Panes with
    /// unsaved text are never counted nor closed.
    public static var keptLimit: Int { 3 }

    @MainActor
    private final class Entry {
        let key: Key
        let pane: Pane
        /// The settle under way.
        var settling: Task<Bool, Never>?
        /// The next settle of a kept pane with unsaved text.
        var retry: Task<Void, Never>?
        /// Settles that failed in a row.
        var failures = 0
        /// Its last settle failed: it holds unsaved text.
        var unsaved = false
        /// Retired while sending: waits for the outcome.
        var awaitingSend = false
        /// Kept after a failed send: shown again when its case is.
        var keep = false
        /// Discard is under way (`discarding`): a dropped link is its doing.
        var ending = false

        init(key: Key, pane: Pane) {
            self.key = key
            self.pane = pane
        }

        /// Something is still to come before it can be judged.
        var pending: Bool { settling != nil || awaitingSend || ending }
    }

    public let loader: BoardReplyEditorController
    public let timing: Timing

    /// Makes the pane for a draft the loader has ready (nil: none made).
    public var make: (@MainActor (Key, ComposeParams) -> Pane?)?
    /// The pane is gone from here for good: take its view away.
    public var detach: (@MainActor (Pane) -> Void)?
    /// A short message over the page.
    public var onToast: (@MainActor (String) -> Void)?
    /// What a slot shows changed.
    public var onChange: (@MainActor () -> Void)?
    /// A pane became the live one (made, or taken back).
    public var onAdopt: (@MainActor (Pane) -> Void)?

    private var liveEntry: Entry?
    /// Retired panes, oldest first: settling, waiting for a send, kept.
    private var parked: [Entry] = []
    private var selected: Board.Case?
    private var suspended = false
    private var loaderToken: BoardObserverToken?
    /// The quit's wait (`finishAll`).
    private var quitWait: CheckedContinuation<Void, Never>?
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "board")

    public init(loader: BoardReplyEditorController, timing: Timing = Timing()) {
        self.loader = loader
        self.timing = timing
        loaderToken = loader.observe { [weak self] in
            self?.phaseChanged()
        }
    }

    // MARK: What is shown

    /// The live pane.
    public var live: Pane? { liveEntry?.pane }

    /// Every pane this type holds, live and parked.
    public var panes: [Pane] { (liveEntry.map { [$0] } ?? []).map(\.pane) + parked.map(\.pane) }

    /// What the reply slot of case `id` shows.
    public func slot(for id: Board.CaseID) -> Slot {
        if let e = liveEntry, e.key.caseID == id {
            return .pane(e.pane, unsaved: e.unsaved)
        }
        switch loader.phase {
        case .none:
            return .none
        case .loading(let k), .ready(let k, _):
            return k.caseID == id ? .loading : .none
        case .failed(let k, let f):
            return k.caseID == id ? .failed(retry: f == .backend) : .none
        }
    }

    /// The key a case's link gives.
    private static func key(of c: Board.Case) -> Key? {
        c.draft.map { Key(caseID: c.id, account: c.account, draft: $0.id) }
    }

    // MARK: Following the board

    /// The selected case (nil: none), after every change of the board: the
    /// live pane stays for its case and draft, and is retired otherwise; a
    /// parked pane of the case's draft is taken back; the loader is told.
    public func show(_ c: Board.Case?) {
        selected = c
        guard !suspended else { return }
        if let e = liveEntry {
            if let c, c.id == e.key.caseID {
                if !e.ending, !e.pane.isSending, c.draft?.id != e.key.draft {
                    // Another link, or none: saved first; only the daemon's
                    // draftNotFound makes it lost.
                    retire(e)
                }
            } else {
                retire(e)
            }
        }
        if liveEntry == nil, let c, let k = Self.key(of: c), let i = parked.firstIndex(where: { $0.key == k }) {
            adopt(parked.remove(at: i))
        }
        loader.show(c)
        phaseChanged()
    }

    /// The page left the window (Mail mode, a closed window): the live pane
    /// is retired and nothing loads until `resume`.
    public func suspend() {
        guard !suspended else { return }
        if let e = liveEntry {
            retire(e)
        }
        suspended = true
        loader.show(nil)
    }

    /// The page is back; `show` the selected case next.
    public func resume() {
        suspended = false
    }

    /// The loader's phase changed: a ready draft of the selected case gets
    /// its pane (a parked one, or a new one).
    private func phaseChanged() {
        defer { onChange?() }
        guard !suspended, liveEntry == nil, case .ready(let key, let params) = loader.phase,
              selected?.id == key.caseID else { return }
        if let i = parked.firstIndex(where: { $0.key == key }) {
            adopt(parked.remove(at: i))
            return
        }
        guard let pane = make?(key, params) else { return }
        adopt(Entry(key: key, pane: pane))
    }

    private func adopt(_ e: Entry) {
        // The user's again: retired later, it is settled and closed like
        // any other.
        e.keep = false
        liveEntry = e
        onAdopt?(e.pane)
    }

    // MARK: Retiring and settling

    /// The live pane's case is no longer the one shown: parked, and saved
    /// unless it is sending (then it waits for the send) or discarding.
    private func retire(_ e: Entry) {
        if liveEntry === e {
            liveEntry = nil
        }
        parked.append(e)
        if e.pane.isSending {
            e.awaitingSend = true
        } else if !e.ending {
            settle(e)
        }
        onChange?()
    }

    private func settle(_ e: Entry) {
        guard e.settling == nil else { return }
        e.retry?.cancel()
        e.retry = nil
        let pane = e.pane
        let task = Task { @MainActor in await pane.settle() }
        e.settling = task
        Task { @MainActor [weak self, weak e] in
            let ok = await task.value
            guard let self, let e else { return }
            self.settled(e, ok)
        }
    }

    private func tracked(_ e: Entry) -> Bool {
        liveEntry === e || parked.contains { $0 === e }
    }

    private func settled(_ e: Entry, _ ok: Bool) {
        e.settling = nil
        defer { checkQuit() }
        guard tracked(e) else { return }
        if ok {
            e.unsaved = false
            e.failures = 0
            if liveEntry === e {
                // Taken back while it settled: it stays the user's.
                onChange?()
                return
            }
            if e.keep {
                trimKept()
                onChange?()
                return
            }
            parked.removeAll { $0 === e }
            e.pane.close()
            detach?(e.pane)
            onChange?()
            return
        }
        // Lost: its end (`End.lost`) has come or is coming.
        guard !e.pane.isLost else { return }
        e.unsaved = true
        e.failures += 1
        log.info("board reply: a suggested reply could not be saved (\(e.failures, privacy: .public)×); kept")
        scheduleRetry(e)
        onChange?()
    }

    /// The next settle of a pane with unsaved text, later each time.
    private func scheduleRetry(_ e: Entry) {
        e.retry?.cancel()
        var delay = timing.retryFirst
        for _ in 1..<max(e.failures, 1) where delay < timing.retryMax {
            delay = delay * 2
        }
        delay = min(delay, timing.retryMax)
        e.retry = Task { @MainActor [weak self, weak e] in
            try? await Task.sleep(for: delay)
            guard !Task.isCancelled, let self, let e, self.tracked(e), e.unsaved else { return }
            e.retry = nil
            self.settle(e)
        }
    }

    /// Beyond `keptLimit` panes with nothing at stake the oldest is closed
    /// (settled once more first: should it hold text after all, it stays).
    private func trimKept() {
        let clean = parked.filter { $0.keep && !$0.unsaved && !$0.pending && !$0.pane.hasUnsavedText }
        guard clean.count > Self.keptLimit, let oldest = clean.first else { return }
        oldest.keep = false
        settle(oldest)
    }

    // MARK: What the panes report

    /// Discard is under way (`true`) or failed (`false`) for `pane`: while
    /// it runs, a dropped link is its doing and the pane is not saved.
    public func discarding(_ pane: Pane, _ on: Bool) {
        guard let e = entry(of: pane) else { return }
        e.ending = on
        if !on, liveEntry !== e {
            // Retired while it ran: what was typed is saved after all.
            settle(e)
        }
        checkQuit()
    }

    /// The send of `pane` failed (the draft controller said why in its own
    /// toast and gave Send back). Out of sight, the user hears which reply,
    /// and the pane is kept for when they come back.
    public func sendFailed(_ pane: Pane) {
        guard let e = entry(of: pane) else { return }
        e.awaitingSend = false
        if liveEntry !== e {
            onToast?(Board.Text.replyNotSent(pane.replyTitle))
            e.keep = true
            settle(e)
        }
        onChange?()
        checkQuit()
    }

    /// `pane` ended (`ComposePane.End`).
    public func ended(_ pane: Pane, _ end: End) {
        guard let e = entry(of: pane) else { return }
        if liveEntry === e {
            liveEntry = nil
        }
        parked.removeAll { $0 === e }
        e.retry?.cancel()
        e.retry = nil
        var reload = false
        switch end {
        case .sent(let text):
            // "Message queued for sending", or the comment's.
            onToast?(text)
            loader.ended(e.key)
        case .lost:
            onToast?(Board.Text.replyRemoved)
            loader.ended(e.key)
        case .discarded, .closed:
            // The board dropped the link already; should the discard fail,
            // the link comes back and the draft loads again.
            reload = loader.phase.key == e.key
        }
        e.pane.close()
        detach?(e.pane)
        if reload, !suspended {
            loader.show(nil)
            loader.show(selected)
        }
        onChange?()
        checkQuit()
    }

    private func entry(of pane: Pane) -> Entry? {
        if let e = liveEntry, e.pane === pane {
            return e
        }
        return parked.first { $0.pane === pane }
    }

    // MARK: Quitting

    /// The application quits: the live pane is retired, every pane with
    /// something at stake settles again (or its send or discard answers),
    /// at most `wait`. True when nothing typed is left unsaved and no send
    /// is unanswered; false: the caller asks before quitting (and calls
    /// `resume` and `show` when the user stays).
    public func finishAll(wait: Duration) async -> Bool {
        if let e = liveEntry {
            retire(e)
        }
        suspended = true
        loader.show(nil)
        for e in parked where e.unsaved && e.settling == nil {
            settle(e)
        }
        if parked.contains(where: \.pending) {
            await withCheckedContinuation { (c: CheckedContinuation<Void, Never>) in
                quitWait = c
                Task { @MainActor [weak self] in
                    try? await Task.sleep(for: wait)
                    self?.openQuit()
                }
                checkQuit()
            }
        }
        return !parked.contains { $0.unsaved || $0.pending }
    }

    private func checkQuit() {
        guard quitWait != nil, !parked.contains(where: \.pending) else { return }
        openQuit()
    }

    private func openQuit() {
        quitWait?.resume()
        quitWait = nil
    }
}
