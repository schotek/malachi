// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// system.storage (docs/api.md §4.0; types.go "SystemStorageResult"): how
// much disk the mail store uses, for the settings window.

import Foundation

/// api.StorageConversion: the state of the background pass that brings the
/// stored mail to the current `compressStore` and `attachmentOfflineDays`.
public struct StorageConversion: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// Nothing left to convert.
    public static let idle: StorageConversion = "idle"
    /// Converting in the background.
    public static let running: StorageConversion = "running"
    /// Stopped: the disk is full; tried again after a `config.set` or a
    /// restart of the daemon.
    public static let noSpace: StorageConversion = "noSpace"
}

/// api.SystemStorageResult. Byte counts are file lengths, not allocated
/// blocks (a file system that compresses by itself may use less).
public struct SystemStorageResult: Codable, Sendable, Equatable {
    /// `databaseBytes + messageBytes + attachmentBytes`.
    public var totalBytes: Int
    /// store.db with its -wal and -shm.
    public var databaseBytes: Int
    /// The stored raw messages as they are stored (compressed or not).
    public var messageBytes: Int
    /// Their content; equals `messageBytes` without compression.
    public var messageUncompressedBytes: Int
    /// What compression saves: `messageUncompressedBytes - messageBytes`.
    public var savedBytes: Int
    /// The compose-side attachment store (drafts, attachment.import).
    public var attachmentBytes: Int
    /// The decoded size of the attachments kept on the server only.
    public var remoteAttachmentBytes: Int
    /// Messages with a stored raw file.
    public var messages: Int
    public var compressedMessages: Int
    /// Messages with attachments on the server only.
    public var partialMessages: Int
    public var conversion: StorageConversion

    public init(
        totalBytes: Int = 0, databaseBytes: Int = 0, messageBytes: Int = 0, messageUncompressedBytes: Int = 0,
        savedBytes: Int = 0, attachmentBytes: Int = 0, remoteAttachmentBytes: Int = 0, messages: Int = 0,
        compressedMessages: Int = 0, partialMessages: Int = 0, conversion: StorageConversion = .idle
    ) {
        self.totalBytes = totalBytes
        self.databaseBytes = databaseBytes
        self.messageBytes = messageBytes
        self.messageUncompressedBytes = messageUncompressedBytes
        self.savedBytes = savedBytes
        self.attachmentBytes = attachmentBytes
        self.remoteAttachmentBytes = remoteAttachmentBytes
        self.messages = messages
        self.compressedMessages = compressedMessages
        self.partialMessages = partialMessages
        self.conversion = conversion
    }
}
