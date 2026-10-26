// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// ui/internal/quickreply/controller.go: the quick-reply bar of Mail's
// reading pane (docs/plans/chat-view.md §10, §11 and §12) without its
// widgets: what the bar shows (its target, modes and text, per
// conversation) and what its buttons do — Send through the daemon's usual
// way, Open in a Window and Forward… through the compose window. The bar
// stands under the pane's page of a single message and under a
// conversation shown as chat (never under the timeline, the board or a
// message window); which of them shows is the host's to say (`setShown`),
// and a single message comes as a model of one member
// (`Conversation.singleModel`), so the rules are the same for both. A
// single message's thread is its conversation's: the text typed under the
// message is the text under its conversation. A thread id is unique only
// within its account (an issue's jira:<id> repeats on another site, Graph's
// conversationId is the server's), so the state is kept per account and
// thread (`Key`), never per thread alone: a search over all accounts may
// show the same thread id of two accounts one after the other.
//
// The rules of the bar's content are `Conversation`'s (`quickReplyTarget`,
// `quickReplyModes`, `quickReplyHTML`, `quickReplyBlank`,
// `quickReplyBody`); the texts are its too and the view's (L10n): this file
// has none, the host says what happened (`sent`, `failed`) and the view says
// it in words. The window gives it a `QuickReplyHost` over the main
// window's client and its actions (MalachiMail).
//
// Threads: everything runs on the main actor. The calls to the daemon run in
// a `Task` of their own and their outcome comes back through
// `QuickReplyHost.post`, as Go's goroutine does through Host.Post.

/// quickreply.Host: what the controller needs of the window.
@MainActor
public protocol QuickReplyHost: AnyObject {
    /// One call to the daemon (the main window's client, as
    /// `MailboxController.perform` makes it), a server-side error as
    /// `RPCError`.
    func call<M: RPCMethod>(_ method: M.Type, _ params: M.Params, timeout: Duration) async throws -> M.Result
    /// Runs `f` on the main actor, after what was posted before.
    func post(_ f: @escaping @MainActor () -> Void)
    /// The line above the quote of a reply in `mode` to message `id`, as the
    /// compose window computes it (`attribution(kind:source:)` over
    /// `composeSource(summary:loaded:)` of what the window knows of the
    /// message); "" for none.
    func attribution(mode: Conversation.QuickReplyMode, id: MessageID) -> String
    /// Runs before draft.create of a reply (never of a comment): the window
    /// downloads message `id` first when its compose window would
    /// (`replyNeedsDownload`: pictures of the quote kept on the mail server
    /// only), ignores the outcome as the window does, and calls `done` on
    /// the main actor. Without a reason to download it calls `done` at once.
    func beforeCreate(id: MessageID, done: @escaping @MainActor () -> Void)
    /// Opens the compose window with a reply, reply all or forward (`mode`)
    /// of message `id` — a comment when the account comments — prepared by
    /// draft.create as the window always is, with `ownHTML` (safe HTML:
    /// `Conversation.quickReplyHTML`) in its editor as the user's own text,
    /// marked as an edit (`ActionsController.openCompose(_:_:from:ownHTML:opened:)`).
    /// `opened` is called on the main actor once a window really opened,
    /// and never when none does (one is being prepared for the message
    /// already, the message is unknown, or a comment's draft.create failed).
    func openCompose(mode: Conversation.QuickReplyMode, id: MessageID, ownHTML: String, opened: @escaping @MainActor () -> Void)
    /// A quick reply went to the outbox (`comment`: a comment on an issue);
    /// the window shows the compose window's toast.
    func sent(comment: Bool)
    /// Send did not go through (`error` from draft.create, draft.save or
    /// message.send); the window shows the compose window's toast for a
    /// failed send. The text stays in the bar.
    func failed(_ error: any Error)
}

/// quickreply.State: what the bar shows, for the conversation shown
/// (`account`, `thread`). Go's zero Item is nil.
public struct QuickReplyState: Sendable, Equatable {
    /// The conversation shown, a thread of an account; both "" for none.
    public var account: AccountID = ""
    public var thread: ThreadID = ""
    /// The host shows the bar's place (`QuickReplyController.setShown`: a
    /// single message's page or a conversation's chat), the conversation
    /// has a target and its account answers it (Reply). The bar is hidden
    /// otherwise.
    public var visible = false
    /// The message a quick reply answers (`Conversation.quickReplyTarget`);
    /// nil without one. Its `sender` names it in the mode button's tooltip.
    public var target: Conversation.Item?
    /// The mode menu's entries (`Conversation.quickReplyModes`); `.forward`
    /// among them opens the window (`QuickReplyController.forward`).
    public var modes: [Conversation.QuickReplyMode] = []
    /// The chosen mode: `.reply` or `.replyAll`, never `.forward`; on an
    /// issue always `.reply`.
    public var mode: Conversation.QuickReplyMode = .reply
    /// What was typed in this conversation's bar.
    public var text = ""
    /// `text` has nothing to send (`Conversation.quickReplyBlank`); Send is
    /// disabled.
    public var blank = true
    /// A Send of this conversation is on its way; the bar is disabled until
    /// it ends.
    public var sending = false
    /// The reply is a comment on an issue (the placeholder and the Reply
    /// label say so).
    public var comment = false

