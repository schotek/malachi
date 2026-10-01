// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/conversation/conversation.go: the view logic of a whole
// conversation in the reading pane. Selecting a folded conversation row of
// the grouped list (two or more messages: members in the folder and the
// user's replies in Sent; a Jira folder is always grouped) shows every
// member the folder holds, and the user's replies the folder lacks, stacked
// oldest first with full bodies, instead of only the newest one; a member
// row and a single-message row keep the single-message view.
//
// The port turns the answer of a folder-scoped thread.get with withSent
// (the summary, the members and the sent ones, each oldest first, at most
// `API.Limits.maxThreadMessages`, the newest) into the items the pane
// stacks: a card per message (a mail message, the user's reply in Sent, or
// the description or a comment of an issue, with the Jira badges), a
// compact row per status or assignee change of an issue, and on top a row
// that says how many older members are left out. It also picks the one
// member opening the conversation marks read (the newest folder member that
// is not an event) and the item the pane scrolls to (the newest), keeps the
// items in step when a member arrives or goes while the conversation is
// shown, and decides which cards are folded to their header
// (ConversationFold.swift, fold.go).
//
// The pane stacks native cards, each body in its own locked view, never one
// composed document: a message's CSS could restyle or forge the headers of
// the others. Headers are plain text only; every string here that comes
// from a message is attacker-controlled.
//
// Go's Translator is the gettext shim here (L10n, keys = the GTK msgids),
// as in JiraView.swift. Where Swift cannot follow Go literally: an empty Go
// id ("") is nil here (`Item.message` of the truncated row, `markRead`),
// and the account of `build`, `merge` and `mergeSent` may be left out (nil: nobody's
// mail is the user's own) by a caller that reads only what does not depend
// on it, as the list controller does for the member to mark read.

import Foundation

/// The conversation package: a namespace, so the Go names map 1:1
/// (`conversation.Build` → `Conversation.build`).
public enum Conversation {
    /// conversation.IsConversationRow: a listed conversation whose
    /// selection shows the whole conversation: two or more messages, the
    /// members in the folder and the user's replies in Sent it lacks
    /// (`ThreadSummary.sentCount`) together, so that a message and the
    /// user's reply to it are a conversation. The outbox is never grouped
    /// (the caller's rule, as for the list).
    public static func isConversationRow(_ t: ThreadSummary) -> Bool {
        t.messageCount + max(t.sentCount, 0) >= 2
    }

    /// conversation.ItemKind: what an item of the stack is.
    public enum ItemKind: Sendable, Equatable {
        /// A message card: a mail message, or the description or a comment
        /// of an issue. Its body is shown in full.
        case message
        /// A compact row of an issue's status or assignee changes: native
        /// text only (never a web view), never unread, never marked read.
        case event
        /// The row on top that says how many older members are left out
        /// (thread.get returns the newest `API.Limits.maxThreadMessages`).
        case truncated
    }

    /// conversation.Item: one entry of the stack.
    public struct Item: Sendable, Equatable {
        public var kind: ItemKind
        /// The member (`.message` and `.event`); nil for `.truncated`.
        public var message: MessageSummary?
        /// The name the card's compact header shows: the display name (else
        /// the address) of the first sender that has one, cleaned for one
        /// line (`Jira.clean`); "" when there is none.
        public var sender = ""
        /// Marks an unread message card; an event and a sent card are never
        /// unread, whatever their flags.
        public var unread = false
        /// Marks a card of the user's reply in a sent folder that the
        /// folder lacks (thread.get's `sent`): not a member of the folder,
        /// so never marked read, never counted as a member and never among
        /// the messages the conversation's actions take; it starts folded
        /// (`defaultFolds`).
        public var sent = false
        /// A member the account's own user wrote: the pane tints its avatar
        /// with the accent colour and changes nothing else (the name stays
        /// the sender's). An item of an issue says so itself
        /// (`MessageIssue.mine`), and a comment an integration relayed
        /// (`via`) is never the user's, whatever the site says; a mail
        /// message is the user's when the address of its first sender is
        /// the account's, compared without case and surrounding space (no
        /// sender, no address or an account without one: not the user's).
        /// An address is what the sender wrote: a forged From looks like
        /// the user's own message.
        public var mine = false
        /// An internal comment of a service-desk issue, which shows the
        /// badge `internalLabel` ("" when not internal). `via` names the
        /// integration that posted a comment for its author ("via Issue
        /// Sync"); `edited` is the badge of a comment changed after it was
        /// posted. "" when not; always "" for mail. The texts are those of
        /// `Jira.issueCard`.
        public var `internal` = false
        public var internalLabel = ""
        public var via = ""
        public var edited = ""
        /// The sentences of an event, one per change (`Jira.eventLines`);
        /// `eventText` is them on one line (`Jira.eventText`), for an
        /// accessible name. Empty for other kinds.
        public var eventLines: [String] = []
        public var eventText = ""
        /// The sentence of `.truncated` ("112 earlier messages are not
        /// shown"); "" for other kinds.
        public var text = ""

