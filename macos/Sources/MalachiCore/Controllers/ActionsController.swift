// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The per-message actions of the GTK main window (ui/internal/window/
/// actions.go, and the RPC halves of outbox.go `retryOutbox` /
/// `cancelSendFrom`, remote.go `loadRemoteImages` / `trustSender` and
/// compose_open.go `openCompose`), minus the widgets: an optimistic change
/// in the model and the rows, message.flag / message.move / message.delete
/// in the background, and the change put back on failure. Every action
/// takes a list of messages: one from a message window or a plain row, all
/// the folder members from a conversation row of the grouped list.
///
/// What needs AppKit is a hook the application installs: the confirmation
/// sheet (`confirm`), the compose window (`openCompose`), the message
/// windows that follow a removed message (`onWindowsClose`) and the views
/// that show a star or an outbox banner (`onStarChanged`,
/// `onOutboxStateChanged`). The rows and the folder badges are the list
/// and mailbox controllers' own callbacks.
///
/// Every RPC runs as a `Task` on the main actor through the mailbox's
/// `perform`, so the continuation is on the main actor too (the GTK
/// window's `glib.IdleAdd` discipline). Log lines carry method names and
/// errors only, never subjects or addresses.
@MainActor
public final class ActionsController {
    /// Asks the user before a destructive action (widget/rpc.go
    /// `ConfirmDestructive`): `parent` is the window the sheet goes on,
    /// opaque here (the AppKit half passes an NSWindow; nil is the main
    /// window), `body` is hostile input shown as plain text and
    /// `confirmLabel` carries its GTK mnemonic. True when confirmed.
    public typealias Confirm = @MainActor (
        _ parent: AnyObject?, _ heading: String, _ body: String, _ confirmLabel: String
    ) async -> Bool

    public let mailbox: MailboxController
    public let list: ListController
    public let cache: MessageCache
    public let settings: Settings

    // MARK: Hooks (the AppKit half)

    /// The confirmation sheet. Without one a destructive action that must
    /// ask is refused (and logged), never run unasked.
    public var confirm: Confirm?
    /// Opens a compose window with the prepared parameters
    /// (compose/manager.go `Open`).
    public var openCompose: (@MainActor (ComposeParams) -> Void)?
    /// Raises the compose window already editing the draft, if any
    /// (compose/manager.go `FindDraft`); true when there was one.
    public var raiseDraft: (@MainActor (Draft) -> Bool)?
    /// Opens a message in its own window (message_view.go
    /// `openMessageWindow`): a draft, when the daemon cannot open drafts.
    public var openMessageWindow: (@MainActor (MessageSummary) -> Void)?
    /// A message left its folder: close its window (message_view.go
    /// `closeMessageWindow`).
    public var onWindowsClose: (@MainActor (MessageID) -> Void)?
    /// The flagged state of a message changed: every star showing it
    /// follows (actions.go `refreshStars`).
    public var onStarChanged: (@MainActor (MessageID, Bool) -> Void)?
    /// The cached delivery state of an outbox message changed: every banner
    /// showing it follows (outbox.go `showOutboxState`).
    public var onOutboxStateChanged: (@MainActor (MessageID) -> Void)?

