// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The conversation view of the reading pane, the controller half: which
// conversation the pane shows, its model (`Conversation`, the port of
// ui/internal/conversation), the members' bodies as the cards ask for them,
// and the model kept in step with the list while it is shown. No AppKit:
// the pane (ConversationViewController) lays the cards out and tells this
// controller which of them are near enough to need a body.
//
// Swift-first: the GTK reading pane follows (ui/internal/window/window.go
// shows, and marks read, the newest folder member of a conversation row
// today); Windows ports this after it.

extension ListRow {
    /// A folded conversation row of the grouped list whose selection shows
    /// the whole conversation in the reading pane: a conversation row (not
    /// a member row, not a single-message row) with two or more members in
    /// the folder (`Conversation.isConversationRow`). Every other row shows
    /// its message alone.
    public var showsConversation: Bool {
        guard thread, !member, key.thread != nil, let summary else { return false }
        return Conversation.isConversationRow(summary)
    }
}

/// Which conversation the reading pane shows and what of it is loaded. The
/// members come from the list controller (`ensureMembers`: thread.get
/// scoped to the listed folder, shared with the unfolded row and the
/// actions), the bodies from the message cache, one card at a time and only
/// for the cards the pane asks for (`needsBody`): message.body alone, and
/// message.get too only for a card that needs what it adds to the summary
/// (attachments, the Cc of the recipients' disclosure). The entries of the
/// cards are held here (`loaded`), so a long conversation does not lose
/// them to the cache's caps while it is shown.
///
/// The mark-as-read timer is the list controller's (`announceSelection`
/// arms it for `Conversation.Model.markRead`); this controller only exposes
/// the model.
@MainActor
public final class ConversationController {
    /// What changed, for the pane.
    public enum Change: Sendable, Equatable {
        /// A conversation was selected; its members are on their way
        /// (`model` is nil).
        case loading
        /// The model was built anew: the pane lays the stack out and
        /// scrolls to `model.scrollTo`.
        case opened
        /// The shown conversation changed (a member arrived or went, its
        /// flags or issue changed): the pane reconciles its cards by id and
        /// keeps what the user reads in place.
        case updated
        /// Nothing is shown any more.
        case cleared
    }

    public let list: ListController
    public let cache: MessageCache

    /// The conversation on display; nil when the pane shows something else.
    public private(set) var thread: ThreadID?
    /// Its model; nil while the members are on their way.
    public private(set) var model: Conversation.Model?
    /// The cache entries of the cards whose body was asked for, by member.
    public private(set) var loaded: [MessageID: LoadedMessage] = [:]

    /// Called after every change of `thread` or `model`.
    public var onChange: (@MainActor (Change) -> Void)?
    /// A card's entry has news (a half of it arrived through `needsBody`);
    /// the entry is in `loaded`. The cache's own fan-out (`onLoaded`) tells
    /// the pane about later news, such as remote images.
    public var onLoaded: (@MainActor (MessageID, LoadedMessage) -> Void)?

    /// The held entries' bodies beyond which the entries of cards the pane
    /// does not keep near are let go (`trim`).
    public nonisolated static let heldBytes = 64 << 20

    /// The folder members the model was last built or merged from.
    private var members: [MessageSummary] = []
    /// The members whose entry was asked for and has not settled, and
    /// whether message.get was part of the request.
    private var pending: [MessageID: Bool] = [:]
    /// The conversation's summary as last known (the row's, then
    /// thread.get's).
    private var summary: ThreadSummary?
    /// Bumped with every selection: a late answer for a conversation left
    /// meanwhile is dropped.
    private var gen: UInt64 = 0
    /// Bumped by `refresh`: a body asked for before the daemon rebuilt the
    /// conversation's messages is dropped when it arrives, so that it
    /// cannot land in `loaded` beside the fresh one.
    private var bodyGen: UInt64 = 0

