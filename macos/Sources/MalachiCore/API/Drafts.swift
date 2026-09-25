// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Drafts and sending (docs/api.md §4.3 message.send/outbox.retry, §4.5;
// types.go "Drafts and sending"). The daemon owns every derived field: it
// sanitises `htmlBody` on the way in, derives `textBody`, assigns
// attachment metadata and sets `updatedAt`.

import Foundation

/// api.DraftAttachment: a file in the daemon's attachment store, created by
/// `attachment.import`. In `draft.save` params only `id` is read.
public struct DraftAttachment: Codable, Sendable, Equatable {
    public var id: String
    /// Sanitised; never the raw client name.
    public var filename: String
    /// Detected from content, not taken from the client.
    public var contentType: String
    public var size: Int
    /// An image referenced from `htmlBody` as `cid:<contentId>`.
    public var inline: Bool
    /// Daemon-assigned, without angle brackets.
    public var contentId: String?

    public init(id: String, filename: String, contentType: String, size: Int, inline: Bool, contentId: String? = nil) {
        self.id = id
        self.filename = filename
        self.contentType = contentType
        self.size = size
        self.inline = inline
        self.contentId = contentId
    }
}

/// api.Draft: a message being composed. `id` nil on the first save;
/// `version` is optimistic concurrency (`conflict` when it differs from the
/// stored one). `htmlBody` in params is the editor's HTML, treated as
/// hostile and sanitised in compose mode; `textBody` is then derived.
/// `inReplyTo` and `forwarding` are mutually exclusive. `replaces` is the
/// Drafts message `draft.open` built the draft from; `draft.save` takes it
/// over (clients send it back unchanged).
public struct Draft: Codable, Sendable, Equatable {
    public var id: DraftID?
    public var accountId: AccountID
    public var version: Int
    @NullAsEmpty public var to: [Address]
    public var cc: [Address]?
    public var bcc: [Address]?
    public var subject: String
    public var textBody: String
    /// nil or empty = plain text.
    public var htmlBody: String?
    public var inReplyTo: MessageID?
    public var forwarding: MessageID?
    public var attachments: [DraftAttachment]?
    public var replaces: MessageID?
    /// Daemon-set; ignored in params (`Date.goZero` in a `draft.create` result).
    public var updatedAt: Date

    public init(
        id: DraftID? = nil, accountId: AccountID, version: Int = 0, to: [Address] = [], cc: [Address]? = nil,
        bcc: [Address]? = nil, subject: String = "", textBody: String = "", htmlBody: String? = nil,
        inReplyTo: MessageID? = nil, forwarding: MessageID? = nil, attachments: [DraftAttachment]? = nil,
        replaces: MessageID? = nil, updatedAt: Date = .goZero
    ) {
        self.id = id
        self.accountId = accountId
        self.version = version
        self.to = to
        self.cc = cc
        self.bcc = bcc
        self.subject = subject
        self.textBody = textBody
        self.htmlBody = htmlBody
        self.inReplyTo = inReplyTo
        self.forwarding = forwarding
        self.attachments = attachments
        self.replaces = replaces
        self.updatedAt = updatedAt
    }
}

/// api.DraftSaveParams.
public struct DraftSaveParams: Codable, Sendable, Equatable {
    public var draft: Draft

    public init(draft: Draft) {
        self.draft = draft
    }
}

/// api.DraftSaveResult: what the daemon stored, which is what will be sent.
public struct DraftSaveResult: Codable, Sendable, Equatable {
    public var draftId: DraftID
    public var version: Int
    public var textBody: String
    public var htmlBody: String?
    public var blocked: BlockedContent
    public var attachments: [DraftAttachment]?

    public init(
        draftId: DraftID, version: Int, textBody: String, htmlBody: String? = nil,
        blocked: BlockedContent = BlockedContent(), attachments: [DraftAttachment]? = nil
    ) {
        self.draftId = draftId
        self.version = version
        self.textBody = textBody
        self.htmlBody = htmlBody
        self.blocked = blocked
        self.attachments = attachments
    }
}

/// api.DraftListParams.
public struct DraftListParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var page: Page

    public init(accountId: AccountID, page: Page = Page()) {
        self.accountId = accountId
        self.page = page
    }
}