        public init(kind: ItemKind, message: MessageSummary? = nil) {
            self.kind = kind
            self.message = message
        }

        /// The member's id; nil for the truncated row.
        public var id: MessageID? {
            kind == .truncated ? nil : message?.id
        }
    }

    /// conversation.Model: what the reading pane shows of one conversation.
    public struct Model: Sendable, Equatable {
        /// The conversation; `merge` ignores a message of another one.
        public var thread: ThreadID
        /// The stack, oldest first by (date, id): a `.truncated` item on top
        /// when `earlier` > 0, then the members and the sent cards. Empty
        /// when the conversation has no member to show (sent cards alone
        /// are no conversation of the folder): the pane shows its empty
        /// page then, or,
        /// when `remove` took the last shown member and `earlier` > 0, loads
        /// the conversation again (`build` never leaves `earlier` > 0
        /// without items).
        public var items: [Item] = []
        /// The issue card shown once above the stack of a Jira conversation
        /// (`Jira.issueCard` without an item: no badges); nil for mail and
        /// for an empty model.
        public var issue: Jira.Card?
        /// How many older members of the conversation in the folder are not
        /// in `items` (sent cards older than the oldest member shown are
        /// left out then, and not counted).
        public var earlier = 0
        /// The member opening the conversation marks read (after the usual
        /// delay): the newest message card that is neither a queued message
        /// of the outbox nor a sent card, when it is unread; nil when it is
        /// read or there is none. Older unread members stay unread, and events are never
        /// marked. The model recomputes it after every change; a client acts
        /// on it when the conversation is opened (and may when a member
        /// arrives while the pane shows the end), never after a flag
        /// change: a member the user marked unread stays unread.
        public var markRead: MessageID?
        /// The index into `items` the pane scrolls to when it opens the
        /// conversation: the newest item; -1 when `items` is empty. Whether
        /// an arrival scrolls (only when the pane was at the end) is the
        /// client's decision.
        public var scrollTo = -1

        public init(thread: ThreadID) {
            self.thread = thread
        }

        /// conversation.Model.Index: the position in `items` of the member
        /// `id`; -1 when it is not shown.
        public func index(_ id: MessageID?) -> Int {
            guard let id, !id.rawValue.isEmpty else { return -1 }
            return items.firstIndex { $0.kind != .truncated && $0.message?.id == id } ?? -1
        }
    }