    public init() {}
}

/// quickreply.Controller: the quick-reply bar of one main window.
@MainActor
public final class QuickReplyController {
    /// quickreply.CallTimeout: bounds each call of Send (draft.create,
    /// draft.save, message.send, draft.delete): draft.create re-reads the
    /// original and copies its pictures, which can take longer than an
    /// ordinary call. The compose window's draft.create bound.
    public static let callTimeout = RPCTimeouts.compose

    /// quickreply.key: one conversation, a thread of an account. The zero
    /// key is none.
    private struct Key: Hashable {
        var account: AccountID = ""
        var thread: ThreadID = ""
    }

    /// The bar's state of one conversation, kept in memory until the text
    /// is sent, moved into a window or cleared (Q9).
    private struct ThreadState: Equatable {
        var text = ""
        var mode: Conversation.QuickReplyMode = .reply
        var sending = false
    }

    /// Called after every change of `state` (the view reads it again), on
    /// the main actor.
    public var onChange: (@MainActor () -> Void)?

    /// The window, which owns the controller (weak: nothing happens
    /// without it).
    private weak var host: (any QuickReplyHost)?

    /// The conversation shown (a single message's thread, with its
    /// account); `paneShown` says the host shows the bar's place
    /// (`setShown`).
    private var shown = Key()
    private var paneShown = false
    /// What `update` learnt of the shown conversation.
    private var target: Conversation.Item?
    private var modes: [Conversation.QuickReplyMode] = []
    private var reply = false
    private var comment = false
    private var account: Account?

    private var threads: [Key: ThreadState] = [:]

    /// quickreply.New: a controller over `host`, which owns it.
    public init(host: any QuickReplyHost) {
        self.host = host
    }

    /// The threads whose state is kept, of any account (tests: a
    /// conversation emptied is forgotten).
    var keptThreads: Set<ThreadID> { Set(threads.keys.map(\.thread)) }

    /// Whether the state of thread `t` of `account` is kept (tests).
    func isKept(_ account: AccountID?, _ t: ThreadID) -> Bool {
        threads[Key(account: account ?? "", thread: t)] != nil
    }

    /// Show: which conversation the pane shows now: thread `t` of `account`
    /// (for a single message its thread and account; nil or "" and "" for
    /// none, and a thread "" is none whatever the account). Its target is
    /// unknown until the next `update`; the text and mode of every
    /// conversation stay.
    public func show(_ account: AccountID?, _ t: ThreadID) {
        var k = Key(account: account ?? "", thread: t)
        if t.rawValue.isEmpty {
            k = Key()
        }
        if k == shown {
            return
        }
        shown = k
        target = nil
        modes = []
        reply = false
        comment = false
        self.account = nil
        changed()
    }

    /// Update: takes the shown conversation's model `m` (a single message's:
    /// `Conversation.singleModel`), the actions of its target's card (`Conversation.cardActions` for the target) and its
    /// account. A model of another conversation (`m.thread` neither "" nor
    /// the one shown) shows that one, of `account`, first, as `show` does; a
    /// conversation shown without its account (`show` with nil or "") takes
    /// `account`'s. A model without a target (the empty model) hides the bar
    /// until the next one and keeps the text: the host gives it while the
    /// conversation's members are on their way, so that the bar never
    /// answers what was shown before.
    public func update(_ m: Conversation.Model, actions: Capabilities.Actions, account: Account?) {
        if !m.thread.rawValue.isEmpty, m.thread != shown.thread {
            shown = Key(account: account?.id ?? "", thread: m.thread)
        } else if !shown.thread.rawValue.isEmpty, shown.account.rawValue.isEmpty {
            shown.account = account?.id ?? ""
        }
        target = Conversation.quickReplyTarget(m)
        modes = Conversation.quickReplyModes(actions)
        reply = actions.reply
        comment = actions.comment
        self.account = account
        changed()
    }