    private let toast: @MainActor (String) -> Void
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "actions")
    /// The messages a draft.create or draft.open is being prepared for
    /// (compose_open.go `composing`): a second click while it runs does
    /// nothing.
    private var composing: Set<MessageID> = []

    /// - Parameters:
    ///   - mailbox: the folder half (badges, the RPC plumbing).
    ///   - list: the list half (rows, the selection).
    ///   - cache: the loaded messages (outbox state, remote images, the
    ///     compose source).
    ///   - settings: `confirmDelete`.
    ///   - toast: shows a transient message (the window's toast overlay).
    public init(
        mailbox: MailboxController, list: ListController, cache: MessageCache, settings: Settings,
        toast: @escaping @MainActor (String) -> Void
    ) {
        self.mailbox = mailbox
        self.list = list
        self.cache = cache
        self.settings = settings
        self.toast = toast
    }

    // MARK: Lookup

    /// What the window knows of message `id` (message_view.go `summary`):
    /// the list's summary, or the cached full message's for a message
    /// window outliving the folder it was opened from.
    public func summary(_ id: MessageID) -> MessageSummary? {
        mailbox.model.message(id)?.summary ?? cache.summary(id)
    }

    /// The known ones of the given messages, in order (actions.go
    /// `summaries`).
    private func summaries(_ ids: [MessageID]) -> [MessageSummary] {
        ids.compactMap { summary($0) }
    }

    /// What the per-message header buttons and actions allow for the
    /// selected row (actions.go `setMessageActionsSensitive`).
    public func actionFlags(for row: ListRow?) -> ActionFlags {
        messageActionState(row, model: mailbox.model)
    }

    /// `actionFlags` for a single message (a message window's `msg.*`
    /// group).
    public func flags(for s: MessageSummary) -> ActionFlags {
        messageActionState(ListRow(key: ListKey(message: s.id), message: s), model: mailbox.model)
    }

    // MARK: Seen

    /// Sets the seen flag on message `id` (actions.go `markRead`; the
    /// mark-as-read timer's target). A message read already is left alone.
    public func markRead(_ id: MessageID) {
        setSeen([id], true)
    }

    /// Clears the seen flag on message `id` (actions.go `markUnread`).
    public func markUnread(_ id: MessageID) {
        setSeen([id], false)
    }

    /// Changes the seen flag of the messages that do not have it so yet,
    /// optimistically (rows, unread badge of the folder, menu actions), and
    /// sends one message.flag; a failure puts everything back (actions.go
    /// `setSeenIDs`). The messages are of one folder (a conversation row's
    /// members are).
    public func setSeen(_ ids: [MessageID], _ seen: Bool) {
        var todo: [MessageID] = []
        var key: FolderKey?
        for id in ids {
            guard let s = summary(id), hasFlag(s.flags, .seen) != seen, !mailbox.model.inOutbox(s) else {
                continue // accelerators bypass the disabled actions
            }
            todo.append(id)
            key = FolderKey(account: s.accountId, folder: s.folderId)
        }
        guard let k = key, !todo.isEmpty else { return }
        let changing = todo
        let apply: @MainActor (Bool) -> Void = { [weak self] on in
            guard let self else { return }
            let change = flagChange(.seen, on: on)
            let changed = self.list.applyFlags(changing, set: change.set ?? [], clear: change.clear ?? [])
            if changed.isEmpty {
                return
            }
            // Marking unread raises the unread count.
            self.mailbox.adjustCounts(k, on ? -changed.count : changed.count, 0)
        }
        apply(seen)

        let n = changing.count
        let what: String
        switch (seen, n) {
        case (true, 1):
            what = L10n.T("Marking the message as read")
        case (true, _):
            what = L10n.N("Marking %d message as read", "Marking %d messages as read", n)
        case (false, 1):
            what = L10n.T("Marking the message as unread")
        case (false, _):
            what = L10n.N("Marking %d message as unread", "Marking %d messages as unread", n)
        }
        let change = flagChange(.seen, on: seen)
        call(
            API.MessageFlag.self,
            MessageFlagParams(accountId: k.account, messageIds: changing, set: change.set, clear: change.clear),
            what: what, onError: { _ in apply(!seen) }
        )
    }

    // MARK: Flagged

    /// Stars or unstars message `id` (actions.go `toggleFlagged`).
    public func toggleFlagged(_ id: MessageID) {
        guard let s = summary(id) else { return }
        setFlagged([id], !hasFlag(s.flags, .flagged))
    }

    /// Stars or unstars the messages that are not so yet, like `setSeen`
    /// (actions.go `setFlaggedIDs`); every star showing a changed message
    /// hears about it through `onStarChanged`.
    public func setFlagged(_ ids: [MessageID], _ on: Bool) {
        var todo: [MessageID] = []
        var account: AccountID?
        for id in ids {
            guard let s = summary(id), hasFlag(s.flags, .flagged) != on, !mailbox.model.inOutbox(s) else {
                continue // accelerators bypass the disabled actions
            }
            todo.append(id)
            account = s.accountId
        }
        guard let acc = account, !todo.isEmpty else { return }
        let changing = todo
        let apply: @MainActor (Bool) -> Void = { [weak self] on in
            guard let self else { return }
            let change = flagChange(.flagged, on: on)
            let changed = self.list.applyFlags(changing, set: change.set ?? [], clear: change.clear ?? [])
            for id in changed {
                self.onStarChanged?(id, on)
            }
        }
        apply(on)

        let n = changing.count
        let what: String
        switch (on, n) {
        case (true, 1):
            what = L10n.T("Starring the message")
        case (true, _):
            what = L10n.N("Starring %d message", "Starring %d messages", n)
        case (false, 1):
            what = L10n.T("Removing the star")
        case (false, _):
            what = L10n.N("Removing the star from %d message", "Removing the star from %d messages", n)
        }
        let change = flagChange(.flagged, on: on)
        call(
            API.MessageFlag.self,
            MessageFlagParams(accountId: acc, messageIds: changing, set: change.set, clear: change.clear),
            what: what, onError: { _ in apply(!on) }
        )
    }

    // MARK: Trash

    /// Moves message `id` to Trash after the optional confirmation shown
    /// over `parent` (actions.go `trashFrom`); its subject is the question's
    /// body.
    public func trash(_ id: MessageID, parent: AnyObject? = nil) {
        guard let s = summary(id) else { return }
        trash([id], subject: subjectText(s.subject), parent: parent)
    }

    /// Moves messages to Trash (message.delete) after the optional
    /// confirmation shown over `parent`, with `subject` as its body
    /// (actions.go `trashIDs`). For a single outbox message it cancels the
    /// send instead (`cancelSend`).
    public func trash(_ ids: [MessageID], subject: String, parent: AnyObject? = nil) {
        if ids.count == 1, let s = summary(ids[0]), mailbox.model.inOutbox(s) {
            cancelSend(ids[0], parent: parent)
            return
        }
        let msgs = summaries(ids)
        guard !msgs.isEmpty else { return }
        confirmTrash(parent, count: msgs.count, subject: subject) { [weak self] in
            self?.moveToTrash(msgs)
        }
    }

    /// Asks before moving `n` messages to Trash when the setting is on,
    /// then runs `proceed` (actions.go `confirmTrash`).
    private func confirmTrash(_ parent: AnyObject?, count n: Int, subject: String, then proceed: @escaping @MainActor () -> Void) {
        if !settings.confirmDelete {
            proceed()
            return
        }
        var heading = L10n.T("Move to Trash?")
        if n > 1 {
            // TRANSLATORS: %d is the number of messages of a conversation.
            heading = L10n.N("Move %d message to Trash?", "Move %d messages to Trash?", n)
        }
        ask(parent, heading: heading, body: subject, label: L10n.T("Move to _Trash"), then: proceed)
    }

    /// The confirmed half of `trash`: the rows go at once, the windows
    /// close, the folder counts follow, message.delete runs and a failure
    /// puts everything back.
    private func moveToTrash(_ msgs: [MessageSummary]) {
        let ids = msgs.map(\.id)
        let restore = list.removeRows(ids)
        for s in msgs {
            onWindowsClose?(s.id)
        }
        let acc = msgs[0].accountId
        // message.delete moves to the Trash role folder; a message already
        // there is expunged instead (no target count to credit).
        var target: FolderKey?
        if let trash = mailbox.model.folderByRole(acc, .trash), trash.id != msgs[0].folderId {
            target = FolderKey(account: acc, folder: trash.id)
        }
        let undo = trackMoves(msgs, target: target)
        let n = msgs.count
        let what = n == 1
            ? L10n.T("Moving the message to Trash")
            : L10n.N("Moving %d message to Trash", "Moving %d messages to Trash", n)
        call(API.MessageDelete.self, MessageDeleteParams(accountId: acc, messageIds: ids), what: what, onError: { _ in
            restore()
            undo()
        })
    }

    /// Adjusts the cached folder counts (the unread badges, the counts
    /// under the window title) for messages leaving their folder for
    /// `target` (nil: leaving the store) and returns the reverse, for a
    /// failed move (actions.go `trackMoves`). Read messages move only the
    /// totals; `MailModel.moveCounts` says where even those stay put.
    func trackMoves(_ msgs: [MessageSummary], target: FolderKey?) -> ListController.Restore {
        guard let first = msgs.first else {
            return {}
        }
        let unread = msgs.filter { !hasFlag($0.flags, .seen) }.count
        let n = msgs.count
        let src = FolderKey(account: first.accountId, folder: first.folderId)
        let shift: @MainActor (Int) -> Void = { [weak self] sign in
            self?.mailbox.moveCounts(src, target, sign * unread, sign * n)
        }
        shift(1)
        return { shift(-1) }
    }

    // MARK: Archive and junk

    /// Moves messages to the account's Archive folder (actions.go
    /// `archiveIDs`).
    public func archive(_ ids: [MessageID]) {
        moveToRole(ids, .archive, missing: L10n.T("This account has no archive folder")) { n in
            n == 1
                ? L10n.T("Archiving the message")
                : L10n.N("Archiving %d message", "Archiving %d messages", n)
        }
    }

    /// Moves message `id` to the account's Junk folder after a
    /// confirmation shown over `parent` (actions.go `junkFrom`).
    public func junk(_ id: MessageID, parent: AnyObject? = nil) {
        guard let s = summary(id) else { return }
        junk([id], subject: subjectText(s.subject), parent: parent)
    }

    /// Moves messages to the account's Junk folder after a confirmation
    /// shown over `parent` (actions.go `junkIDs`). Unlike Trash the question
    /// is always asked: the move feeds the server's spam filter and is not
    /// undone by moving back.
    public func junk(_ ids: [MessageID], subject: String, parent: AnyObject? = nil) {
        let msgs = summaries(ids)
        guard let first = msgs.first else { return }
        let missing = L10n.T("This account has no junk folder")
        guard mailbox.model.folderByRole(first.accountId, .junk) != nil else {
            toast(missing)
            return
        }
        var heading = L10n.T("Mark as junk?")
        if msgs.count > 1 {
            // TRANSLATORS: %d is the number of messages of a conversation.
            heading = L10n.N("Mark %d message as junk?", "Mark %d messages as junk?", msgs.count)
        }
        let ids = msgs.map(\.id)
        ask(parent, heading: heading, body: subject, label: L10n.T("Mark as _Junk")) { [weak self] in
            self?.moveToRole(ids, .junk, missing: missing) { n in
                n == 1
                    ? L10n.T("Marking the message as junk")
                    : L10n.N("Marking %d message as junk", "Marking %d messages as junk", n)
            }
        }
    }

    /// Moves messages to their account's folder with the given role
    /// (message.move; actions.go `moveIDsToRole`). `what` names the action
    /// in progressive form for the error toast, for the number moved;
    /// `missing` is the toast when the account has no such folder. The rows
    /// go at once and come back on failure; the folder counts follow the
    /// messages to the target (`trackMoves`). Outbox messages are left
    /// out (the daemon refuses moves on them), and a message already in the
    /// target folder is a no-op.
    func moveToRole(_ ids: [MessageID], _ role: FolderRole, missing: String, what: (Int) -> String) {
        let msgs = summaries(ids).filter { !mailbox.model.inOutbox($0) } // accelerators bypass the disabled actions
        guard let first = msgs.first else { return }
        let acc = first.accountId
        guard let target = mailbox.model.folderByRole(acc, role) else {
            toast(missing)
            return
        }
        if target.id == first.folderId {
            return
        }
        let moving = msgs.map(\.id)
        let restore = list.removeRows(moving)
        for s in msgs {
            onWindowsClose?(s.id)
        }
        let undo = trackMoves(msgs, target: FolderKey(account: acc, folder: target.id))
        call(
            API.MessageMove.self, MessageMoveParams(accountId: acc, messageIds: moving, targetFolderId: target.id),
            what: what(msgs.count), onError: { _ in
                restore()
                undo()
            }
        )
    }

    // MARK: Outbox

    /// Asks (always: there is no Trash to get the message back from) and
    /// then removes the outbox message `id` for good with message.delete;
    /// the confirmation is shown over `parent` (outbox.go `cancelSendFrom`).
    /// The row goes at once and comes back when the daemon refuses.
    public func cancelSend(_ id: MessageID, parent: AnyObject? = nil) {
        guard let s = summary(id) else { return }
        // TRANSLATORS: %s is the subject of the message.
        let body = L10n.T("“%s” will be removed from the outbox and not sent.", subjectText(s.subject))
        ask(parent, heading: L10n.T("Cancel sending this message?"), body: body, label: L10n.T("Do Not _Send")) { [weak self] in
            guard let self else { return }
            let restore = self.list.removeRows([id])
            self.onWindowsClose?(id)
            let undo = self.trackMoves([s], target: nil)
            // The drop is ours, not a delivery (trackOutbox). It is counted
            // before the call: removing a queued or failed message moves
            // pendingOutbox or failedOutbox, and the notify.syncState that
            // follows reloads the folders, possibly before this reply
            // arrives.
            self.mailbox.noteOutboxCancelled(s.accountId)
            self.call(
                API.MessageDelete.self, MessageDeleteParams(accountId: s.accountId, messageIds: [id]),
                what: L10n.T("Cancelling the send"),
                onError: { [weak self] _ in
                    // Nothing was dropped after all.
                    self?.mailbox.noteOutboxCancelFailed(s.accountId)
                    restore()
                    undo()
                },
                onOK: { [weak self] _ in
                    // The sidebar is refreshed here as well (an empty
                    // outbox disappears), for a daemon that sends no
                    // notify.syncState on the change; a second reload finds
                    // nothing left to count.
                    self?.mailbox.onOutboxChanged(s.accountId)
                }
            )
        }
    }

    /// Re-queues the failed message `id` (outbox.retry; outbox.go
    /// `retryOutbox`). The banner shows "queued" at once; a refused retry
    /// fetches the real state back.
    public func retryOutbox(_ id: MessageID) {
        guard let s = summary(id) else { return }
        if let lm = cache.loaded(id), var o = lm.msg?.summary.outbox {
            o.state = .queued
            o.error = nil
            lm.msg?.summary.outbox = o
        }
        if let found = mailbox.model.message(id), var o = found.summary.outbox {
            o.state = .queued
            o.error = nil
            mailbox.model.setOutbox(id, o)
        }
        onOutboxStateChanged?(id)
        call(
            API.OutboxRetry.self, OutboxRetryParams(accountId: s.accountId, messageId: id),
            what: L10n.T("Retrying the send"), onError: { [weak self] _ in
                guard let self else { return }
                self.cache.refetch(s) { [weak self] _ in
                    self?.onOutboxStateChanged?(id)
                }
            }
        )
    }

    // MARK: Remote content

    /// Fetches the body of `id` again with remote images allowed for this
    /// one call and shows the result wherever the message is on display
    /// (remote.go `loadRemoteImages`, through the cache: the bar shows the
    /// wait from the click on, a request already running is left alone, a
    /// failure is a toast with the bar back as it was).
    public func loadImages(_ id: MessageID) {
        guard let s = summary(id) else { return }
        cache.loadImages(s) { _ in }
    }

    /// Puts the sender of `id` on the daemon's known-senders list
    /// (sender.add), switches the stored remote-content preference to
    /// "from known senders" when it was "never" (otherwise the list would
    /// change nothing), and loads this message's images now (remote.go
    /// `trustSender`). The bar shows the wait from the click on, through
    /// all three calls.
    public func trustSender(_ id: MessageID) {
        guard let s = summary(id), let first = s.from.first else { return }
        let address = first.address.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !address.isEmpty else { return }
        guard let lm = cache.beginLoadingImages(id) else { return }
        call(
            API.SenderAdd.self, SenderAddParams(address: address), what: L10n.T("Trusting the sender"),
            onError: { [weak self] _ in
                self?.cache.imagesDone(id, lm)
            },
            onOK: { [weak self] _ in
                self?.ensureKnownSendersPolicy { [weak self] in
                    self?.cache.fetchRemoteImages(s, lm) { _ in }
                }
            }
        )
    }

    /// Raises the stored remote-content preference from "block" to
    /// "knownSenders" (config.get, then config.set with the whole set echoed
    /// back) and runs `done` afterwards, or at once when nothing needs
    /// changing (remote.go `ensureKnownSendersPolicy`). A failure is toasted
    /// and `done` still runs: the sender is trusted either way.
    func ensureKnownSendersPolicy(_ done: @escaping @MainActor () -> Void) {
        mailbox.perform(API.ConfigGet.self, EmptyParams()) { [weak self] outcome in
            guard let self else { return }
            switch outcome {
            case .failure(let err):
                self.policyChangeFailed(err)
                done()
            case .success(let got):
                guard got.preferences.remoteContent == .block else {
                    done()
                    return
                }
                var want = got.preferences
                want.remoteContent = .knownSenders
                self.mailbox.perform(API.ConfigSet.self, ConfigSetParams(preferences: want)) { [weak self] outcome in
                    guard let self else { return }
                    if case .failure(let err) = outcome {
                        self.policyChangeFailed(err)
                    }
                    done()
                }
            }
        }
    }

    private func policyChangeFailed(_ err: any Error) {
        log.warning("remote content preference: \(String(describing: err), privacy: .public)")
        toast(rpcErrorText(L10n.T("Changing the remote content preference"), err))
    }

    // MARK: Compose

    /// Opens a reply or forward of message `id` (compose_open.go
    /// `openCompose`). The template comes from the backend (draft.create:
    /// recipients, subject, the original quoted formatted with its pictures
    /// copied into the attachment store); a second click while it is being
    /// prepared does nothing (one window will appear). Only when the backend
    /// cannot answer does the window open from what the pane knows
    /// (`prefill`), with a toast unless the fallback is the normal course
    /// (`composeFallbackText`).
    public func openCompose(_ kind: ComposeKind, _ id: MessageID) {
        guard let s = summary(id), !composing.contains(id) else { return }
        let src = composeSource(summary: s, loaded: cache.loaded(id))
        // The account's own address, for Reply All exclusion; the first
        // account's when the message's is unknown (compose.Manager
        // `SelfAddress`).
        let me = mailbox.model.account(s.accountId).map(selfAddress)
            ?? mailbox.model.enabledAccounts.first.map(selfAddress)
            ?? Address(address: "")
        let fallback: @MainActor () -> Void = { [weak self] in
            guard let self else { return }
            var p = prefill(kind: kind, source: src, self: me)
            p.accountID = s.accountId
            self.openCompose?(p)
        }

        composing.insert(id)
        let attributionLine = attribution(kind: kind, source: src)
        let params = DraftCreateParams(
            accountId: s.accountId, mode: kind.mode, messageId: id,
            attribution: attributionLine.isEmpty ? nil : attributionLine
        )
        mailbox.perform(API.DraftCreate.self, params, timeout: RPCTimeouts.compose) { [weak self] outcome in
            guard let self else { return }
            self.composing.remove(id)
            switch outcome {
            case .failure(let err):
                self.log.warning("draft.create \(params.mode.rawValue, privacy: .public): \(String(describing: err), privacy: .public)")
                let text = composeFallbackText(composeWhat(kind), err)
                if !text.isEmpty {
                    self.toast(text)
                }
                fallback()
            case .success(let res):
                var p = fromDraft(kind: kind, draft: res.draft, blocked: res.blocked)
                p.accountID = s.accountId
                self.openCompose?(p)
            }
        }
    }

    /// Opens message `id` of a Drafts folder in the compose window, or
    /// raises the window already editing it (drafts.go `openDraft`). A
    /// second request while the first is on its way does nothing; a daemon
    /// without draft.open shows the message instead.
    public func openDraft(_ id: MessageID) {
        guard let s = summary(id), !composing.contains(id) else { return }
        composing.insert(id)
        let params = DraftOpenParams(accountId: s.accountId, messageId: id)
        mailbox.perform(API.DraftOpen.self, params, timeout: RPCTimeouts.compose) { [weak self] outcome in
            guard let self else { return }
            self.composing.remove(id)
            switch outcome {
            case .failure(let err):
                self.log.warning("draft.open: \(String(describing: err), privacy: .public)")
                if draftOpenUnsupported(err) {
                    self.openMessageWindow?(s)
                    return
                }
                self.toast(draftOpenErrorText(err))
            case .success(let res):
                if self.raiseDraft?(res.draft) == true {
                    return
                }
                self.openCompose?(fromDraft(kind: .edit, draft: res.draft, blocked: res.blocked))
                if let n = res.skipped?.count, n > 0 {
                    self.toast(draftSkippedText(n))
                }
            }
        }
    }

    // MARK: Plumbing

    /// The window's `callThen` (actions.go): on failure the error is
    /// logged, toasted as `rpcErrorText(what, err)` and handed to
    /// `onError` (which reverts the optimistic change); on success `onOK`
    /// runs with the result. Either callback may be nil. Nothing runs once
    /// the mailbox closed.
    private func call<M: RPCMethod>(
        _ method: M.Type, _ params: M.Params, what: String, timeout: Duration? = nil,
        onError: (@MainActor (any Error) -> Void)? = nil, onOK: (@MainActor (M.Result) -> Void)? = nil
    ) {
        mailbox.perform(method, params, timeout: timeout) { [weak self] outcome in
            guard let self else { return }
            switch outcome {
            case .success(let res):
                onOK?(res)
            case .failure(let err):
                self.log.warning("\(M.name, privacy: .public): \(String(describing: err), privacy: .public)")
                self.toast(rpcErrorText(what, err))
                onError?(err)
            }
        }
    }

    /// Shows the destructive confirmation over `parent` and runs `proceed`
    /// when the user confirms. Without a hook the action is refused: a
    /// destructive step the user asked to be questioned about never runs
    /// unasked.
    private func ask(
        _ parent: AnyObject?, heading: String, body: String, label: String,
        then proceed: @escaping @MainActor () -> Void
    ) {
        guard let confirm else {
            log.error("no confirmation hook is installed; the action was refused")
            return
        }
        Task { @MainActor in
            if await confirm(parent, heading, body, label) {
                proceed()
            }
        }
    }
}
