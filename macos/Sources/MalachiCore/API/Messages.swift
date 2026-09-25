// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Messages (docs/api.md §3, §4.3; types.go "Messages"). Everything here
// that came from a message is hostile input: display it as plain text.

import Foundation

/// api.Address: a parsed RFC 5322 mailbox. Both fields are attacker-controlled
/// display data; never interpret them as markup.
public struct Address: Codable, Sendable, Hashable {
    public var name: String?
    public var address: String

    public init(name: String? = nil, address: String) {
        self.name = name
        self.address = address
    }
}

/// api.OutboxInfo: the delivery state of a message in the outbox folder.
public struct OutboxInfo: Codable, Sendable, Equatable {
    public var state: OutboxState
    public var attempts: Int
    /// Set while queued after a transient failure.
    public var nextAttemptAt: Date?
    /// The last failure; absent before the first attempt and after a success.
    public var error: RPCError?

    public init(state: OutboxState, attempts: Int, nextAttemptAt: Date? = nil, error: RPCError? = nil) {
        self.state = state
        self.attempts = attempts
        self.nextAttemptAt = nextAttemptAt
        self.error = error
    }
}

/// api.MessageSummary: the list-view projection of a message. Never contains
/// body content beyond `snippet`, plain text derived by the daemon.
public struct MessageSummary: Codable, Sendable, Equatable {
    public var id: MessageID
    public var accountId: AccountID
    public var folderId: FolderID
    /// The conversation; nil only for a message an older daemon stored that
    /// has not been linked yet.
    public var threadId: ThreadID?
    @NullAsEmpty public var from: [Address]
    public var to: [Address]?
    public var subject: String
    /// Best-effort when the header is garbage.
    public var date: Date
    public var snippet: String
    @NullAsEmpty public var flags: [Flag]
    public var hasAttachments: Bool
    public var size: Int
    /// Present only for a message in the account's outbox folder.
    public var outbox: OutboxInfo?

    public init(
        id: MessageID, accountId: AccountID, folderId: FolderID, threadId: ThreadID? = nil,
        from: [Address], to: [Address]? = nil, subject: String, date: Date, snippet: String,
        flags: [Flag], hasAttachments: Bool, size: Int, outbox: OutboxInfo? = nil
    ) {
        self.id = id
        self.accountId = accountId
        self.folderId = folderId
        self.threadId = threadId
        self.from = from
        self.to = to
        self.subject = subject
        self.date = date
        self.snippet = snippet
        self.flags = flags
        self.hasAttachments = hasAttachments
        self.size = size
        self.outbox = outbox
    }
}

/// api.Attachment: a MIME part the user can download; metadata only, the
/// bytes come through `message.part`. `filename` is sanitised by the daemon.
public struct Attachment: Codable, Sendable, Equatable {
    public var partId: String
    public var filename: String
    public var contentType: String
    public var size: Int
    /// Referenced from the HTML body via cid:.
    public var inline: Bool
    public var contentId: String?

    public init(partId: String, filename: String, contentType: String, size: Int, inline: Bool, contentId: String? = nil) {
        self.partId = partId
        self.filename = filename
        self.contentType = contentType
        self.size = size
        self.inline = inline
        self.contentId = contentId
    }
}

/// api.Message: the full header view (`message.get`). Go embeds
/// `MessageSummary`, so on the wire its fields sit beside these; here they
/// are `summary`, and the coding flattens.
public struct Message: Codable, Sendable, Equatable {
    public var summary: MessageSummary
    public var cc: [Address]?
    public var bcc: [Address]?
    public var replyTo: [Address]?
    /// The Message-ID header, display only.
    public var rfcMessageId: String?
    public var inReplyTo: String?
    public var references: [String]?
    public var attachments: [Attachment]
    /// A curated subset chosen by the daemon (List-Unsubscribe, Precedence,
    /// Auto-Submitted, …); never the raw header block.
    public var headers: [String: String]?

    public init(
        summary: MessageSummary, cc: [Address]? = nil, bcc: [Address]? = nil, replyTo: [Address]? = nil,
        rfcMessageId: String? = nil, inReplyTo: String? = nil, references: [String]? = nil,
        attachments: [Attachment] = [], headers: [String: String]? = nil
    ) {
        self.summary = summary
        self.cc = cc
        self.bcc = bcc
        self.replyTo = replyTo
        self.rfcMessageId = rfcMessageId
        self.inReplyTo = inReplyTo
        self.references = references
        self.attachments = attachments
        self.headers = headers
    }

    private enum CodingKeys: String, CodingKey {
        case cc, bcc, replyTo, rfcMessageId, inReplyTo, references, attachments, headers
    }