    /// conversation.Build: the model of a conversation from a
    /// folder-scoped thread.get: `thread` is its summary, `members` its
    /// folder members, `sent` the user's replies in Sent the folder lacks
    /// (thread.get's `sent` with `withSent`; empty without), `account` the
    /// account they belong to (its address tells the user's own mail,
    /// `Item.mine`). The members are ordered oldest first by (date, id)
    /// whatever order they come in; a member without an id and a repeated
    /// id (the first is kept) are dropped; beyond
    /// `API.Limits.maxThreadMessages` only the newest are kept. An event
    /// whose changes this client does not know at all (the field is an open
    /// enum) is left out, as `Jira.eventLines` leaves out such a change.
    /// `earlier` counts the members of `thread.messageCount` that are not
    /// among the members. The sent ones are put among the members by (date,
    /// id) as cards with `sent` set, under the same rules (no id, a
    /// repeated id, beyond `API.Limits.maxThreadMessages`), and without an
    /// id that is a member's (the member wins) or an event; when older
    /// members are left out (`earlier` > 0), a sent one older than the
    /// oldest member shown is left out too. The issue card is the thread's
    /// issue, else the newest member's. No member to show makes an empty
    /// model with `earlier` 0 (the conversation left the folder: nothing to
    /// load again), whatever sent there is.
    public static func build(
        _ thread: ThreadSummary, _ members: [MessageSummary], sent: [MessageSummary] = [], account: Account? = nil
    ) -> Model {
        var m = Model(thread: thread.id)
        let list = sortedUnique(members)
        var shown = list[...]
        if shown.count > API.Limits.maxThreadMessages {
            shown = shown.suffix(API.Limits.maxThreadMessages)
        }
        m.earlier = max(thread.messageCount, list.count) - shown.count
        var items: [Item] = []
        items.reserveCapacity(shown.count)
        for s in shown {
            if let it = memberItem(s, account) {
                items.append(it)
            }
        }
        if items.isEmpty {
            return Model(thread: thread.id)
        }
        items = withSent(items, sent, list, cut: m.earlier > 0, account)
        var info = thread.issue
        if info == nil {
            info = shown.last { $0.issue != nil }?.issue?.info
        }
        let card = info.map { Jira.issueCard($0) }
        return assemble(m, items, card)
    }

    /// conversation.Merge: puts a member that arrived while the
    /// conversation is shown in its place by (date, id), or replaces the
    /// shown member with the same id by `arrived` (its flags, delivery state
    /// or issue changed) and moves it if its date changed. A message without
    /// an id, or of another conversation (a thread id that differs), leaves
    /// the model as it is; which folder it is in is the caller's check, as
    /// for the list. A member with an issue refreshes the issue card (an
    /// event has changed the status). A sent card with the member's id
    /// gives way to it. `account` is the conversation's account, as for
    /// `build`. `markRead` and `scrollTo` follow the rules of `build`; the
    /// model given is not modified.
    public static func merge(_ m: Model, _ arrived: MessageSummary, account: Account? = nil) -> Model {
        if arrived.id.rawValue.isEmpty {
            return m
        }
        if !m.thread.rawValue.isEmpty, let t = arrived.threadId, !t.rawValue.isEmpty, t != m.thread {
            return m
        }
        var items: [Item] = []
        items.reserveCapacity(m.items.count + 1)
        for it in m.items where it.kind != .truncated && it.message?.id != arrived.id {
            items.append(it)
        }
        if let it = memberItem(arrived, account) {
            let at = items.firstIndex { x in x.message.map { before(arrived, $0) } ?? false } ?? items.count
            items.insert(it, at: at)
        }
        var card = m.issue
        if let issue = arrived.issue {
            card = Jira.issueCard(issue.info)
        }
        return assemble(m, items, card)
    }

    /// conversation.MergeSent: `merge` for a sent card: the user's reply in
    /// Sent that the folder lacks arrived or changed (a thread.get with
    /// `withSent` answered again). It is put in its place by (date, id) as
    /// a card with `sent` set, or replaces the sent card with its id. A
    /// message without an id, of another conversation, with the id of a
    /// member, or an event leaves the model as it is, and so does an empty
    /// model (sent cards alone are no conversation); when older members are
    /// left out (`earlier` > 0), one older than the oldest member shown is
    /// dropped. That it is in the folder's answer's `sent` (deduplicated by
    /// Message-ID there) is the caller's check. `markRead` and `scrollTo`
    /// follow the rules of `build`; the model given is not modified.
    public static func mergeSent(_ m: Model, _ arrived: MessageSummary, account: Account? = nil) -> Model {
        if arrived.id.rawValue.isEmpty {
            return m
        }
        if !m.thread.rawValue.isEmpty, let t = arrived.threadId, !t.rawValue.isEmpty, t != m.thread {
            return m
        }
        let at = m.index(arrived.id)
        if at >= 0, !m.items[at].sent {
            return m
        }
        var members: [Item] = []
        members.reserveCapacity(m.items.count)
        var sent: [MessageSummary] = []
        sent.reserveCapacity(m.items.count + 1)
        for it in m.items {
            if it.kind == .truncated || it.message?.id == arrived.id {
                continue
            }
            if it.sent {
                if let s = it.message {
                    sent.append(s)
                }
            } else {
                members.append(it)
            }
        }
        if members.isEmpty {
            return m
        }
        sent.append(arrived)
        let shown = members.compactMap(\.message)
        let items = withSent(members, sent, shown, cut: m.earlier > 0, account)
        return assemble(m, items, m.issue)
    }

