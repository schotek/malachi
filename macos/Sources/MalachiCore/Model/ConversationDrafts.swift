// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/conversation/drafts.go: the user's unsent replies to the
// conversation (thread.get's `drafts` with `withDrafts`;
// `ThreadSummary.draftCount` counts them), on an issue the unsent
// comments, are draft cards: the pane shows them right under what opened
// the conversation, above the newest member, newest first (`draftsFirst`),
// with a Draft badge, when they were saved, their plain text and Edit and
// Discard. They are no members: never unread, never marked read, never
// folded, never among the messages the conversation's actions take, and
// not in `Model.items` (whose rules they would change). Edit opens the
// draft in a compose window (a comment window for a comment draft);
// Discard asks `discardQuestion()` and deletes it (draft.delete).
// notify.draftsChanged naming the thread asks thread.get again.

import Foundation

extension Conversation {
    /// drafts.go `draftTextBytes`: the text a draft card shows at most;
    /// Edit shows it whole.
    static let draftTextBytes = 64 << 10

    /// conversation.DraftCard: one draft card.
    public struct DraftCard: Sendable, Equatable {
        /// The draft as thread.get returned it: what Edit opens.
        public var draft: Draft
        /// The name of the card's avatar: the user's own (the account's
        /// display name, else its address), cleaned for one line.
        public var sender: String
        /// The draft's plain text (the daemon's, derived from its HTML) as
        /// the card shows it: cleaned (`Board.cleanBlock`), at most
        /// `draftTextBytes`; "" for an empty draft.
        public var text: String
        /// A comment draft of an issue (`Draft.comment`): Edit opens the
        /// comment window.
        public var comment: Bool
        /// A comment draft only the service-desk team will read, which
        /// shows the badge `internalLabel` ("" when not internal).
        public var `internal`: Bool
        public var internalLabel: String
    }

    /// conversation.Drafts: the draft cards of a conversation from
    /// thread.get's drafts: those of the account only, without an id or
    /// with a repeated one (the first kept), newest `updatedAt` first (ties
    /// by id, the greater first), at most `API.Limits.maxThreadDrafts`.
    public static func drafts(_ drafts: [Draft], account: Account?) -> [DraftCard] {
        var seen = Set<DraftID>()
        var list: [Draft] = []
        for d in drafts {
            guard let id = d.id, !id.rawValue.isEmpty, !seen.contains(id) else { continue }
            if let account, !account.id.rawValue.isEmpty, !d.accountId.rawValue.isEmpty, d.accountId != account.id {
                continue
            }
            seen.insert(id)
            list.append(d)
        }
        // A stable sort: Swift's sort is not, so the input order breaks
        // ties that the rule leaves (none: the ids differ).
        list.sort { a, b in
            if a.updatedAt != b.updatedAt {
                return a.updatedAt > b.updatedAt
            }
            return (a.id?.rawValue ?? "") > (b.id?.rawValue ?? "")
        }
        if list.count > API.Limits.maxThreadDrafts {
            list = Array(list.prefix(API.Limits.maxThreadDrafts))
        }
        var name = Jira.clean(account?.config.displayName ?? "")
        if name.isEmpty {
            name = Jira.clean(account?.config.email ?? "")
        }
        return list.map { d in
            var c = DraftCard(
                draft: d, sender: name, text: Board.cleanBlock(d.textBody, max: draftTextBytes),
                comment: d.comment != nil, internal: false, internalLabel: "")
            if let comment = d.comment, comment.visibility == .internal {
                c.internal = true
                c.internalLabel = Jira.internalLabel()
            }
            return c
        }
    }

    /// conversation.SameDrafts: two lists of draft cards show the same:
    /// the same drafts in the same order at the same versions.
    public static func sameDrafts(_ a: [DraftCard], _ b: [DraftCard]) -> Bool {
        a.count == b.count
            && zip(a, b).allSatisfy { x, y in
                x.draft.id == y.draft.id && x.draft.version == y.draft.version && x.text == y.text
                    && x.internal == y.internal
            }
    }

    /// conversation.DraftBadge: the badge of a draft card.
    public static func draftBadge() -> String {
        // TRANSLATORS: a badge on a card of the user's unsent reply or comment in a conversation.
        L10n.C("conversation", "Draft")
    }

    /// conversation.DraftEditLabel: the button of a draft card that opens
    /// it for editing.
    public static func draftEditLabel() -> String {
        L10n.T("Edit")
    }

    /// conversation.DraftDiscardLabel: the button of a draft card that
    /// deletes it, after `discardQuestion()`.
    public static func draftDiscardLabel() -> String {
        L10n.T("_Discard")
    }

    /// conversation.DiscardQuestion: the question before a draft card's
    /// Discard deletes the draft (the compose window's own question).
    public static func discardQuestion() -> String {
        L10n.T("Discard this message?")
    }

    /// conversation.DraftGone: the toast of an Edit whose draft is gone
    /// (sent or deleted meanwhile).
    public static func draftGone() -> String {
        L10n.T("The draft is no longer there")
    }

    /// conversation.DraftSaved: the line of a draft card that says when it
    /// was saved; `when` is the client's own short date of `updatedAt`.
    public static func draftSaved(_ when: String) -> String {
        L10n.T("Draft saved %s", when)
    }

    /// conversation.DraftsFirst: where the pane puts the draft cards among
    /// the items it shows (`ConversationLayout.displayOrder`): right after
    /// the card that opened the conversation when it is first (root 0),
    /// else first.
    public static func draftsFirst(_ root: Int) -> Int {
        root == 0 ? 1 : 0
    }
}
