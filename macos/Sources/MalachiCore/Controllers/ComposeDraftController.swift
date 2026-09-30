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
    /// The issue of a comment draft as draft.create returned it
    /// (`Draft.comment`), sent back with the form's visibility; nil for an
    /// e-mail.
    public var comment: DraftComment?
    /// The Drafts message the first save takes over (draft.open); cleared
    /// once a save went through.
    public var replaces: MessageID?
    /// The draft is the user's to keep: saved with ⌘S, the menu or the
    /// close dialog, or opened from the Drafts folder. Until then Discard
    /// in the close dialog deletes what the autosave stored, which would
    /// otherwise live on in the Drafts folder.
    public var explicitSave = false

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
    /// The window writes a comment on an issue (`ComposeParams.comment`,
    /// `Jira.commentCompose`): no recipients, subject or attachments, and
    /// no Save Draft, since no Drafts folder keeps a comment.
    var isComment: Bool { get }
    /// The comment's visibility as chosen in the window
    /// (`Jira.selectedVisibility` at first); public for an e-mail.
    var commentVisibility: CommentVisibility { get }
}

extension ComposeForm {
    /// An e-mail window.
    public var isComment: Bool { false }
    public var commentVisibility: CommentVisibility { .public }
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
    /// `Params.InReplyTo` / `Params.Forwarding`), and for a comment the
    /// issue it goes to (`ComposeParams.comment`).
    public func setOriginal(inReplyTo: MessageID?, forwarding: MessageID?, comment: DraftComment? = nil) {
        draft.inReplyTo = inReplyTo
        draft.forwarding = forwarding
        draft.comment = comment
    }

    /// The window writes a comment (`ComposeForm.isComment`).
    public var isComment: Bool { form?.isComment ?? false }

    /// The saved draft the window edits (compose.go `newWindow`:
    /// `Params.DraftID`, `Version`, `Replaces`): its id and version make
    /// the saves updates, and a draft opened from the Drafts folder is
    /// never deleted by closing the window.
    public func setOpened(draftID: DraftID?, version: Int, replaces: MessageID?, fromDrafts: Bool) {
        draft.draftID = draftID
        draft.version = version
        draft.replaces = replaces
        draft.explicitSave = fromDrafts
    }

    /// closeRequest's first branch: the window may go without a question.
    /// A comment, which no Drafts folder keeps, goes unasked only while
    /// there is nothing in it (`Jira.sendProblem`), a save under way or
    /// not (its copy goes too, `cleanup`).
    public var canCloseWithoutAsking: Bool {
        if draft.discard {
            return true
        }
        if isComment {
            return !hasCommentText
        }
        return !draft.dirty && !draft.saving
    }