    /// conversation.Remove: drops the member or sent card `id` (it was
    /// moved, deleted or left the folder or Sent). An id that is not shown
    /// leaves the model as it is. When the last shown member goes, the
    /// model becomes empty and keeps `earlier`, whatever sent cards are
    /// left: a conversation with older members is loaded again. `markRead`
    /// and `scrollTo` follow the rules of `build`; the model given is not
    /// modified.
    public static func remove(_ m: Model, _ id: MessageID) -> Model {
        let at = m.index(id)
        if at < 0 {
            return m
        }
        var items: [Item] = []
        items.reserveCapacity(m.items.count - 1)
        var members = 0
        for (i, it) in m.items.enumerated() where i != at {
            items.append(it)
            if it.kind != .truncated && !it.sent {
                members += 1
            }
        }
        if members == 0 {
            var empty = Model(thread: m.thread)
            empty.earlier = m.earlier
            return empty
        }
        var out = m
        out.items = items
        out.markRead = markRead(items)
        out.scrollTo = items.count - 1
        return out
    }

    /// conversation.CardActions: the buttons a card offers on hover: Reply
    /// (labelled Comment when `comment` is set), Reply All and Forward, as
    /// the message toolbar would offer them for this member alone
    /// (`Capabilities.available` with the member selected).
    /// `composeAccount` says some enabled account can compose
    /// (`Capabilities.Situation.composeAccount`): the forward of an issue
    /// goes out from a mail account. An event offers none; the other
    /// actions (move, trash, archive, junk) are never set here.
    public static func cardActions(_ account: Account, _ m: MessageSummary, composeAccount: Bool) -> Capabilities.Actions {
        if Jira.isEvent(m.issue) {
            return Capabilities.Actions()
        }
        let a = Capabilities.available(Capabilities.Situation(
            account: account, selected: true, outbox: m.outbox != nil, composeAccount: composeAccount))
        return Capabilities.Actions(reply: a.reply, replyAll: a.replyAll, forward: a.forward, comment: a.comment)
    }

    /// conversation.assemble: completes `m` from its member and sent items,
    /// oldest first: the row of older members on top when `earlier` > 0,
    /// the issue card, `markRead` and `scrollTo`. No member items make an
    /// empty model (`thread` and `earlier` kept).
    private static func assemble(_ m: Model, _ items: [Item], _ card: Jira.Card?) -> Model {
        var out = Model(thread: m.thread)
        out.earlier = m.earlier
        if !items.contains(where: { !$0.sent }) {
            return out
        }
        if out.earlier > 0 {
            out.items.reserveCapacity(items.count + 1)
            out.items.append(truncatedItem(out.earlier))
        }
        out.items.append(contentsOf: items)
        out.issue = card
        out.markRead = markRead(out.items)
        out.scrollTo = out.items.count - 1
        return out
    }

    /// conversation.truncatedItem: the row that says `n` older members are
    /// left out.
    private static func truncatedItem(_ n: Int) -> Item {
        var it = Item(kind: .truncated)
        // TRANSLATORS: at the top of a conversation in the reading pane; only its newest messages are shown.
        it.text = L10n.N("%d earlier message is not shown", "%d earlier messages are not shown", n)
        return it
    }

    /// conversation.memberItem: the item of member `s` of an account's
    /// conversation; nil for an event without a change this client knows.
    private static func memberItem(_ s: MessageSummary, _ account: Account?) -> Item? {
        var it = Item(kind: .message, message: s)
        it.sender = sender(s.from)
        it.mine = mine(s, account)
        if let issue = s.issue, Jira.isEvent(issue) {
            let lines = Jira.eventLines(issue.changes)
            if lines.isEmpty {
                return nil
            }
            it.kind = .event
            it.eventLines = lines
            it.eventText = Jira.eventText(issue.changes)
            return it
        }
        it.unread = !hasFlag(s.flags, .seen)
        if let issue = s.issue {
            let c = Jira.issueCard(issue.info, item: issue)
            it.internal = c.internal
            it.internalLabel = c.internalLabel
            it.via = c.via
            it.edited = c.edited
        }
        return it
    }

