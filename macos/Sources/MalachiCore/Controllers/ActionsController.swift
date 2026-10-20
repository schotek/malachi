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
    /// The forward of several messages is being prepared (`forwardMessages`).
    private var forwardingMany = false
    /// The drafts the assistant panel's Open Draft is looking up.
    private var findingDrafts: Set<DraftID> = []
    /// The pages of draft.list one Open Draft reads at most.
    static let draftListPageLimit = 500
    static let draftListMaxPages = 20

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
    /// window outliving the folder it was opened from, or the user's reply
    /// in Sent that a conversation shows (`MailModel.sentMessage`; its
    /// card's reply and forward).
    public func summary(_ id: MessageID) -> MessageSummary? {
        mailbox.model.message(id)?.summary ?? cache.summary(id) ?? mailbox.model.sentMessage(id)
    }

    /// The known ones of the given messages, in order (actions.go
    /// `summaries`).
    private func summaries(_ ids: [MessageID]) -> [MessageSummary] {
        ids.compactMap { summary($0) }
    }

    /// What the per-message header buttons and actions allow for the
    /// selected row (actions.go `setMessageActionsSensitive`).
    public func actionFlags(for row: ListRow?) -> ActionFlags {
        withCrm(messageActionState(row, model: mailbox.model), row?.message)
    }

    /// `actionFlags` for a single message (a message window's `msg.*`
    /// group).
    public func flags(for s: MessageSummary) -> ActionFlags {
        withCrm(messageActionState(ListRow(key: ListKey(message: s.id), message: s), model: mailbox.model), s)
    }

    /// The application's eWay-CRM state (docs/eway.md); nil leaves the
    /// CRM flags off.
    public var crm: CRMState?

    /// `f` with selection.Flags Crm and CrmSaved of message `s`: it can be
    /// saved (`CRM.offer(for:rows:)` of the one row), and it is saved.
    private func withCrm(_ f: ActionFlags, _ s: MessageSummary?) -> ActionFlags {
        guard f.on, let crm, let s else { return f }
        var out = f
        let o = crm.offer([s])
        out.crm = o.enabled
        out.crmSaved = o.saved
        return out
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
    /// sends one message.flag; a failure puts everything back, success in
    /// marking read withdraws their desktop notifications (actions.go
    /// `setSeenIDs`). The messages are of one account (a selection is),
    /// possibly of several of its folders (a search): each folder's count
    /// follows its own.
    public func setSeen(_ ids: [MessageID], _ seen: Bool) {
        var todo: [MessageID] = []
        var key: FolderKey?
        var folderOf: [MessageID: FolderKey] = [:]
        for id in ids {
            guard let s = summary(id), hasFlag(s.flags, .seen) != seen, !mailbox.model.inOutbox(s) else {
                continue // accelerators bypass the disabled actions
            }
            todo.append(id)
            key = FolderKey(account: s.accountId, folder: s.folderId)
            folderOf[id] = key
        }
        guard let k = key, !todo.isEmpty else { return }
        let changing = todo
        let apply: @MainActor (Bool) -> Void = { [weak self] on in
            guard let self else { return }
            let change = flagChange(.seen, on: on)
            let changed = self.list.applyFlags(changing, set: change.set ?? [], clear: change.clear ?? [])
            // A window may show one no list does (notify_open.go).
            self.cache.applyFlags(changing, set: change.set ?? [], clear: change.clear ?? [])
            if changed.isEmpty {
                return
            }
            // Marking unread raises the unread count of each message's
            // folder.
            var perFolder: [FolderKey: Int] = [:]
            for id in changed {
                perFolder[folderOf[id] ?? k, default: 0] += 1
            }
            for (f, n) in perFolder {
                self.mailbox.adjustCounts(f, on ? -n : n, 0)
            }
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
            what: what, onError: { _ in apply(!seen) }, onOK: { [weak self] _ in
                if seen {
                    self?.mailbox.withdrawNotifications(changing)
                }
            }
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
    /// send instead (`cancelSend`), for several queued ones the sending of
    /// them all after one question (`cancelSends`).
    public func trash(_ ids: [MessageID], subject: String, parent: AnyObject? = nil) {
        if ids.count == 1, let s = summary(ids[0]), mailbox.model.inOutbox(s) {
            cancelSend(ids[0], parent: parent)
            return
        }
        let msgs = summaries(ids)
        guard !msgs.isEmpty else { return }
        let queued = msgs.filter { mailbox.model.inOutbox($0) }
        if queued.count == msgs.count {
            cancelSends(queued, parent: parent)
            return
        }
        if !queued.isEmpty {
            return // a selection is one folder's; outbox rows never mix in
        }
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
    /// close, the folder counts follow, message.delete runs, a failure puts
    /// everything back and success withdraws the desktop notifications.
    private func moveToTrash(_ msgs: [MessageSummary]) {
        let ids = msgs.map(\.id)
        let restore = list.removeRows(ids)
        for s in msgs {
            onWindowsClose?(s.id)
        }
        let acc = msgs[0].accountId
        // message.delete moves to the Trash role folder; a message already
        // there is expunged instead (no target count to credit).
        let trash = mailbox.model.folderByRole(acc, .trash)
        let target = trash.map { FolderKey(account: acc, folder: $0.id) }
        let undoMoves = trackMoves(msgs.filter { $0.folderId != trash?.id }, target: target)
        let undoExpunged = trackMoves(msgs.filter { $0.folderId == trash?.id }, target: nil)
        let undo: ListController.Restore = {
            undoExpunged()
            undoMoves()
        }
        let n = msgs.count
        let what = n == 1
            ? L10n.T("Moving the message to Trash")
            : L10n.N("Moving %d message to Trash", "Moving %d messages to Trash", n)
        call(API.MessageDelete.self, MessageDeleteParams(accountId: acc, messageIds: ids), what: what, onError: { _ in
            restore()
            undo()
        }, onOK: { [weak self] _ in
            self?.mailbox.withdrawNotifications(ids)
        })
    }

    /// Adjusts the cached folder counts (the unread badges, the counts
    /// under the window title) for messages leaving their folder for
    /// `target` (nil: leaving the store) and returns the reverse, for a
    /// failed move (actions.go `trackMoves`). Read messages move only the
    /// totals; `MailModel.moveCounts` says where even those stay put.
    func trackMoves(_ msgs: [MessageSummary], target: FolderKey?) -> ListController.Restore {
        guard !msgs.isEmpty else {
            return {}
        }
        // Per source folder: a selection of a search may come from several.
        var order: [FolderKey] = []
        var counts: [FolderKey: (unread: Int, total: Int)] = [:]
        for s in msgs {
            let src = FolderKey(account: s.accountId, folder: s.folderId)
            if counts[src] == nil {
                order.append(src)
                counts[src] = (0, 0)
            }
            counts[src]!.total += 1
            if !hasFlag(s.flags, .seen) {
                counts[src]!.unread += 1
            }
        }
        let shift: @MainActor (Int) -> Void = { [weak self] sign in
            for src in order {
                guard let c = counts[src] else { continue }
                self?.mailbox.moveCounts(src, target, sign * c.unread, sign * c.total)
            }
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
    /// messages to the target (`trackMoves`), and success withdraws their
    /// desktop notifications. Outbox messages are left out (the daemon
    /// refuses moves on them), and a message already in the target folder
    /// is a no-op.
    func moveToRole(_ ids: [MessageID], _ role: FolderRole, missing: String, what: (Int) -> String) {
        let msgs = movable(ids)
        guard let first = msgs.first else { return }
        guard let target = mailbox.model.folderByRole(first.accountId, role) else {
            toast(missing)
            return
        }
        move(msgs, to: target.id, what: what)
    }

    /// Moves messages to another folder of their account (Move To in a
    /// message's context menu; actions.go `moveIDsTo`), as `moveToRole`
    /// does.
    public func moveTo(_ ids: [MessageID], folder: FolderID) {
        move(movable(ids), to: folder, what: ContextMenu.movingText)
    }

    /// The summaries of `ids` a move takes: not the outbox's (accelerators
    /// and menus bypass the disabled actions).
    private func movable(_ ids: [MessageID]) -> [MessageSummary] {
        summaries(ids).filter { !mailbox.model.inOutbox($0) }
    }

    /// The move itself (actions.go `moveListTo`): the rows go at once and
    /// come back on failure, the counts follow, the windows close.
    private func move(_ msgs: [MessageSummary], to target: FolderID, what: (Int) -> String) {
        // The messages already in the target stay where they are.
        let msgs = msgs.filter { $0.folderId != target }
        guard let first = msgs.first else { return }
        let acc = first.accountId
        let moving = msgs.map(\.id)
        let restore = list.removeRows(moving)
        for s in msgs {
            onWindowsClose?(s.id)
        }
        let undo = trackMoves(msgs, target: FolderKey(account: acc, folder: target))
        call(
            API.MessageMove.self, MessageMoveParams(accountId: acc, messageIds: moving, targetFolderId: target),
            what: what(msgs.count), onError: { _ in
                restore()
                undo()
            }, onOK: { [weak self] _ in
                self?.mailbox.withdrawNotifications(moving)
            }
        )
    }

    // MARK: Folders

    /// Mark All as Read in a folder's context menu (folder.markRead;
    /// context_menus.go `markFolderRead`). The daemon's
    /// notify.messagesChanged brings the counts and the list up to date.
    public func markFolderRead(_ k: FolderKey) {
        call(API.FolderMarkRead.self, FolderMarkReadParams(accountId: k.account, folderId: k.folder),
             what: ContextMenu.markingReadText())
    }

    /// Empty Trash or Empty Junk (folder.empty), always after the
    /// question, shown over `parent`; the notification brings the folder
    /// up to date.
    public func emptyFolder(_ k: FolderKey, parent: AnyObject? = nil) {
        guard let f = mailbox.model.folder(k) else { return }
        let junk = f.role == .junk
        let q = ContextMenu.emptyQuestion(junk: junk, count: f.total)
        ask(parent, heading: q.heading, body: q.body, label: q.confirm) { [weak self] in
            self?.call(API.FolderEmpty.self, FolderEmptyParams(accountId: k.account, folderId: k.folder),
                       what: ContextMenu.emptyingText(junk: junk))
        }
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

    /// Trash on several queued messages: one question, then the sending
    /// of them all is cancelled (message.delete), as `cancelSend` does for
    /// one (selection.go `cancelSends`).
    func cancelSends(_ msgs: [MessageSummary], parent: AnyObject? = nil) {
        if msgs.count == 1 {
            cancelSend(msgs[0].id, parent: parent)
            return
        }
        ask(parent, heading: Selection.cancelSendingQuestion(msgs.count), body: "", label: L10n.T("Do Not _Send")) { [weak self] in
            guard let self else { return }
            let ids = msgs.map(\.id)
            let acc = msgs[0].accountId
            let restore = self.list.removeRows(ids)
            for id in ids {
                self.onWindowsClose?(id)
            }
            let undo = self.trackMoves(msgs, target: nil)
            for _ in msgs {
                self.mailbox.noteOutboxCancelled(acc)
            }
            self.call(
                API.MessageDelete.self, MessageDeleteParams(accountId: acc, messageIds: ids),
                what: L10n.T("Cancelling the send"),
                onError: { [weak self] _ in
                    for _ in msgs {
                        self?.mailbox.noteOutboxCancelFailed(acc)
                    }
                    restore()
                    undo()
                },
                onOK: { [weak self] _ in
                    self?.mailbox.onOutboxChanged(acc)
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

    /// Downloads the pictures of `id` kept on the mail server only and
    /// shows the message again wherever it is on display (remote.go
    /// `downloadPictures`, through the cache: the bars show the wait from
    /// the click on, a request already running is left alone, a failure is
    /// a toast through `toast`, or the controller's own when nil, with the
    /// bar back as it was).
    public func downloadPictures(_ id: MessageID, toast: (@MainActor (String) -> Void)? = nil) {
        guard let s = summary(id) else { return }
        cache.downloadPictures(s, toast: toast ?? self.toast) { _ in }
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
    /// `openCompose` / `openComposeFrom`). The template comes from the
    /// backend (draft.create: recipients, subject, the original quoted
    /// formatted with its pictures copied into the attachment store); a
    /// second click while it is being prepared does nothing (one window will
    /// appear). Only when the backend cannot answer does the window open
    /// from what the pane knows (`prefill`), with a toast unless the
    /// fallback is the normal course (`composeFallbackText`).
    ///
    /// A forward of a message with attachments kept on the mail server (or
    /// a body not downloaded yet, or one the cache does not hold) downloads
    /// it first (`forwardNeedsDownload`), since draft.create imports only
    /// what is stored. When the download fails the user is asked over
    /// `parent` whether to forward without them ("Forward Without
    /// Attachments?", the error as the body), unless asking would change
    /// nothing: no daemon, one without message.download, or a message over
    /// its cap (`askForwardWithout`).
    /// The question has no answer for Cancel: the request ends when it is
    /// asked, and a confirmation starts it again (unless another one for
    /// the message runs by then).
    ///
    /// A reply or reply to all of a message whose pictures are kept on the
    /// mail server only downloads it first as well (`replyNeedsDownload`),
    /// so the quote has them; a failure is only logged and the reply goes
    /// on (the compose window says what draft.create left out). A comment
    /// (a reply on an account that comments) quotes nothing and downloads
    /// nothing.
    public func openCompose(_ kind: ComposeKind, _ id: MessageID, from parent: AnyObject? = nil) {
        openCompose(kind, id, from: parent, ownHTML: "", opened: nil)
    }

    /// `openCompose(_:_:from:)` with `ownHTML` in the window's editor as the
    /// user's own text (compose_open.go `openComposeWith`;
    /// `ComposeParams.ownHTML`, safe HTML from
    /// `Conversation.quickReplyHTML`), the comment's as well. `opened` is
    /// called right after the window opened (`openCompose` hook, `openWith`),
    /// and never when none does: the message is unknown, one is being
    /// prepared for it already, a forward was refused, or a comment's
    /// draft.create failed. The chat's quick-reply bar clears its text then.
    public func openCompose(
        _ kind: ComposeKind, _ id: MessageID, from parent: AnyObject?, ownHTML: String,
        opened: (@MainActor () -> Void)?
    ) {
        guard let s = summary(id), !composing.contains(id) else { return }
        composing.insert(id)
        if kind == .reply, mailbox.model.account(s.accountId)?.can(.comment) == true {
            createDraft(kind, s, ownHTML: ownHTML, opened: opened)
            return
        }
        if kind == .reply || kind == .replyAll, replyNeedsDownload(cache.loaded(id)) {
            let cache = cache
            Task { @MainActor [weak self] in
                // download() logs a failure; the reply goes on without.
                _ = try? await cache.download(accountID: s.accountId, messageID: id)
                self?.createDraft(kind, s, ownHTML: ownHTML, opened: opened)
            }
            return
        }
        guard kind == .forward, forwardNeedsDownload(cache.loaded(id)) else {
            createDraft(kind, s, ownHTML: ownHTML, opened: opened)
            return
        }
        let cache = cache
        Task { @MainActor [weak self] in
            var failure: (any Error)?
            do {
                try await cache.download(accountID: s.accountId, messageID: id)
            } catch {
                failure = error
            }
            guard let self else { return }
            guard let err = failure, askForwardWithout(err) else {
                self.createDraft(kind, s, ownHTML: ownHTML, opened: opened)
                return
            }
            self.composing.remove(id)
            guard await self.confirmForwardWithout(parent, err), !self.composing.contains(id) else { return }
            self.composing.insert(id)
            self.createDraft(kind, s, ownHTML: ownHTML, opened: opened)
        }
    }

    /// The line the compose window puts above the quote of a reply (or
    /// forward, `kind`) to message `id`: `attribution(kind:source:)` over
    /// `composeSource(summary:loaded:)` of what the window knows of it, as
    /// `createDraft` computes it; "" for an unknown message. The chat's
    /// quick-reply bar hands it to draft.create (`QuickReplyHost.attribution`).
    public func replyAttribution(_ kind: ComposeKind, _ id: MessageID) -> String {
        guard let s = summary(id) else { return "" }
        return attribution(kind: kind, source: composeSource(summary: s, loaded: cache.loaded(id)))
    }

    /// What `openCompose` does before draft.create of a reply, for the
    /// chat's quick-reply bar (`QuickReplyHost.beforeCreate`): message `id`
    /// is downloaded first when its pictures are kept on the mail server
    /// only (`replyNeedsDownload`), the outcome ignored (the download logs a
    /// failure), then `done` runs on the main actor; at once otherwise, and
    /// for an unknown message.
    public func prepareReply(_ id: MessageID, done: @escaping @MainActor () -> Void) {
        guard let s = summary(id), replyNeedsDownload(cache.loaded(id)) else {
            done()
            return
        }
        let cache = cache
        Task { @MainActor in
            _ = try? await cache.download(accountID: s.accountId, messageID: id)
            done()
        }
    }

    /// "Forward Without Attachments?" after the failed download `err`
    /// (compose_open.go, `ConfirmDestructive`); without a confirmation hook
    /// the forward is refused, like every question that must be asked.
    private func confirmForwardWithout(_ parent: AnyObject?, _ err: any Error) async -> Bool {
        guard let confirm else {
            log.error("no confirmation hook is installed; the forward was refused")
            return false
        }
        return await confirm(
            parent, L10n.T("Forward Without Attachments?"),
            rpcErrorText(L10n.T("Downloading the attachments"), err),
            L10n.T("_Forward Without Attachments"))
    }

    /// The draft.create half of `openCompose` for message `s`, whose id is
    /// in `composing` (compose_open.go `create`): the template from the
    /// backend with what it could not import (`skipped`), or the fallback.
    /// A reply on an account that comments (an issue) is a comment
    /// (`createComment`); a forward of a message whose account writes no
    /// mail is written in a mail account (`forwardAccount`), and
    /// draft.create finds the message by `messageAccountId`. `ownHTML` and
    /// `opened`: `openCompose(_:_:from:ownHTML:opened:)`.
    private func createDraft(
        _ kind: ComposeKind, _ s: MessageSummary, ownHTML: String = "", opened: (@MainActor () -> Void)? = nil
    ) {
        let id = s.id
        let own = mailbox.model.account(s.accountId)
        if kind == .reply, own?.can(.comment) == true {
            createComment(s, ownHTML: ownHTML, opened: opened)
            return
        }
        // The account the draft is written in.
        var from = s.accountId
        var messageAccount: AccountID?
        if kind == .forward, let own, !own.can(.compose) {
            guard let target = forwardAccount() else {
                // Forward is off without such an account; an accelerator
                // bypasses that.
                composing.remove(id)
                log.warning("forward: no account writes mail")
                return
            }
            from = target.id
            messageAccount = s.accountId
        }
        let src = composeSource(summary: s, loaded: cache.loaded(id))
        // The account's own address, for Reply All exclusion; the first
        // mail account's when the message's is unknown (compose.Manager
        // `SelfAddress`).
        let me = mailbox.model.account(from).map(selfAddress)
            ?? mailbox.model.mailSidebarAccounts.first.map(selfAddress)
            ?? Address(address: "")
        let attributionLine = attribution(kind: kind, source: src)
        let fallback: @MainActor () -> Void = { [weak self] in
            guard let self else { return }
            var p = prefill(kind: kind, source: src, self: me)
            p.accountID = from
            p.attribution = attributionLine
            p.ownHTML = ownHTML
            self.openWith(p, opened)
        }

        let params = DraftCreateParams(
            accountId: from, mode: kind.mode, messageId: id,
            attribution: attributionLine.isEmpty ? nil : attributionLine, messageAccountId: messageAccount
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
                p.accountID = from
                p.skipped = res.skipped?.count ?? 0
                p.attribution = attributionLine
                p.ownHTML = ownHTML
                self.openWith(p, opened)
            }
        }
    }

    /// Forwards several messages in one new message (draft.create forward
    /// with `messageIds`; selection.go `forwardMessages`): the daemon
    /// attaches each whole; one it could not attach is counted in the
    /// compose window's note (`skipped`). From their account, or a mail
    /// account for an issue tracker's (`forwardAccount`). One request at a
    /// time.
    public func forwardMessages(_ ids: [MessageID]) {
        guard ids.count >= 2, !forwardingMany, let s = summary(ids[0]) else { return }
        var from = s.accountId
        var messageAccount: AccountID?
        if let own = mailbox.model.account(s.accountId), !own.can(.compose) {
            guard let target = forwardAccount() else { return }
            from = target.id
            messageAccount = s.accountId
        }
        let params = DraftCreateParams(
            accountId: from, mode: .forward, messageAccountId: messageAccount,
            messageIds: Array(ids.prefix(Selection.maxForward)))
        forwardingMany = true
        mailbox.perform(API.DraftCreate.self, params, timeout: RPCTimeouts.compose) { [weak self] outcome in
            guard let self else { return }
            self.forwardingMany = false
            switch outcome {
            case .failure(let err):
                self.log.warning("draft.create forward of \(ids.count): \(String(describing: err), privacy: .public)")
                self.toast(rpcErrorText(composeWhat(.forward), err))
            case .success(let res):
                var p = fromDraft(kind: .forward, draft: res.draft, blocked: res.blocked)
                p.accountID = from
                p.skipped = res.skipped?.count ?? 0
                self.openCompose?(p)
            }
        }
    }

    /// Opens each message in a window of its own (a draft in the compose
    /// window), at most `Selection.maxOpen` (Open in New Window on several
    /// rows).
    public func openMessages(_ msgs: [MessageSummary]) {
        guard msgs.count <= Selection.maxOpen else { return }
        for s in msgs {
            openMessage(s)
        }
    }

    /// The mail account a message of an account that writes no mail (an
    /// issue) is forwarded from (`Capabilities.forwardFrom`): the account
    /// of the folder selected in the sidebar when it writes mail (an issue
    /// a search found from a mailbox), else the first enabled one that
    /// does; nil when none does (Forward is off then).
    private func forwardAccount() -> Account? {
        let m = mailbox.model
        return Capabilities.forwardFrom(m.accounts, from: m.selected?.account ?? "")
    }

    /// A comment on the issue of message `s`, whose account comments
    /// (draft.create reply there): the compose window in comment mode
    /// (`ComposeParams.comment`). Nothing is quoted, so there is no
    /// attribution, and no fallback either: without a comment draft from
    /// the daemon there is nothing to write, only the toast (and no
    /// `opened`).
    private func createComment(_ s: MessageSummary, ownHTML: String = "", opened: (@MainActor () -> Void)? = nil) {
        let id = s.id
        let params = DraftCreateParams(accountId: s.accountId, mode: .reply, messageId: id)
        mailbox.perform(API.DraftCreate.self, params, timeout: RPCTimeouts.compose) { [weak self] outcome in
            guard let self else { return }
            self.composing.remove(id)
            switch outcome {
            case .failure(let err):
                self.log.warning("draft.create comment: \(String(describing: err), privacy: .public)")
                self.toast(rpcErrorText(composeWhat(.reply), err))
            case .success(let res):
                var p = fromDraft(kind: .reply, draft: res.draft, blocked: res.blocked)
                p.accountID = s.accountId
                p.ownHTML = ownHTML
                self.openWith(p, opened)
            }
        }
    }

    /// compose_open.go `openWith`: opens the compose window with `p`, then
    /// calls `opened` (nil for none), which tells the chat's quick-reply bar
    /// that a window holds its text now — never when no window can open
    /// (no `openCompose` host).
    private func openWith(_ p: ComposeParams, _ opened: (@MainActor () -> Void)?) {
        guard let openCompose else { return }
        openCompose(p)
        opened?()
    }

    /// `openCompose` for a message the list and the cache may not know (the
    /// board's Reply, which has only the ids): its summary from message.get
    /// first (`MessageCache.lookUpOutcome`, which keeps it), then as for a
    /// listed one. A message the daemon no longer has says so in a toast;
    /// one it could not be asked about says why. A reply on an account that
    /// comments is named "Comment" (`Jira.replyLabel`).
    public func openCompose(_ kind: ComposeKind, account: AccountID, message id: MessageID, from parent: AnyObject? = nil) {
        if summary(id) != nil {
            openCompose(kind, id, from: parent)
            return
        }
        let what = kind == .reply && mailbox.model.account(account)?.can(.comment) == true
            ? Jira.replyLabel(comment: true) : composeWhat(kind)
        cache.lookUpOutcome(accountId: account, id: id) { [weak self] outcome in
            guard let self else { return }
            switch outcome {
            case .found:
                self.openCompose(kind, id, from: parent)
            case .gone:
                self.log.warning("compose: message.get found no message")
                self.toast(L10n.T("%s failed: the message is no longer on the server", what))
            case .failed(let err):
                self.toast(rpcErrorText(what, err))
            }
        }
    }

    /// Opens message `s` as a double click does, on its row in the list or
    /// on the header of its card in a conversation (message_view.go
    /// `openMessage`): a message of a Drafts folder in the compose window,
    /// any other in a window of its own.
    public func openMessage(_ s: MessageSummary) {
        if mailbox.model.inDrafts(s) {
            openDraft(s.id)
        } else {
            openMessageWindow?(s)
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

    // MARK: Draft cards

    /// A draft card's Edit (conversation_draft.go `editThreadDraft`): the
    /// window that edits the draft already comes to the front; otherwise
    /// the draft as draft.get has it now (the card may show an older
    /// version) opens in the compose window, the comment window for a
    /// comment draft. A draft that is gone says so; a second request while
    /// one runs does nothing.
    public func editThreadDraft(_ d: Draft) {
        if raiseDraft?(d) == true {
            return
        }
        guard let id = d.id, !id.rawValue.isEmpty, !findingDrafts.contains(id) else { return }
        findingDrafts.insert(id)
        mailbox.perform(API.DraftGet.self, DraftGetParams(accountId: d.accountId, draftId: id)) { [weak self] outcome in
            guard let self else { return }
            self.findingDrafts.remove(id)
            switch outcome {
            case .failure(let err):
                self.log.warning("draft.get: \(String(describing: err), privacy: .public)")
                if (err as? RPCError)?.code == .draftNotFound {
                    self.toast(Conversation.draftGone())
                    return
                }
                self.toast(rpcErrorText(L10n.T("Opening the draft"), err))
            case .success(let res):
                if self.raiseDraft?(res.draft) == true {
                    return
                }
                self.openCompose?(fromDraft(kind: .edit, draft: res.draft, blocked: BlockedContent()))
            }
        }
    }

    /// A draft card's Discard (conversation_draft.go
    /// `discardThreadDraft`): a draft a compose window edits is that
    /// window's to discard (it comes to the front); otherwise, after the
    /// question, draft.delete. notify.draftsChanged takes the card away.
    public func discardThreadDraft(_ d: Draft, parent: AnyObject? = nil) {
        if raiseDraft?(d) == true {
            return
        }
        guard let id = d.id, !id.rawValue.isEmpty else { return }
        ask(parent, heading: Conversation.discardQuestion(), body: "", label: Conversation.draftDiscardLabel()) {
            [weak self] in
            self?.call(
                API.DraftDelete.self, DraftDeleteParams(accountId: d.accountId, draftId: id),
                what: L10n.T("Discarding the draft"))
        }
    }

    // MARK: The assistant panel

    /// The assistant panel's Open Draft (ui/internal/assistant, the In App
    /// target): the draft `id` of account `account`, which Claude Code
    /// saved through the bridge's create_draft, opens in the compose
    /// window, or the window already editing it comes to the front. The
    /// ids come from the bridge's own line of the tool result
    /// (`Assistant.parseDraftResult`), so the draft is looked up with
    /// draft.list first (pages of 500, the cursor followed), and only a
    /// draft the daemon lists is opened; one that is gone (sent, deleted,
    /// or never there) says so. A second request while one runs does
    /// nothing.
    public func openSavedDraft(account: AccountID, id: DraftID) {
        guard !id.rawValue.isEmpty, !findingDrafts.contains(id) else { return }
        findingDrafts.insert(id)
        findDraft(account, id, cursor: nil, page: 0) { [weak self] outcome in
            guard let self else { return }
            self.findingDrafts.remove(id)
            switch outcome {
            case .failure(let err):
                self.log.warning("draft.list: \(String(describing: err), privacy: .public)")
                self.toast(rpcErrorText(L10n.T("Opening the draft"), err))
            case .success(nil):
                self.toast(Assistant.panelTexts().draftGone)
            case .success(let draft?):
                if self.raiseDraft?(draft) == true {
                    return
                }
                self.openCompose?(fromDraft(kind: .edit, draft: draft, blocked: BlockedContent()))
            }
        }
    }

    /// Pages draft.list of `account` for draft `id`: the draft, nil when
    /// the last page (or the page limit) came without it, or the error.
    private func findDraft(
        _ account: AccountID, _ id: DraftID, cursor: String?, page: Int,
        _ done: @escaping @MainActor (Result<Draft?, any Error>) -> Void
    ) {
        let params = DraftListParams(accountId: account, page: Page(cursor: cursor, limit: Self.draftListPageLimit))
        mailbox.perform(API.DraftList.self, params) { [weak self] outcome in
            guard let self else { return }
            switch outcome {
            case .failure(let err):
                done(.failure(err))
            case .success(let res):
                if let d = res.drafts.first(where: { $0.id == id }) {
                    done(.success(d))
                } else if let next = res.page.nextCursor, !next.isEmpty, next != cursor, page + 1 < Self.draftListMaxPages {
                    self.findDraft(account, id, cursor: next, page: page + 1, done)
                } else {
                    done(.success(nil))
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