/// api.DraftListResult: newest `updatedAt` first, full bodies.
public struct DraftListResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var drafts: [Draft]
    public var page: PageInfo

    public init(drafts: [Draft], page: PageInfo) {
        self.drafts = drafts
        self.page = page
    }
}

/// api.DraftDeleteParams.
public struct DraftDeleteParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var draftId: DraftID

    public init(accountId: AccountID, draftId: DraftID) {
        self.accountId = accountId
        self.draftId = draftId
    }
}

/// api.DraftCreateParams: an unsaved template computed by the daemon
/// (recipients, Re:/Fwd:, the quoted sanitised body, imported pictures, or a
/// parsed mailto:). `attribution` is the client's line above the quote in
/// the user's language: plain text, LF-separated, at most
/// `API.Limits.maxDraftAttributionBytes` and `maxDraftAttributionLines`.
public struct DraftCreateParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var mode: ComposeMode
    /// Required unless `mode` is `new`.
    public var messageId: MessageID?
    /// `new` only.
    public var mailto: String?
    /// Ignored for `new`.
    public var attribution: String?

    public init(
        accountId: AccountID, mode: ComposeMode, messageId: MessageID? = nil, mailto: String? = nil,
        attribution: String? = nil
    ) {
        self.accountId = accountId
        self.mode = mode
        self.messageId = messageId
        self.mailto = mailto
        self.attribution = attribution
    }
}

/// api.DraftCreateResult: `draft` has no id and version 0; nothing is
/// persisted until the first `draft.save`. `draft.attachments` lists what
/// was imported for it (unbound until then).
public struct DraftCreateResult: Codable, Sendable, Equatable {
    public var draft: Draft
    public var quoted: QuoteForm
    /// What the sanitiser removed from the quoted original; empty unless `quoted` is `html`.
    public var blocked: BlockedContent
    /// Parts of the original that were not imported (over a cap, unreadable, …).
    public var skipped: [Attachment]?

    public init(draft: Draft, quoted: QuoteForm, blocked: BlockedContent = BlockedContent(), skipped: [Attachment]? = nil) {
        self.draft = draft
        self.quoted = quoted
        self.blocked = blocked
        self.skipped = skipped
    }
}

/// api.DraftOpenParams: a message of the account's Drafts folder to edit.
public struct DraftOpenParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var messageId: MessageID

    public init(accountId: AccountID, messageId: MessageID) {
        self.accountId = accountId
        self.messageId = messageId
    }
}

/// api.DraftOpenResult: the saved draft the message is the copy of (`id`
/// and `version` set), or a draft built from the message — unsaved, its
/// attachments imported but unbound, `replaces` set when nothing was lost;
/// a newer copy of a saved draft carries that draft's `id` and `version`.
/// Nothing is persisted by `draft.open`.
public struct DraftOpenResult: Codable, Sendable, Equatable {
    public var draft: Draft
    /// What the sanitiser removed from the message's HTML.
    public var blocked: BlockedContent
    /// Parts of the message that were not imported.
    public var skipped: [Attachment]?

    public init(draft: Draft, blocked: BlockedContent = BlockedContent(), skipped: [Attachment]? = nil) {
        self.draft = draft
        self.blocked = blocked
        self.skipped = skipped
    }
}

/// api.MessageSendParams: queues a saved draft. The result only confirms
/// enqueueing; the queued message lives in the outbox folder.
public struct MessageSendParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var draftId: DraftID
    public var version: Int

    public init(accountId: AccountID, draftId: DraftID, version: Int) {
        self.accountId = accountId
        self.draftId = draftId
        self.version = version
    }
}

/// api.MessageSendResult: the id of the queued message.
public struct MessageSendResult: Codable, Sendable, Equatable {
    public var outboxId: MessageID

    public init(outboxId: MessageID) {
        self.outboxId = outboxId
    }
}

/// api.OutboxRetryParams: re-queues a queued or failed outbox message for
/// an immediate attempt.
public struct OutboxRetryParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var messageId: MessageID

    public init(accountId: AccountID, messageId: MessageID) {
        self.accountId = accountId
        self.messageId = messageId
    }
}