    public init(from decoder: any Decoder) throws {
        summary = try MessageSummary(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        cc = try c.decodeIfPresent([Address].self, forKey: .cc)
        bcc = try c.decodeIfPresent([Address].self, forKey: .bcc)
        replyTo = try c.decodeIfPresent([Address].self, forKey: .replyTo)
        rfcMessageId = try c.decodeIfPresent(String.self, forKey: .rfcMessageId)
        inReplyTo = try c.decodeIfPresent(String.self, forKey: .inReplyTo)
        references = try c.decodeIfPresent([String].self, forKey: .references)
        attachments = try c.decodeIfPresent([Attachment].self, forKey: .attachments) ?? []
        headers = try c.decodeIfPresent([String: String].self, forKey: .headers)
    }

    public func encode(to encoder: any Encoder) throws {
        try summary.encode(to: encoder)
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeIfPresent(cc, forKey: .cc)
        try c.encodeIfPresent(bcc, forKey: .bcc)
        try c.encodeIfPresent(replyTo, forKey: .replyTo)
        try c.encodeIfPresent(rfcMessageId, forKey: .rfcMessageId)
        try c.encodeIfPresent(inReplyTo, forKey: .inReplyTo)
        try c.encodeIfPresent(references, forKey: .references)
        try c.encode(attachments, forKey: .attachments)
        try c.encodeIfPresent(headers, forKey: .headers)
    }
}

/// api.MessageListParams. `unreadOnly` is the deprecated spelling of
/// `filter: .unread`, honoured only while `filter` is absent.
public struct MessageListParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var folderId: FolderID
    public var page: Page
    public var sort: SortOrder?
    public var filter: MessageFilter?
    public var unreadOnly: Bool?

    public init(
        accountId: AccountID, folderId: FolderID, page: Page = Page(), sort: SortOrder? = nil,
        filter: MessageFilter? = nil, unreadOnly: Bool? = nil
    ) {
        self.accountId = accountId
        self.folderId = folderId
        self.page = page
        self.sort = sort
        self.filter = filter
        self.unreadOnly = unreadOnly
    }
}

/// api.MessageListResult.
public struct MessageListResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var messages: [MessageSummary]
    public var page: PageInfo

    public init(messages: [MessageSummary], page: PageInfo) {
        self.messages = messages
        self.page = page
    }
}

/// api.MessageGetParams.
public struct MessageGetParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var messageId: MessageID

    public init(accountId: AccountID, messageId: MessageID) {
        self.accountId = accountId
        self.messageId = messageId
    }
}

/// api.MessageGetResult.
public struct MessageGetResult: Codable, Sendable, Equatable {
    public var message: Message

    public init(message: Message) {
        self.message = message
    }
}

/// api.MessageBodyParams. `remoteContent` overrides the stored preference
/// for this call only: `block` or `allow`; `knownSenders` is invalidArgument.
public struct MessageBodyParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var messageId: MessageID
    public var remoteContent: RemoteContentPolicy?

    public init(accountId: AccountID, messageId: MessageID, remoteContent: RemoteContentPolicy? = nil) {
        self.accountId = accountId
        self.messageId = messageId
        self.remoteContent = remoteContent
    }
}

/// api.BlockedContent: what the sanitiser removed or neutralised, so the UI
/// can show an honest "N remote images blocked".
public struct BlockedContent: Codable, Sendable, Equatable {
    public var remoteImages: Int
    public var remoteStyles: Int
    public var remoteFonts: Int
    public var scripts: Int
    public var forms: Int
    public var eventHandlers: Int
    /// javascript:, data:text/html, vbscript:, …
    public var dangerousUrls: Int
    public var embeddedFrames: Int
    /// Heuristically detected 1×1 remote images.
    public var trackingPixels: Int

    public init(
        remoteImages: Int = 0, remoteStyles: Int = 0, remoteFonts: Int = 0, scripts: Int = 0, forms: Int = 0,
        eventHandlers: Int = 0, dangerousUrls: Int = 0, embeddedFrames: Int = 0, trackingPixels: Int = 0
    ) {
        self.remoteImages = remoteImages
        self.remoteStyles = remoteStyles
        self.remoteFonts = remoteFonts
        self.scripts = scripts
        self.forms = forms
        self.eventHandlers = eventHandlers
        self.dangerousUrls = dangerousUrls
        self.embeddedFrames = embeddedFrames
        self.trackingPixels = trackingPixels
    }

    /// Nothing was removed.
    public var isEmpty: Bool { self == BlockedContent() }
}

/// api.Link: a hyperlink of the body with its real destination, so the UI
/// can show where a link goes rather than what it says.
public struct Link: Codable, Sendable, Equatable {
    public var text: String
    /// Normalised absolute URL; only http(s) and mailto survive.
    public var href: String

    public init(text: String, href: String) {
        self.text = text
        self.href = href
    }
}