    /// conversation.withSent: puts the sent cards among the member items
    /// (oldest first) by (date, id): `sent` without empty, repeated and
    /// members' ids (`members` are the folder's members known, shown or
    /// not), the newest `API.Limits.maxThreadMessages`, and, when older
    /// members are left out (`cut`), none older than the oldest member
    /// item. A sent event is left out.
    private static func withSent(
        _ items: [Item], _ sent: [MessageSummary], _ members: [MessageSummary], cut: Bool, _ account: Account?
    ) -> [Item] {
        if sent.isEmpty {
            return items
        }
        let taken = Set(members.map(\.id))
        var list = sortedUnique(sent).filter { !taken.contains($0.id) && !Jira.isEvent($0.issue) }
        if list.count > API.Limits.maxThreadMessages {
            list = Array(list.suffix(API.Limits.maxThreadMessages))
        }
        var out: [Item] = []
        out.reserveCapacity(items.count + list.count)
        let first = items.first?.message
        var i = 0
        for s in list {
            if cut, let first, before(s, first) {
                continue
            }
            while i < items.count, let x = items[i].message, before(x, s) {
                out.append(items[i])
                i += 1
            }
            // An event is filtered out above, so the item is a card.
            guard var it = memberItem(s, account) else { continue }
            it.sent = true
            it.unread = false
            out.append(it)
        }
        out.append(contentsOf: items[i...])
        return out
    }

    /// conversation.markRead: the newest message card that is neither
    /// queued in the outbox nor a sent card when it is unread, else nil.
    private static func markRead(_ items: [Item]) -> MessageID? {
        for it in items.reversed() {
            guard it.kind == .message, !it.sent, let s = it.message, s.outbox == nil else { continue }
            return it.unread ? s.id : nil
        }
        return nil
    }

    /// conversation.mine: whether the user of `account` wrote member `s`
    /// (`Item.mine`).
    private static func mine(_ s: MessageSummary, _ account: Account?) -> Bool {
        if let issue = s.issue {
            return issue.mine == true && (issue.via ?? "").isEmpty
        }
        guard let first = s.from.first, let account else { return false }
        let own = account.config.email.trimmingCharacters(in: .whitespacesAndNewlines)
        let from = first.address.trimmingCharacters(in: .whitespacesAndNewlines)
        return !own.isEmpty && !from.isEmpty && equalFold(own, from)
    }

    /// strings.EqualFold: equal ignoring case, the folded scalars compared
    /// literally as Go compares strings (no canonical equivalence, which
    /// Swift's `==` on String would apply), as `CertTrust.foldKey` does.
    private static func equalFold(_ a: String, _ b: String) -> Bool {
        Array(a.lowercased().unicodeScalars) == Array(b.lowercased().unicodeScalars)
    }

    /// conversation.sender: the cleaned display name (else address) of the
    /// first address that has one.
    private static func sender(_ from: [Address]) -> String {
        for a in from {
            let name = Jira.clean(a.name ?? "")
            if !name.isEmpty {
                return name
            }
            let addr = Jira.clean(a.address)
            if !addr.isEmpty {
                return addr
            }
        }
        return ""
    }

    /// conversation.sortedUnique: `members` without empty and repeated ids
    /// (the first kept), oldest first by (date, id).
    private static func sortedUnique(_ members: [MessageSummary]) -> [MessageSummary] {
        var seen = Set<MessageID>()
        var out: [MessageSummary] = []
        out.reserveCapacity(members.count)
        for s in members {
            if s.id.rawValue.isEmpty || seen.contains(s.id) {
                continue
            }
            seen.insert(s.id)
            out.append(s)
        }
        // (date, id) is a total order once the ids are unique.
        out.sort(by: before)
        return out
    }

    /// conversation.before: orders members oldest first: by date, then by
    /// id (thread.get's order).
    static func before(_ a: MessageSummary, _ b: MessageSummary) -> Bool {
        if a.date != b.date {
            return a.date < b.date
        }
        return a.id.rawValue < b.id.rawValue
    }
}
