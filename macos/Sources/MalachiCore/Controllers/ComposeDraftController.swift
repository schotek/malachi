// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

// The lifecycle of the draft behind a compose window: ui/internal/compose/
// draft.go without the widgets. The window (AppKit) is reached through
// `ComposeForm`; the daemon through the client. Every RPC runs as a `Task`
// on the main actor, so the continuation after the call is on the main
// actor too (the GTK window's `glib.IdleAdd` discipline), and a reply that
// arrives after the window closed is dropped (`closed`).

/// compose.richText: whether the editor's HTML is transmitted with the
/// draft. The backend sanitises it in compose mode and derives the text
/// alternative, so the formatting toolbar and inline images are on.
public let composeRichText = true

/// compose.saveReason: what asked for the save.
public enum SaveReason: Sendable, Equatable {
    /// ⌘S, the menu, the close dialog, sending.
    case explicit
    case autosave
}

/// What the user answered to "Save changes to this draft?" (the AppKit
/// side maps its `SaveDraftAnswer` onto this).
public enum DraftCloseAnswer: Sendable, Equatable {
    case save, discard, cancel
}

/// compose.draftState: the lifecycle of the draft behind a window.
public struct DraftState: Sendable, Equatable {
    /// nil until the first successful save (Go's "").
    public var draftID: DraftID?
    public var version = 0

    public var inReplyTo: MessageID?
    public var forwarding: MessageID?

    /// Edits not yet persisted.
    public var dirty = false
    /// draft.save in flight.
    public var saving = false
    public var sending = false

    public var lastSaved: Date?
    /// The last autosave error shown as a toast.
    public var lastError = ""

    /// The window is gone; late callbacks are dropped.
    public var closed = false
    /// Close without asking.
    public var discard = false

    public init() {}
}

/// What the draft controller reads from and writes to the compose window
/// (the rows, the editor, the chips, the status line, the toasts). Every
/// string it hands over is plain text.
@MainActor
public protocol ComposeForm: AnyObject {
    /// The selected From identity (compose.go `account`): the placeholder
    /// account while the backend lists none, never nil.
    var account: Account { get }
    /// The three recipient rows parsed (compose.go `recipients`); `ok` is
    /// false when any token is invalid.
    func recipients() -> (to: [Address], cc: [Address], bcc: [Address], ok: Bool)
    var subject: String { get }
    /// The attachments the window lists, in order.
    var attachments: [DraftAttachment] { get }
    /// editor.HTML / editor.Text: the editor's last reported content.
    func editorHTML() -> String
    func editorText() -> String
    /// editor.Flush: `done` runs once the editor reported its current
    /// content.
    func flushEditor(_ done: @escaping @MainActor () -> Void)
    /// compose.go `setAttachments`: replaces the list and the chips with
    /// what the backend kept.
    func setAttachments(_ attachments: [DraftAttachment])
    func setStatus(_ text: String)
    func toast(_ text: String)
    /// The Send button and menu item (`compose.send` enabled state).
    func setSendEnabled(_ enabled: Bool)
    /// Closes the window without asking (the controller decided).
    func closeWindow()
}

/// compose/draft.go: autosave, `build`, `save`, `saveFailed`, `send`,
/// `discard`, `closeRequest` and `cleanup` over a `ComposeForm`.
@MainActor
public final class ComposeDraftController {
    /// compose.autosaveDelay: how long after the last edit a dirty draft is
    /// saved.
    public static let autosaveDelay: Duration = .seconds(30)

    public private(set) var draft = DraftState()

    /// The window. Weak: the window owns the controller.
    public weak var form: (any ComposeForm)?

    /// Manager.OnSent: called with a short message when the window queued
    /// a message (a toast on the main window).
    public var onSent: (@MainActor (String) -> Void)?

    /// widget.ConfirmDestructive for Discard: heading, body, label →
    /// confirmed. The default confirms, for a window without dialogs.
    public var confirmDiscard: @MainActor (_ heading: String, _ body: String, _ label: String) async -> Bool = { _, _, _ in true }

