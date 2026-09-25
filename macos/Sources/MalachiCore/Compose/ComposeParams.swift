// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The types of ui/internal/compose/prefill.go: what a compose window is
// opened for, what is known about the original, and what the window is
// prefilled with.

import Foundation

/// compose.Kind: what the compose window was opened for.
public enum ComposeKind: Sendable, Hashable, CaseIterable {
    case new
    case reply
    case replyAll
    case forward

    /// compose.Kind.Mode: the draft.create mode of the kind.
    public var mode: ComposeMode {
        switch self {
        case .new: return .new
        case .reply: return .reply
        case .replyAll: return .replyAll
        case .forward: return .forward
        }
    }
}

/// compose.Source: what the caller knows about the message being replied
/// to or forwarded. Every field is hostile input. A nil `date` (or Go's
/// zero time) is a message without one.
public struct ComposeSource: Sendable, Equatable {
    public var id: MessageID
    public var accountID: AccountID
    public var from: [Address]
    /// The Reply-To header; replies go here instead of `from`.
    public var replyTo: [Address]
    public var to: [Address]
    public var cc: [Address]
    public var subject: String
    public var date: Date?
    /// The plain-text body.
    public var text: String

    public init(
        id: MessageID = "", accountID: AccountID = "", from: [Address] = [], replyTo: [Address] = [],
        to: [Address] = [], cc: [Address] = [], subject: String = "", date: Date? = nil, text: String = ""
    ) {
        self.id = id
        self.accountID = accountID
        self.from = from
        self.replyTo = replyTo
        self.to = to
        self.cc = cc
        self.subject = subject
        self.date = date
        self.text = text
    }
}

/// compose.Params: opens a compose window with these fields prefilled.
public struct ComposeParams: Sendable, Equatable {
    public var kind: ComposeKind
    /// Preselects the From identity; nil means the first account.
    public var accountID: AccountID?
    public var to: [Address]
    public var cc: [Address]
    public var bcc: [Address]
    public var subject: String
    /// Inserted into the editor document verbatim and therefore already
    /// safe: only the backend (`fromDraft`), `prefill` and `parseMailto`
    /// produce it.
    public var bodyHTML: String
    public var inReplyTo: MessageID?
    public var forwarding: MessageID?
    /// What the backend imported for the draft (the quoted original's
    /// pictures, a forwarded message's files): not yet bound, the first
    /// draft.save binds them.
    public var attachments: [DraftAttachment]
    /// What the backend's sanitiser removed from the quoted original; the
    /// window says so once.
    public var blocked: BlockedContent

    public init(
        kind: ComposeKind = .new, accountID: AccountID? = nil, to: [Address] = [], cc: [Address] = [],
        bcc: [Address] = [], subject: String = "", bodyHTML: String = "", inReplyTo: MessageID? = nil,
        forwarding: MessageID? = nil, attachments: [DraftAttachment] = [], blocked: BlockedContent = BlockedContent()
    ) {
        self.kind = kind
        self.accountID = accountID
        self.to = to
        self.cc = cc
        self.bcc = bcc
        self.subject = subject
        self.bodyHTML = bodyHTML
        self.inReplyTo = inReplyTo
        self.forwarding = forwarding
        self.attachments = attachments
        self.blocked = blocked
    }
}