    /// The comment holds more than white space and invisible characters
    /// (the editor's last reported text).
    private var hasCommentText: Bool {
        Jira.sendProblem(form?.editorText() ?? "").isEmpty
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
        } else if form.isComment {
            // A comment is saved only against a crash, not as a draft the
            // user keeps: nothing to say about it.
            form.setStatus("")
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
            forwarding: draft.forwarding,
            replaces: draft.replaces
        )
        if composeRichText {
            d.htmlBody = form.editorHTML()
        }
        // Of a comment draft.save reads only the visibility; the issue
        // goes back as it came.
        if form.isComment, let c = draft.comment {
            d.comment = DraftComment(issue: c.issue, visibility: form.commentVisibility)
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
            let log = self.log
            Task { [weak self] in
                let outcome: Result<DraftSaveResult, any Error>
                do {
                    outcome = .success(try await client.call(API.DraftSave.self, DraftSaveParams(draft: wire)))
                } catch {
                    outcome = .failure(error)
                }
                guard let self, !self.draft.closed else {
                    // The window went while a comment was being saved: no
                    // Drafts folder keeps it, so the copy goes too.
                    if wire.comment != nil, case .success(let res) = outcome {
                        Self.forget(client: client, log: log, accountID: wire.accountId, draftID: res.draftId)
                    }
                    return
                }
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
            draft.replaces = nil
            draft.explicitSave = draft.explicitSave || reason == .explicit
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

    /// saveFailed: a conflict, or a draft deleted meanwhile, starts over
    /// with a fresh draft (local wins); a Drafts message to take over that
    /// is gone is dropped from the next save; an autosave does not nag with
    /// the same failure every 30 s.
    public func saveFailed(_ reason: SaveReason, _ error: any Error) {
        if let e = error as? RPCError {
            switch e.code {
            case .conflict:
                // Local wins: the next save creates a fresh draft with our text.
                draft.draftID = nil
                draft.version = 0
                draft.replaces = nil
                form?.toast(L10n.T("This draft was changed elsewhere; your text will be saved as a new draft"))
                return
            case .draftNotFound:
                // Deleted meanwhile (its copy went to the Trash): the text
                // survives as a new draft, its attachments with it.
                draft.draftID = nil
                draft.version = 0
                draft.replaces = nil
                form?.toast(L10n.T("This draft was removed elsewhere; your text will be saved as a new draft"))
                return
            case .messageNotFound where draft.replaces != nil:
                // The Drafts message it was to take over is gone: save
                // without it.
                draft.replaces = nil
                markDirty()
                return
            default:
                break
            }
        }
        if isComment {
            // A comment is saved to be sent (`send`), and otherwise only
            // against a crash: a failed autosave says nothing.
            if reason == .autosave {
                log.debug("comment autosave: \(String(describing: error), privacy: .public)")
                if autosave == nil {
                    markDirty()
                }
                return
            }
            form?.toast(rpcErrorText(L10n.T("Sending"), error))
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

    /// send validates, saves if needed and queues the message. A comment
    /// has no recipients; it needs text (`Jira.sendProblem`, checked on
    /// the editor's current content).
    public func send() {
        guard !draft.sending, let form else { return }
        if !form.isComment {
            let (to, cc, bcc, ok) = form.recipients()
            if !ok {
                form.toast(L10n.T("Fix the highlighted recipients"))
                return
            }
            if to.count + cc.count + bcc.count == 0 {
                form.toast(L10n.T("Add at least one recipient"))
                return
            }
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
        guard form.isComment else {
            queue(fail: fail)
            return
        }
        form.flushEditor { [weak self] in
            guard let self, !self.draft.closed, let form = self.form else { return }
            let problem = Jira.sendProblem(form.editorText())
            if !problem.isEmpty {
                form.toast(problem)
                fail()
                return
            }
            self.queue(fail: fail)
        }
    }

    /// send's second half: the explicit save, then message.send; `fail`
    /// gives Send back.
    private func queue(fail: @escaping @MainActor () -> Void) {
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
                    let comment = self.isComment
                    self.draft.discard = true
                    self.onSent?(comment ? Jira.commentQueued() : L10n.T("Message queued for sending"))
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

    /// deleteDraft deletes the stored draft (and with it its copy in the
    /// Drafts folder); the window is closing, so a failure is only logged.
    private func deleteDraft() {
        guard let form, let id = draft.draftID else { return }
        Self.forget(client: client, log: log, accountID: form.account.id, draftID: id)
    }

    /// draft.delete in the background, a failure only logged.
    private static func forget(client: RPCClient, log: Logger, accountID: AccountID, draftID: DraftID) {
        Task {
            do {
                _ = try await client.call(API.DraftDelete.self, DraftDeleteParams(accountId: accountID, draftId: draftID))
            } catch {
                log.debug("draft.delete: \(String(describing: error), privacy: .public)")
            }
        }
    }

    private func discardNow() {
        guard let form else { return }
        let accountID = form.account.id
        let client = client
        let log = log
        if draft.draftID != nil {
            deleteDraft()
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
    /// (`cleanup` has run); false when it stays. A comment has no Save
    /// Draft: the question is whether to discard it.
    public func closeRequest() async -> Bool {
        if canCloseWithoutAsking {
            cleanup()
            return true
        }
        if isComment {
            let ok = await confirmDiscard(L10n.T("Discard this message?"), "", L10n.T("_Discard"))
            guard ok, !draft.closed else { return false }
            cleanup()
            return true
        }
        switch await saveDraftQuestion() {
        case .discard:
            draft.discard = true
            if !draft.explicitSave, draft.draftID != nil {
                deleteDraft()
            }
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
    /// A comment's saved copy goes with the window unless it was sent: no
    /// Drafts folder keeps it (the autosave is only for a crash).
    public func cleanup() {
        guard !draft.closed else { return }
        if isComment, !draft.discard {
            deleteDraft()
        }
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