    /// The "Save changes to this draft?" dialog. The default cancels, so a
    /// window without dialogs stays open.
    public var saveDraftQuestion: @MainActor () async -> DraftCloseAnswer = { .cancel }

    /// The clock behind `lastSaved` (tests pin it).
    public var now: @MainActor () -> Date = { Date() }

    private let client: RPCClient
    private let settings: Settings
    private let placeholder: @MainActor () -> Bool
    private let registry: CIDRegistry
    private let delay: Duration
    private var autosave: Task<Void, Never>?
    /// Runs when the in-flight save finishes (`pendingAfterSave`).
    private var pendingAfterSave: [@MainActor ((any Error)?) -> Void] = []
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "compose")

    /// - Parameters:
    ///   - client: the transport.
    ///   - settings: `confirmDelete` decides whether Discard asks.
    ///   - placeholder: Manager.Placeholder: whether the From row lists the
    ///     placeholder identity (the status line says so).
    ///   - registry: the cid: registry the inline pictures live in.
    ///   - autosaveDelay: `Self.autosaveDelay`; tests pass milliseconds.
    public init(
        client: RPCClient, settings: Settings, placeholder: @escaping @MainActor () -> Bool,
        registry: CIDRegistry = .shared, autosaveDelay: Duration = ComposeDraftController.autosaveDelay
    ) {
        self.client = client
        self.settings = settings
        self.placeholder = placeholder
        self.registry = registry
        delay = autosaveDelay
    }

    /// Whether the autosave timer is armed (Go `autosave != 0`).
    public var autosaveArmed: Bool { autosave != nil }

    /// The original a reply or forward refers to (compose.go `newWindow`:
    /// `Params.InReplyTo` / `Params.Forwarding`).
    public func setOriginal(inReplyTo: MessageID?, forwarding: MessageID?) {
        draft.inReplyTo = inReplyTo
        draft.forwarding = forwarding
    }

    /// closeRequest's first branch: the window may go without a question.
    public var canCloseWithoutAsking: Bool {
        draft.discard || (!draft.dirty && !draft.saving)
    }

    // MARK: Dirty state and status

    /// markDirty records an edit and arms the autosave timer.
    public func markDirty() {
        draft.dirty = true
        refreshStatus()
        if autosave == nil {
            autosave = Task { [weak self] in
                do {
                    try await Task.sleep(for: self?.delay ?? ComposeDraftController.autosaveDelay)
                } catch {
                    return // cancelled
                }
                guard let self, !Task.isCancelled else { return }
                self.autosave = nil
                if self.draft.dirty, !self.draft.saving {
                    self.save(reason: .autosave)
                }
            }
        }
    }

    private func cancelAutosave() {
        autosave?.cancel()
        autosave = nil
    }

    /// The status line under the window.
    public func refreshStatus() {
        guard let form else { return }
        let d = draft
        if d.sending {
            form.setStatus(L10n.T("Sending…"))
        } else if d.saving {
            form.setStatus(L10n.T("Saving draft…"))
        } else if d.dirty {
            form.setStatus(L10n.T("Unsaved changes"))
        } else if let saved = d.lastSaved {
            form.setStatus(L10n.T("Draft saved %s", formatTime(saved)))
        } else if placeholder() {
            form.setStatus(L10n.T("Using placeholder account"))
        } else {
            form.setStatus("")
        }
    }

    // MARK: Building and saving

    /// build assembles the wire draft from the rows and the editor's last
    /// content. Call after `flushEditor`. nil without a form.
    public func build() -> Draft? {
        guard let form else { return nil }
        let (to, cc, bcc, _) = form.recipients()
        var d = Draft(
            id: draft.draftID,
            accountId: form.account.id,
            version: draft.version,
            to: to,
            cc: cc.isEmpty ? nil : cc,
            bcc: bcc.isEmpty ? nil : bcc,
            subject: form.subject,
            textBody: form.editorText(),
            inReplyTo: draft.inReplyTo,
            forwarding: draft.forwarding
        )
        if composeRichText {
            d.htmlBody = form.editorHTML()
        }
        // In draft.save params only the id of an attachment is read.
        let atts = form.attachments.map { a in
            DraftAttachment(id: a.id, filename: "", contentType: "", size: 0, inline: false)
        }
        d.attachments = atts.isEmpty ? nil : atts
        return d
    }

    /// save persists the draft; `done` (optional) runs with the outcome. A
    /// save already in flight queues `done` behind it.
    public func save(reason: SaveReason, done: (@MainActor ((any Error)?) -> Void)? = nil) {
        if let done {
            pendingAfterSave.append(done)
        }
        if draft.saving {
            return
        }
        cancelAutosave()
        draft.saving = true
        draft.dirty = false // edits during the call set it again
        refreshStatus()
        guard let form else { return }
        form.flushEditor { [weak self] in
            guard let self, !self.draft.closed, let wire = self.build() else { return }
            let client = self.client
            Task { [weak self] in
                let outcome: Result<DraftSaveResult, any Error>
                do {
                    outcome = .success(try await client.call(API.DraftSave.self, DraftSaveParams(draft: wire)))
                } catch {
                    outcome = .failure(error)
                }
                guard let self, !self.draft.closed else { return }
                self.saved(reason, outcome)
            }
        }
    }

    private func saved(_ reason: SaveReason, _ outcome: Result<DraftSaveResult, any Error>) {
        draft.saving = false
        var failure: (any Error)?
        switch outcome {
        case .failure(let err):
            failure = err
            draft.dirty = true
            saveFailed(reason, err)
        case .success(let res):
            draft.draftID = res.draftId
            draft.version = res.version
            draft.lastSaved = now()
            draft.lastError = ""
            if let form {
                let kept = res.attachments ?? []
                if kept.count != form.attachments.count {
                    form.setAttachments(kept)
                }
                let msg = blockedSummary(res.blocked)
                if !msg.isEmpty {
                    form.toast(msg)
                }
            }
        }
        refreshStatus()
        if draft.dirty, autosave == nil, failure == nil {
            markDirty() // edits arrived during the save
        }
        let pending = pendingAfterSave
        pendingAfterSave = []
        for f in pending {
            f(failure)
        }
    }

    /// saveFailed: a conflict starts over with a fresh draft (local wins);
    /// an autosave does not nag with the same failure every 30 s.
    public func saveFailed(_ reason: SaveReason, _ error: any Error) {
        if let e = error as? RPCError, e.code == .conflict {
            // Local wins: the next save creates a fresh draft with our text.
            draft.draftID = nil
            draft.version = 0
            form?.toast(L10n.T("This draft was changed elsewhere; your text will be saved as a new draft"))
            return
        }
        let text = rpcErrorText(L10n.T("Saving the draft"), error)
        if reason == .autosave {
            // Do not nag every 30 s with the same failure (e.g. no backend).
            if text == draft.lastError {
                log.debug("autosave failed again: \(String(describing: error), privacy: .public)")
                return
            }
            draft.lastError = text
        }
        form?.toast(text)
        // Retry later.
        if autosave == nil {
            markDirty()
        }
    }

    // MARK: Sending

    /// send validates, saves if needed and queues the message.
    public func send() {
        guard !draft.sending, let form else { return }
        let (to, cc, bcc, ok) = form.recipients()
        if !ok {
            form.toast(L10n.T("Fix the highlighted recipients"))
            return
        }
        if to.count + cc.count + bcc.count == 0 {
            form.toast(L10n.T("Add at least one recipient"))
            return
        }
        draft.sending = true
        form.setSendEnabled(false)
        refreshStatus()
        let fail: @MainActor () -> Void = { [weak self] in
            guard let self else { return }
            self.draft.sending = false
            self.form?.setSendEnabled(true)
            self.refreshStatus()
        }
        save(reason: .explicit) { [weak self] err in
            guard let self else { return }
            if err != nil {
                fail()
                return
            }
            guard let form = self.form, let draftID = self.draft.draftID else {
                fail()
                return
            }
            let params = MessageSendParams(accountId: form.account.id, draftId: draftID, version: self.draft.version)
            let client = self.client
            Task { [weak self] in
                let outcome: Result<MessageSendResult, any Error>
                do {
                    outcome = .success(try await client.call(API.MessageSend.self, params))
                } catch {
                    outcome = .failure(error)
                }
                guard let self, !self.draft.closed else { return }
                switch outcome {
                case .failure(let err):
                    if let e = err as? RPCError, e.code == .conflict {
                        self.draft.draftID = nil
                        self.draft.version = 0
                        self.draft.dirty = true
                    }
                    self.form?.toast(rpcErrorText(L10n.T("Sending"), err))
                    fail()
                case .success:
                    self.draft.discard = true
                    self.onSent?(L10n.T("Message queued for sending"))
                    self.form?.closeWindow()
                }
            }
        }
    }

    // MARK: Discarding and closing

    /// discard drops the draft (after confirmation when the setting is on).
    /// A draft never saved has no id, but may hold attachments the backend
    /// imported for it (the template's pictures and files); those are
    /// released rather than left for the sweep.
    public func discard() {
        guard form != nil else { return }
        if !settings.confirmDelete || (!draft.dirty && draft.draftID == nil) {
            discardNow()
            return
        }
        Task { [weak self] in
            guard let self else { return }
            let ok = await self.confirmDiscard(L10n.T("Discard this message?"), "", L10n.T("_Discard"))
            guard ok, !self.draft.closed else { return }
            self.discardNow()
        }
    }

    private func discardNow() {
        guard let form else { return }
        let accountID = form.account.id
        let client = client
        let log = log
        if let id = draft.draftID {
            Task {
                do {
                    _ = try await client.call(API.DraftDelete.self, DraftDeleteParams(accountId: accountID, draftId: id))
                } catch {
                    log.debug("draft.delete: \(String(describing: error), privacy: .public)")
                }
            }
        } else {
            for a in form.attachments {
                let attID = a.id
                Task {
                    do {
                        _ = try await client.call(
                            API.AttachmentRemove.self, AttachmentRemoveParams(accountId: accountID, attachmentId: attID))
                    } catch {
                        log.debug("attachment.remove: \(String(describing: error), privacy: .public)")
                    }
                }
            }
        }
        draft.discard = true
        form.closeWindow()
    }

    /// closeRequest keeps the window open while there are unsaved edits
    /// and asks what to do with them. True when the window may close now
    /// (`cleanup` has run); false when it stays.
    public func closeRequest() async -> Bool {
        if canCloseWithoutAsking {
            cleanup()
            return true
        }
        switch await saveDraftQuestion() {
        case .discard:
            draft.discard = true
            cleanup()
            return true
        case .save:
            let ok = await withCheckedContinuation { (cont: CheckedContinuation<Bool, Never>) in
                save(reason: .explicit) { err in
                    cont.resume(returning: err == nil)
                }
            }
            // On failure the toast is shown and the window stays.
            guard ok, !draft.closed else { return false }
            draft.discard = true
            cleanup()
            return true
        case .cancel:
            return false
        }
    }

    /// cleanup runs when the window really closes: late replies are
    /// dropped, the autosave is disarmed, the inline pictures forgotten.
    /// Idempotent. A save whose outcome is still awaited (the close
    /// question's) is answered with a cancellation so nobody waits forever.
    public func cleanup() {
        guard !draft.closed else { return }
        draft.closed = true
        cancelAutosave()
        for a in form?.attachments ?? [] where a.inline {
            if let cid = a.contentId {
                registry.unregister(cid)
            }
        }
        let pending = pendingAfterSave
        pendingAfterSave = []
        for f in pending {
            f(CancellationError())
        }
    }
}