    /// Installs itself as the list controller's `onThreadMembersChanged`
    /// and `onConversationChanged` (a handler installed before keeps
    /// running first).
    public init(list: ListController, cache: MessageCache) {
        self.list = list
        self.cache = cache
        let previous = list.onThreadMembersChanged
        list.onThreadMembersChanged = { [weak self] tid in
            previous?(tid)
            self?.membersChanged(tid)
        }
        let previousChanged = list.onConversationChanged
        list.onConversationChanged = { [weak self] tid in
            previousChanged?(tid)
            self?.refresh(tid)
        }
    }

    // MARK: Selection

    /// The list's selection changed (`ListController.onSelectedRowChanged`):
    /// a conversation row (`ListRow.showsConversation`) is shown here, and
    /// true is returned; anything else clears the conversation and returns
    /// false (the pane shows the row's message, or its empty page). The
    /// same conversation announced again keeps what is shown.
    @discardableResult
    public func show(_ row: ListRow?) -> Bool {
        guard let row, row.showsConversation, let tid = row.key.thread else {
            clear()
            return false
        }
        if tid == thread {
            if let s = row.summary {
                summary = s
            }
            return true
        }
        gen += 1
        thread = tid
        model = nil
        members = []
        loaded = [:]
        pending = [:]
        summary = row.summary
        onChange?(.loading)
        requestMembers(tid)
        return true
    }

    /// Shows nothing (another kind of row, or none, is selected).
    public func clear() {
        gen += 1
        let had = thread != nil
        thread = nil
        model = nil
        members = []
        loaded = [:]
        pending = [:]
        summary = nil
        if had {
            onChange?(.cleared)
        }
    }

    /// Asks the list for the folder members of `tid`; they arrive through
    /// `membersChanged`, a failure builds from what the listing told
    /// (`buildFromListing`).
    private func requestMembers(_ tid: ThreadID) {
        let g = gen
        list.ensureMembers(tid, { [weak self] in
            guard let self, self.gen == g else { return }
            self.membersChanged(tid)
        }, failed: { [weak self] in
            guard let self, self.gen == g, self.thread == tid, self.model == nil else { return }
            self.buildFromListing(tid)
        })
    }

    /// thread.get failed (the list has said why): the conversation as the
    /// listing knows it, its newest member, with the row of the older ones
    /// on top.
    private func buildFromListing(_ tid: ThreadID) {
        guard let t = summary else { return }
        let known = list.mailbox.model.members[tid]?.list ?? [t.latest]
        members = known
        model = Conversation.build(t, known, account: account(t.accountId))
        onChange?(.opened)
    }

    // MARK: Members

    /// The list's model changed the members of conversation `tid`
    /// (`ListController.onThreadMembersChanged`), or they arrived: the model
    /// is built the first time, then kept in step by merging what changed
    /// and removing what went (`Conversation.merge`, `remove`). Members the
    /// list lost to a reload are asked for again.
    public func membersChanged(_ tid: ThreadID) {
        guard tid == thread, let mem = list.mailbox.model.members[tid] else { return }
        if !mem.complete {
            requestMembers(tid)
            return
        }
        if let t = list.row(for: ListKey(thread: tid))?.summary {
            summary = t
        }
        guard let t = summary else { return }
        // The account tells the user's own mail (`Conversation.Item.mine`).
        let own = account(t.accountId)
        guard let current = model else {
            members = mem.list
            model = Conversation.build(t, mem.list, account: own)
            onChange?(.opened)
            return
        }
        var m = current
        var before: [MessageID: MessageSummary] = [:]
        for s in members where before[s.id] == nil {
            before[s.id] = s
        }
        let now = Set(mem.list.map(\.id))
        for s in mem.list where before[s.id] != s {
            m = Conversation.merge(m, s, account: own)
        }
        for s in members where !now.contains(s.id) {
            m = Conversation.remove(m, s.id)
            loaded[s.id] = nil
            pending[s.id] = nil
        }
        members = mem.list
        if m.items.isEmpty, m.earlier > 0 {
            // Every shown member went while older ones are left out: the
            // conversation is loaded again.
            model = nil
            onChange?(.loading)
            list.refreshMembers(tid)
            requestMembers(tid)
            return
        }
        guard m != current else { return }
        model = m
        onChange?(.updated)
    }

