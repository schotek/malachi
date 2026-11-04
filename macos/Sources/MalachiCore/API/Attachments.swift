// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The compose-side attachment store (docs/api.md §4.10; types.go
// "Attachments").

import Foundation

/// api.AttachmentImportParams: exactly one of `path` (absolute, a regular
/// file) and `data` (at most `API.Limits.maxAttachmentDataBytes`; needs
/// `filename`). `inline` marks an image to be referenced as `cid:<contentId>`.
public struct AttachmentImportParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var path: String?
    public var data: Data?
    /// Required with `data`; overrides the basename of `path`.
    public var filename: String?
    public var inline: Bool?

    public init(accountId: AccountID, path: String? = nil, data: Data? = nil, filename: String? = nil, inline: Bool? = nil) {
        self.accountId = accountId
        self.path = path
        self.data = data
        self.filename = filename
        self.inline = inline
    }
}

/// api.AttachmentImportResult.
public struct AttachmentImportResult: Codable, Sendable, Equatable {
    public var attachment: DraftAttachment

    public init(attachment: DraftAttachment) {
        self.attachment = attachment
    }
}

/// api.AttachmentRemoveParams. Removing an unknown id is not an error.
public struct AttachmentRemoveParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var attachmentId: String

    public init(accountId: AccountID, attachmentId: String) {
        self.accountId = accountId
        self.attachmentId = attachmentId
    }
}

/// api.AttachmentGetParams: reads a stored attachment back, what an editor
/// shows for a `cid:` reference the daemon minted.
public struct AttachmentGetParams: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var attachmentId: String

    public init(accountId: AccountID, attachmentId: String) {
        self.accountId = accountId
        self.attachmentId = attachmentId
    }
}

/// api.AttachmentGetResult: the whole file; one over
/// `API.Limits.maxAttachmentDataBytes` is attachmentTooBig.
public struct AttachmentGetResult: Codable, Sendable, Equatable {
    public var attachmentId: String
    public var filename: String
    public var contentType: String
    public var size: Int
    public var data: Data

    public init(attachmentId: String, filename: String, contentType: String, size: Int, data: Data) {
        self.attachmentId = attachmentId
        self.filename = filename
        self.contentType = contentType
        self.size = size
        self.data = data
    }
}