    /// SetShown: whether the pane shows a place of the bar: the page of a
    /// single message whose folder can be answered from (not the Outbox,
    /// not Drafts), or a conversation in the chat layout. The empty page,
    /// the page of several selected messages and the timeline have no bar;
    /// neither has the board. Hiding keeps the text and mode of every
    /// conversation, and a Send on its way ends as usual.
    public func setShown(_ shown: Bool) {
        if shown == paneShown {
            return
        }
        paneShown = shown
        changed()
    }

    /// SetText: what the field holds now, for the conversation shown.
    public func setText(_ text: String) {
        if shown.thread.rawValue.isEmpty {
            return
        }
        if threads[shown] == nil, text.isEmpty {
            return
        }
        var st = threads[shown] ?? ThreadState()
        if st.text == text {
            return
        }
        st.text = text
        threads[shown] = st
        prune(shown)
        changed()
    }

    /// SetMode: chooses `.reply` or `.replyAll` among the modes offered for
    /// the conversation shown; anything else (`.forward`, a mode not
    /// offered) is ignored.
    public func setMode(_ mode: Conversation.QuickReplyMode) {
        if shown.thread.rawValue.isEmpty || mode == .forward || !modes.contains(mode) {
            return
        }
        // A conversation without state has `.reply`: choosing it changes
        // nothing (Go keeps an empty entry then; here none is made).
        var st = threads[shown] ?? ThreadState()
        if st.mode == mode {
            return
        }
        st.mode = mode
        threads[shown] = st
        prune(shown)
        changed()
    }

    /// State: what the bar shows now.
    public var state: QuickReplyState {
        var s = QuickReplyState()
        s.account = shown.account
        s.thread = shown.thread
        s.target = target
        s.modes = modes
        s.comment = comment
        s.mode = currentMode
        if !shown.thread.rawValue.isEmpty, let st = threads[shown] {
            s.text = st.text
            s.sending = st.sending
        }
        s.blank = Conversation.quickReplyBlank(s.text)
        s.visible = visible
        return s
    }

    /// The bar shows (`QuickReplyState.visible`).
    private var visible: Bool {
        paneShown && !shown.thread.rawValue.isEmpty && target != nil && reply
    }

    /// The chosen mode of the conversation shown when it is still offered,
    /// else the first mode offered that is not `.forward`, else `.reply`; on
    /// an issue always `.reply`.
    private var currentMode: Conversation.QuickReplyMode {
        if comment {
            return .reply
        }
        if let st = threads[shown], modes.contains(st.mode) {
            return st.mode
        }
        return modes.first { $0 != .forward } ?? .reply
    }

    /// Send: sends the text of the conversation shown as a reply (or
    /// comment) to its target: draft.create on the target (after
    /// `QuickReplyHost.beforeCreate`, never for a comment) with the
    /// attribution the window would use, one draft.save of its draft with
    /// the typed text above the original quoted
    /// (`Conversation.quickReplyBody`), and message.send. Success clears the
    /// text and says `QuickReplyHost.sent`; a failure says
    /// `QuickReplyHost.failed` and keeps the text, and a failed message.send
    /// first deletes the saved draft (draft.delete, its error ignored) so
    /// that no draft the user never saw stays behind. The outcome goes to
    /// the conversation Send started in (its account and thread), whichever
    /// is shown by then. Nothing
    /// happens when the bar is hidden, the text is blank or a Send of this
    /// conversation is on its way.
    public func send() {
        guard let host, visible, var st = threads[shown], !st.sending, !Conversation.quickReplyBlank(st.text),
              let target, let message = target.message else { return }
        let k = shown
        let text = st.text
        let comment = comment
        let mode = currentMode
        var acc = message.accountId
        if acc.rawValue.isEmpty {
            acc = account?.id ?? ""
        }
        let id = message.id
        st.sending = true
        threads[k] = st
        changed()

        // Each closure reads the host from the controller again instead of
        // capturing the one above: a non-Sendable host captured by `start`
        // and again by its Task is "sending" to Swift 6.3 (Xcode 26.6).
        let start: @MainActor () -> Void = { [weak self] in
            guard let host = self?.host else { return }
            var params = DraftCreateParams(accountId: acc, mode: mode.composeMode, messageId: id)
            if !comment {
                let line = host.attribution(mode: mode, id: id)
                params.attribution = line.isEmpty ? nil : line
            }
            let p = params
            Task { @MainActor [weak self] in
                guard let host = self?.host else { return }
                let err = await Self.deliver(host, p, text: text)
                host.post {
                    self?.finish(k, text: text, comment: comment, error: err)
                }
            }
        }
        if comment {
            start()
            return
        }
        host.beforeCreate(id: id, done: start)
    }