    /// The daemon rebuilt the messages of the shown conversation in place
    /// (notify.messagesChanged: a Jira pass with other rendering settings, a
    /// comment edited or re-attributed, the issue renamed; docs/api.md §5),
    /// which the list reports through `onConversationChanged` after it
    /// forgot the folder members and before it lists the folder again. The
    /// held entries are let go (the cache let go of its own) and the pane is
    /// told the conversation changed, so it asks for the bodies of the cards
    /// near the viewport again (`needsBody`, which finds nothing held and
    /// fetches); the cards keep what they show until the fresh body
    /// arrives. The members come back through `membersChanged` once the
    /// reload asked thread.get for them, and merge into the model.
    public func refresh(_ tid: ThreadID) {
        guard tid == thread else { return }
        loaded = [:]
        pending = [:]
        bodyGen += 1
        if model != nil {
            onChange?(.updated)
        }
    }

    // MARK: Bodies

    /// The card of member `id` is near the viewport: its body is fetched
    /// unless held already (message.body; message.get as well when
    /// `details`, or when the message has attachments, whose chips need
    /// it). An event has no body. The entry is held in `loaded` and
    /// announced through `onLoaded` whenever a half of it arrives.
    public func needsBody(_ id: MessageID, details: Bool = false) {
        guard let s = member(id), !readsWithoutBody(s) else { return }
        let full = details || s.hasAttachments
        if let lm = loaded[id], lm.bodySettled, !full || lm.msg != nil {
            return
        }
        // Asked already (the pane asks on every scroll): the answer comes.
        if let asked = pending[id], asked || !full {
            return
        }
        pending[id] = full
        let g = gen
        let bg = bodyGen
        let then: MessageCache.Waiter = { [weak self] lm in
            guard let self, self.gen == g, self.bodyGen == bg, self.member(id) != nil else { return }
            if !lm.getting, !lm.fetching {
                self.pending[id] = nil
            }
            self.loaded[id] = lm
            self.onLoaded?(id, lm)
        }
        if full {
            cache.fetch(s, then)
        } else {
            cache.fetchBody(s, then)
        }
    }

    /// The cache has news about member `id` (its `onLoaded` fan-out: the
    /// remote images, a download): the card's entry is that one from now
    /// on. An id that is not shown is ignored.
    public func adopt(_ id: MessageID, _ lm: LoadedMessage) {
        guard member(id) != nil else { return }
        loaded[id] = lm
    }

    /// Lets go of the entries of the cards not in `near` while the held
    /// bodies are above `budget` bytes, largest first: their cards keep what
    /// they show and ask again when they come near.
    public func trim(keeping near: Set<MessageID>, budget: Int = ConversationController.heldBytes) {
        var total = loaded.values.reduce(0) { $0 + $1.size }
        guard total > budget else { return }
        let far = loaded.filter { !near.contains($0.key) }.sorted { $0.value.size > $1.value.size }
        for (id, lm) in far {
            if total <= budget {
                break
            }
            total -= lm.size
            loaded[id] = nil
        }
    }

    // MARK: Lookup

    /// The shown member `id`, as the model has it now.
    public func member(_ id: MessageID) -> MessageSummary? {
        guard let m = model else { return nil }
        let i = m.index(id)
        return i >= 0 ? m.items[i].message : nil
    }

    /// The account of the shown conversation's members.
    public func account(_ id: AccountID) -> Account? {
        list.mailbox.model.account(id)
    }

    /// The buttons the card of `s` offers (`Conversation.cardActions`):
    /// none for an account the window does not know.
    public func actions(for s: MessageSummary) -> Capabilities.Actions {
        let m = list.mailbox.model
        guard let a = m.account(s.accountId) else { return Capabilities.Actions() }
        return Conversation.cardActions(a, s, composeAccount: !Capabilities.forwardAccounts(m.accounts).isEmpty)
    }

    /// The site of the Jira account `id` ("" when unknown or not Jira):
    /// the only site the issue card's key may open.
    public func issueSite(_ id: AccountID) -> String {
        account(id)?.config.jira?.siteUrl ?? ""
    }
}
