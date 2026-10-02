// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// The board's inline reply editor for one main window, the AppKit side:
/// makes the `ComposePane`s (`.inline`, owner `.board`) that Core's
/// `BoardReplyPanes` asks for, gives the live one's view to whichever detail
/// shows its case (the panel while `View.showsPanel`, else the List's pane)
/// and forwards the panes' ends to Core. Every decision about a pane — when
/// it is made, retired, saved, kept, closed or abandoned — is Core's
/// (`BoardReplyPanes`, tested there). Owned by the board page; the List's
/// detail and the panel's detail ask it what their reply slot shows
/// (`slot(for:in:)`) and hear of every change (`attach`).
///
/// The invented samples have no draft behind them: no Core, the static
/// block stays (only the development hook shows a blank pane).
@MainActor
final class BoardReplyEditorHost {
    /// What a detail's reply slot shows.
    enum Slot {
        /// Nothing of the host's: the Suggest control, or the samples' block.
        case none
        /// The draft is loading.
        case loading
        /// The draft could not be opened; `retry` offers Try Again.
        case failed(retry: Bool)
        /// The live pane: put its view into the slot; `unsaved`: what was
        /// typed could not be saved yet (`Board.Text.replyNotSaved`).
        case pane(ComposePane, unsaved: Bool)
        /// The live pane belongs to the other presentation: show nothing.
        case elsewhere
    }

    /// What making a pane needs of the application, set by the window
    /// (`BoardActions.inlineReply`).
    struct Environment {
        let state: AppState
        /// The compose manager's controller (the account list), once the
        /// application wired it.
        let composer: @MainActor () -> ComposeController?
    }

    private let actions: BoardActions
    private var controller: BoardController { actions.controller }
    /// The page's window (alerts, the first responder).
    var window: () -> NSWindow? = { nil }
    /// A short message over the page.
    var onToast: ((String) -> Void)?

    /// The rules; nil for the samples and until the window gave the
    /// environment.
    private var panes: BoardReplyPanes<ComposePane>?
    /// A compose controller of our own when the application has none.
    private var ownComposer: ComposeController?
    /// The development hook's blank pane and its case (samples only).
    private var development: (pane: ComposePane, caseID: Board.CaseID)?

    /// Reply asked for the editor of this case before its pane existed.
    private var focusPending: Board.CaseID?
    /// A pane that had the keyboard ended: the detail takes it next.
    private var keyboardAfterEnd = false
    /// The last editor height per pane, to scroll only when it grows.
    private var heights: [ObjectIdentifier: CGFloat] = [:]
    /// The page is out of the window (Mail mode, a closed window).
    private var suspended = false