    /// Send's calls: draft.create, draft.save, message.send, and
    /// draft.delete after a failed message.send; nil when all went through.
    private static func deliver(_ h: any QuickReplyHost, _ params: DraftCreateParams, text: String) async -> (any Error)? {
        let created: DraftCreateResult
        do {
            created = try await h.call(API.DraftCreate.self, params, timeout: callTimeout)
        } catch {
            return error
        }
        var d = saveDraft(mode: params.mode, text: text, draft: created.draft)
        if d.accountId.rawValue.isEmpty {
            d.accountId = params.accountId
        }
        let saved: DraftSaveResult
        do {
            saved = try await h.call(API.DraftSave.self, DraftSaveParams(draft: d), timeout: callTimeout)
        } catch {
            return error
        }
        do {
            _ = try await h.call(
                API.MessageSend.self,
                MessageSendParams(accountId: d.accountId, draftId: saved.draftId, version: saved.version),
                timeout: callTimeout)
            return nil
        } catch {
            // An unknown draft is no error of draft.delete, so a
            // message.send that queued the message and then timed out leaves
            // nothing; any other failure leaves a draft the conversation
            // shows.
            _ = try? await h.call(
                API.DraftDelete.self, DraftDeleteParams(accountId: d.accountId, draftId: saved.draftId),
                timeout: callTimeout)
            return error
        }
    }

    /// quickreply.SaveDraft: the draft a quick reply saves with `text` typed
    /// in `mode` on `draft`, draft.create's result: the shape of the compose
    /// window's own save (`ComposeDraftController` `build`) — the template's
    /// identity, recipients and subject, the body
    /// `Conversation.quickReplyBody` (the typed text above the original
    /// quoted; a comment's text alone), the text as typed (the daemon
    /// derives its own from the HTML), the template's attachments by id
    /// (else the quote's pictures would be lost; only `id` is read), and a
    /// comment always public (the bar has no visibility; the window has).
    /// `to` is never nil on the wire (`Draft.to` is not optional here); a
    /// draft without attachments has nil, as Go's nil slice.
    public static func saveDraft(mode: ComposeMode, text: String, draft d: Draft) -> Draft {
        var out = Draft(
            id: d.id, accountId: d.accountId, version: d.version, to: d.to, cc: d.cc, bcc: d.bcc,
            subject: d.subject, textBody: text, htmlBody: Conversation.quickReplyBody(mode: mode, own: text, draft: d),
            inReplyTo: d.inReplyTo, forwarding: d.forwarding, replaces: d.replaces
        )
        if let c = d.comment {
            out.comment = DraftComment(issue: c.issue, visibility: .public)
        }
        if let list = d.attachments, !list.isEmpty {
            out.attachments = list.map {
                DraftAttachment(id: $0.id, filename: "", contentType: "", size: 0, inline: false)
            }
        }
        return out
    }

    /// Ends a Send of conversation `k` whose text was `text`.
    private func finish(_ k: Key, text: String, comment: Bool, error: (any Error)?) {
        var st = threads[k] ?? ThreadState()
        st.sending = false
        if error == nil {
            // What was typed meanwhile (the field is disabled while sending,
            // but a view may still pass a change) stays.
            if st.text == text {
                st.text = ""
            }
        }
        threads[k] = st
        if let error {
            host?.failed(error)
        } else {
            host?.sent(comment: comment)
        }
        prune(k)
        changed()
    }

    /// OpenWindow: moves the text of the conversation shown into the compose
    /// window: a reply in the chosen mode (a comment on an issue) to the
    /// target, prepared by the daemon as the window always is, with the text
    /// as the user's own. The bar's text is cleared once the window opened
    /// (`QuickReplyHost.openCompose`'s `opened`), and only if it is
    /// unchanged by then. No draft is saved here; the window's own autosave
    /// does that.
    public func openWindow() {
        open(currentMode)
    }

    /// Forward: the mode menu's Forward…: the compose window with a forward
    /// of the target and the text of the conversation shown above it, as
    /// `openWindow`. Nothing happens when the account does not forward.
    public func forward() {
        guard modes.contains(.forward) else { return }
        open(.forward)
    }

    private func open(_ mode: Conversation.QuickReplyMode) {
        guard let host, visible, let id = target?.message?.id else { return }
        let k = shown
        var text = ""
        if let st = threads[k] {
            if st.sending {
                return
            }
            text = st.text
        }
        host.openCompose(mode: mode, id: id, ownHTML: Conversation.quickReplyHTML(text)) { [weak self] in
            guard let self, var st = self.threads[k], st.text == text, !text.isEmpty else { return }
            st.text = ""
            self.threads[k] = st
            self.prune(k)
            self.changed()
        }
    }

    /// Forgets a conversation whose state is the zero one.
    private func prune(_ k: Key) {
        if let st = threads[k], st == ThreadState() {
            threads[k] = nil
        }
    }

    private func changed() {
        onChange?()
    }
}