/// api.MessageBodyResult: the renderable content of a message. `html` is
/// always the sanitiser's output, a fragment for the webview's body with
/// cid: images rewritten to `malachi-cid:<accountId>/<messageId>/<partId>`;
/// it is still rendered only in a JavaScript-disabled webview under a strict
/// CSP (docs/security.md §3.2).
public struct MessageBodyResult: Codable, Sendable, Equatable {
    public var messageId: MessageID
    public var bodyState: BodyState
    public var hasHtml: Bool
    /// Absent when `hasHtml` is false or `htmlWithheld`.
    public var html: String?
    /// The HTML part could not be shown safely; `text` is still served.
    public var htmlWithheld: Bool?
    /// The plain text alternative, or text derived from the HTML.
    public var text: String
    public var blocked: BlockedContent
    @NullAsEmpty public var links: [Link]
    /// Content-IDs whose cid: references survived, to their part ids.
    public var inlineParts: [String: String]?
    /// The policy that was applied: `block` or `allow`, never `knownSenders`.
    /// A client offers to load images only under `block`.
    public var remoteContent: RemoteContentPolicy
    public var sanitizerVersion: String

    public init(
        messageId: MessageID, bodyState: BodyState, hasHtml: Bool, html: String? = nil, htmlWithheld: Bool? = nil,
        text: String, blocked: BlockedContent = BlockedContent(), links: [Link] = [],
        inlineParts: [String: String]? = nil, remoteContent: RemoteContentPolicy, sanitizerVersion: String
    ) {
        self.messageId = messageId
        self.bodyState = bodyState
        self.hasHtml = hasHtml
        self.html = html
        self.htmlWithheld = htmlWithheld
        self.text = text
        self.blocked = blocked
        self.links = links
        self.inlineParts = inlineParts
        self.remoteContent = remoteContent
        self.sanitizerVersion = sanitizerVersion
    }
}

/// api.MessagePartParams: one MIME part by the `partId` an `Attachment`
/// carries and a `malachi-cid:` URL ends with.
public struct MessagePartParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var messageId: MessageID
    public var partId: String

    public init(accountId: AccountID, messageId: MessageID, partId: String) {
        self.accountId = accountId
        self.messageId = messageId
        self.partId = partId
    }
}

/// api.MessagePartResult: the decoded part, at most `API.Limits.maxAttachmentDataBytes`.
public struct MessagePartResult: Codable, Sendable, Equatable {
    public var partId: String
    public var contentType: String
    /// Sanitised, as in `Attachment`.
    public var filename: String
    public var size: Int
    public var data: Data

    public init(partId: String, contentType: String, filename: String, size: Int, data: Data) {
        self.partId = partId
        self.contentType = contentType
        self.filename = filename
        self.size = size
        self.data = data
    }
}

/// api.MessageEmbeddedParams: an attached message (message/rfc822 or *.eml)
/// of a stored message. `remoteContent` is resolved for the senders of the
/// containing message, not the attached one.
public struct MessageEmbeddedParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var messageId: MessageID
    public var partId: String
    public var remoteContent: RemoteContentPolicy?

    public init(accountId: AccountID, messageId: MessageID, partId: String, remoteContent: RemoteContentPolicy? = nil) {
        self.accountId = accountId
        self.messageId = messageId
        self.partId = partId
        self.remoteContent = remoteContent
    }
}

/// api.MessageEmbeddedResult: the attached message rendered read-only.
/// `message.summary.id`, `accountId` and `folderId` are the containing
/// message's; its cid: pictures are inlined as data: URIs (so
/// `body.inlineParts` is empty) and its attachments carry no `partId`.
public struct MessageEmbeddedResult: Codable, Sendable, Equatable {
    public var partId: String
    public var message: Message
    public var body: MessageBodyResult

    public init(partId: String, message: Message, body: MessageBodyResult) {
        self.partId = partId
        self.message = message
        self.body = body
    }
}

/// api.MessageFlagParams: at most `API.Limits.maxMessageIDsPerCall` ids;
/// `deleted` in either list is invalidArgument (use `message.delete`).
public struct MessageFlagParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var messageIds: [MessageID]
    public var set: [Flag]?
    public var clear: [Flag]?

    public init(accountId: AccountID, messageIds: [MessageID], set: [Flag]? = nil, clear: [Flag]? = nil) {
        self.accountId = accountId
        self.messageIds = messageIds
        self.set = set
        self.clear = clear
    }
}

/// api.MessageMoveParams.
public struct MessageMoveParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var messageIds: [MessageID]
    public var targetFolderId: FolderID

    public init(accountId: AccountID, messageIds: [MessageID], targetFolderId: FolderID) {
        self.accountId = accountId
        self.messageIds = messageIds
        self.targetFolderId = targetFolderId
    }
}

/// api.MessageDeleteParams. `permanent` bypasses the Trash folder.
public struct MessageDeleteParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var messageIds: [MessageID]
    public var permanent: Bool?

    public init(accountId: AccountID, messageIds: [MessageID], permanent: Bool? = nil) {
        self.accountId = accountId
        self.messageIds = messageIds
        self.permanent = permanent
    }
}