    private var details: [WeakDetail] = []
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "board")

    /// Every host, for the quit (`finishAll`).
    private static var hosts: [WeakHost] = []

    init(actions: BoardActions) {
        self.actions = actions
        Self.hosts.removeAll { $0.host == nil }
        Self.hosts.append(WeakHost(host: self))
    }

    /// A detail whose reply slot follows the host (`replySlotChanged`).
    func attach(_ d: BoardDetailViewController) {
        details.removeAll { $0.detail == nil || $0.detail === d }
        details.append(WeakDetail(detail: d))
    }

    private func changed() {
        for d in details {
            d.detail?.replySlotChanged()
        }
        if keyboardAfterEnd {
            keyboardAfterEnd = false
            for d in details {
                d.detail?.takeKeyboardAfterReply()
            }
        }
    }

    // MARK: What the slot shows

    /// What the reply slot of the detail in `presentation` shows for case
    /// `id`.
    func slot(for id: Board.CaseID, in presentation: BoardDetailViewController.Presentation) -> Slot {
        if let development, development.caseID == id {
            return presentation == activePresentation ? .pane(development.pane, unsaved: false) : .elsewhere
        }
        guard let panes else { return .none }
        switch panes.slot(for: id) {
        case .none: return .none
        case .loading: return .loading
        case .failed(let retry): return .failed(retry: retry)
        case .pane(let p, let unsaved):
            return presentation == activePresentation ? .pane(p, unsaved: unsaved) : .elsewhere
        }
    }

    /// The presentation that shows the selected case: the panel while the
    /// page shows it, else the List's pane.
    private var activePresentation: BoardDetailViewController.Presentation {
        controller.view.showsPanel ? .panel : .pane
    }

    /// Try Again of the slot's failure note.
    func retry() {
        panes?.loader.retry()
    }

    /// Whether Reply edits the suggested reply of case `id` inline.
    func editsInline(_ id: Board.CaseID) -> Bool {
        guard prepared, let c = actions.boardCase(id) else { return false }
        return c.draft != nil
    }

    /// Reply with a suggested reply: the case is selected and its editor
    /// takes the keyboard, now or as soon as it is there.
    func focusReply(_ id: Board.CaseID) {
        if controller.state.selection != id {
            controller.select(id)
        }
        if let live = panes?.live, panes?.slot(for: id).isPane(live) == true {
            focus(live)
            return
        }
        focusPending = id
    }

    // MARK: Following the board

    /// The board changed (the page calls this before its details render):
    /// Core is told which case is selected.
    func update() {
        guard !suspended else { return }
        let selected = controller.view.detail.flatMap { actions.boardCase($0.id) }
        if focusPending != nil, focusPending != selected?.id {
            focusPending = nil
        }
        if let development, development.caseID != selected?.id {
            dropDevelopment()
        }
        guard prepare() else { return }
        panes?.show(selected)
    }

    /// The page left the window (Mail mode) or the window closes: the live
    /// pane is retired (saved) and nothing loads until `resume`.
    func suspend() {
        guard !suspended else { return }
        suspended = true
        focusPending = nil
        dropDevelopment()
        panes?.suspend()
    }

    /// The page is back: the selected case's reply loads again.
    func resume() {
        guard suspended else { return }
        suspended = false
        panes?.resume()
        update()
    }

    /// Core, made once the window gave the environment (never for the
    /// samples); false when there is none.
    @discardableResult
    private func prepare() -> Bool {
        if panes != nil {
            return true
        }
        guard !actions.samples, let env = actions.inlineReply else { return false }
        let p = BoardReplyPanes<ComposePane>(loader: BoardReplyEditorController(client: env.state.client))
        p.make = { [weak self] key, params in
            self?.makePane(params, key: key)
        }
        p.detach = { [weak self] pane in
            self?.release(pane)
        }
        p.onToast = { [weak self] text in
            self?.onToast?(text)
        }
        p.onChange = { [weak self] in
            self?.changed()
        }
        p.onAdopt = { [weak self] pane in
            self?.adopted(pane)
        }
        panes = p
        return true
    }

    private var prepared: Bool {
        !actions.samples && actions.inlineReply != nil
    }

    private func adopted(_ pane: ComposePane) {
        guard let id = focusPending, panes?.slot(for: id).isPane(pane) == true else { return }
        focusPending = nil
        // Once a detail has put the view into the window.
        DispatchQueue.main.async { [weak self, weak pane] in
            guard let self, let pane, self.panes?.live === pane else { return }
            self.focus(pane)
        }
    }

    private func makePane(_ params: ComposeParams, key: BoardReplyEditorController.Key) -> ComposePane? {
        guard let env = actions.inlineReply else { return nil }
        let composer = env.composer() ?? fallbackComposer(env)
        let pane = ComposePane(
            state: env.state, accounts: composer, params: params, editor: ComposeEditorView(sized: true),
            options: .init(layout: .inline, owner: .board))
        pane.host = self
        // Discard goes the board's way (after the draft controller's own
        // question): the draft it edits goes, with the case's link while
        // the case has it; a refusal keeps the pane and its text.
        let caseID = key.caseID
        pane.draft.discardStored = { [weak self, weak pane] account, draft in
            guard let self else { throw CancellationError() }
            if let pane {
                self.panes?.discarding(pane, true)
            }
            do {
                try await self.controller.discardDraft(caseID, draft: draft, account: account)
            } catch {
                if let pane {
                    self.panes?.discarding(pane, false)
                }
                throw error
            }
        }
        // Once the editor shows the draft, the draft controller learns how
        // the editor writes it (`editorReady`): only a real edit is saved,
        // never the editor's normalisation of the HTML it was given.
        let ready = pane.editor.onReady
        pane.editor.onReady = { [weak pane] in
            ready?()
            pane?.draft.editorReady()
        }
        pane.draft.onSendFailed = { [weak self, weak pane] in
            guard let self, let pane else { return }
            self.panes?.sendFailed(pane)
        }
        composer.register(pane)
        wireKeyViews(pane)
        return pane
    }

    private func fallbackComposer(_ env: Environment) -> ComposeController {
        if let ownComposer {
            return ownComposer
        }
        let c = ComposeController(client: env.state.client, settings: env.state.settings)
        ownComposer = c
        return c
    }

    /// Tab from To through Cc/Bcc and Subject into the editor, as in the
    /// compose window (the main window computes no key view loop of its
    /// own for views added later).
    private func wireKeyViews(_ pane: ComposePane) {
        guard !pane.isComment else { return }
        let h = pane.header
        let chain: [NSView] = [h.toField.editor, h.ccBccButton, h.ccField.editor, h.bccField.editor, h.subjectField]
        for (a, b) in zip(chain, chain.dropFirst()) {
            a.nextKeyView = b
        }
        h.subjectField.nextKeyView = Self.firstResponderCandidate(in: pane.editor.view) ?? pane.editor.view
    }

    private static func firstResponderCandidate(in v: NSView) -> NSView? {
        if v.acceptsFirstResponder {
            return v
        }
        for s in v.subviews {
            if let found = firstResponderCandidate(in: s) {
                return found
            }
        }
        return nil
    }

    /// Core is done with `pane`: it leaves the composer and the window.
    private func release(_ pane: ComposePane) {
        if let responder = pane.view.window?.firstResponder as? NSView, responder.isDescendant(of: pane.view) {
            keyboardAfterEnd = true
        }
        actions.inlineReply?.composer()?.remove(pane)
        ownComposer?.remove(pane)
        heights[ObjectIdentifier(pane)] = nil
        pane.view.removeFromSuperview()
        pane.host = nil
    }

    // MARK: Quitting

    /// The application quits: every host's panes settle, at most `wait`,
    /// while the connection still stands. False when a reply could not be
    /// saved or sent in that time (the caller asks before quitting).
    static func finishAll(wait: Duration) async -> Bool {
        var ok = true
        for h in hosts.compactMap(\.host) {
            h.suspended = true
            h.focusPending = nil
            h.dropDevelopment()
            if let panes = h.panes, await !panes.finishAll(wait: wait) {
                ok = false
            }
        }
        return ok
    }

    /// The user stayed after all (the quit question's Cancel): the pages
    /// show their replies again.
    static func resumeAll() {
        for h in hosts.compactMap(\.host) {
            h.suspended = false
            h.panes?.resume()
            h.update()
        }
    }

    // MARK: Focus and height

    private func focus(_ pane: ComposePane, tries: Int = 0) {
        guard panes?.live === pane || development?.pane === pane, let window = pane.view.window else { return }
        pane.focusEditor()
        let v = pane.editor.view
        v.scrollToVisible(v.bounds)
        if pane.editor.isReady {
            pane.editor.focusStart()
        } else if tries < 30 {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) { [weak self] in
                guard window.isVisible else { return }
                self?.focus(pane, tries: tries + 1)
            }
        }
    }

    /// The keyboard is in the live pane (a field's editor counts).
    func keyboardInLivePane() -> ComposePane? {
        guard let pane = panes?.live ?? development?.pane, let responder = window()?.firstResponder as? NSView,
              responder.isDescendant(of: pane.view) else { return nil }
        return pane
    }

    // MARK: Development aid

    /// DEVELOPMENT AID (MALACHI_START `reply-pane`, with the samples only):
    /// a blank board-owned pane, never saved, in the slot of case `id`,
    /// which is selected. Abandoned when another case is selected. nil
    /// over the daemon's board, where its autosave would write a real draft.
    func developmentShowBlank(for id: Board.CaseID) -> ComposePane? {
        guard actions.samples, let env = actions.inlineReply else { return nil }
        if controller.state.selection != id {
            controller.select(id)
        }
        dropDevelopment()
        let pane = ComposePane(
            state: env.state, accounts: fallbackComposer(env), params: ComposeParams(kind: .new),
            editor: ComposeEditorView(sized: true), options: .init(layout: .inline, owner: .board))
        pane.host = self
        wireKeyViews(pane)
        development = (pane, id)
        changed()
        return pane
    }

    private func dropDevelopment() {
        guard let development else { return }
        self.development = nil
        development.pane.abandon()
        release(development.pane)
        changed()
    }
}

// MARK: - ComposePaneHost

extension BoardReplyEditorHost: ComposePaneHost {
    var paneWindow: NSWindow? { window() }

    func paneTitleChanged(_ pane: ComposePane) {}

    func paneSendEnabledChanged(_ pane: ComposePane, _ enabled: Bool) {}

    /// A failed send, a refused file: over the page.
    func paneToast(_ text: String) {
        onToast?(text)
    }

    func paneDidEnd(_ pane: ComposePane, _ end: ComposePane.End) {
        if development?.pane === pane {
            dropDevelopment()
            return
        }
        let e: BoardReplyPanes<ComposePane>.End
        switch end {
        case .sent(let text): e = .sent(text)
        case .discarded: e = .discarded
        case .closed: e = .closed
        case .lost: e = .lost
        }
        panes?.ended(pane, e)
    }

    /// The editor grew while it has the keyboard: the detail scrolls so
    /// the pane's bottom edge (its Send row) stays in sight.
    func paneHeightChanged(_ pane: ComposePane) {
        let id = ObjectIdentifier(pane)
        let grew = pane.editorFrameHeight > heights[id] ?? 0
        heights[id] = pane.editorFrameHeight
        guard grew, panes?.live === pane || development?.pane === pane, let window = pane.view.window,
              let responder = window.firstResponder as? NSView, responder.isDescendant(of: pane.editor.view)
        else { return }
        DispatchQueue.main.async { [weak pane] in
            guard let pane, let window = pane.view.window else { return }
            // The whole column first: the scroll view's document grows with it.
            window.layoutIfNeeded()
            let b = pane.view.bounds
            let bottom = pane.view.isFlipped
                ? NSRect(x: b.minX, y: b.maxY - 1, width: 1, height: 1)
                : NSRect(x: b.minX, y: b.minY, width: 1, height: 1)
            pane.view.scrollToVisible(bottom)
        }
    }
}

// MARK: - BoardReplyPane

/// What Core's rules need of the inline pane.
extension ComposePane: BoardReplyPane {
    func settle() async -> Bool {
        await draft.settle()
    }

    func close() {
        cleanup()
    }

    func abandon() {
        draft.abandon()
        cleanup()
    }

    var hasUnsavedText: Bool { draft.draft.dirty || draft.draft.saving }

    var isSending: Bool { draft.draft.sending }

    var isLost: Bool { draft.lost }

    var replyTitle: String { titleText }
}

private extension BoardReplyPanes.Slot {
    /// The slot shows `pane`.
    func isPane(_ pane: Pane) -> Bool {
        if case .pane(let p, _) = self {
            return p === pane
        }
        return false
    }
}

@MainActor
private final class WeakDetail {
    weak var detail: BoardDetailViewController?

    init(detail: BoardDetailViewController) {
        self.detail = detail
    }
}

@MainActor
private final class WeakHost {
    weak var host: BoardReplyEditorHost?

    init(host: BoardReplyEditorHost) {
        self.host = host
    }
}
