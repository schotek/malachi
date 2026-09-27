// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Daemon-owned preferences (docs/api.md §4.8; types.go "Config").

import Foundation

/// api.Preferences: the options that affect mail handling and therefore live
/// in the daemon, not in the app's own settings. `config.set` is
/// read-modify-write: echo the whole set from `config.get`.
///
/// `compressStore` and `attachmentOfflineDays` were added later and follow
/// other rules: in `config.set` an absent (nil) one is left unchanged, so
/// an older daemon's answer, which lacks them, goes back without them;
/// `config.get` and the `config.set` result of a daemon that knows them
/// always carry both. nil is never encoded (Go's `omitempty` pointers).
public struct Preferences: Codable, Sendable, Equatable {
    /// 0 = manual sync only; otherwise at least `API.Limits.syncIntervalMin`.
    public var syncIntervalSeconds: Int
    /// The default policy of `message.body`: block, knownSenders or allow.
    public var remoteContent: RemoteContentPolicy
    /// Messages newer than this many days are kept locally; 0 keeps everything.
    public var offlineDays: Int
    /// Raw messages are stored zstd-compressed; changing it converts the
    /// stored mail in the background. nil: a daemon that does not know it.
    public var compressStore: Bool?
    /// Which large attachments (`API.Limits.largeAttachmentMinBytes` and
    /// up) are stored locally: 0 all, 1…`attachmentOfflineDaysMax` those
    /// of the messages of the last N days, `attachmentOfflineNone` (-1)
    /// none; the others stay on the server (`Attachment.remote`). nil: a
    /// daemon that does not know it.
    public var attachmentOfflineDays: Int?

    public init(
        syncIntervalSeconds: Int, remoteContent: RemoteContentPolicy, offlineDays: Int,
        compressStore: Bool? = nil, attachmentOfflineDays: Int? = nil
    ) {
        self.syncIntervalSeconds = syncIntervalSeconds
        self.remoteContent = remoteContent
        self.offlineDays = offlineDays
        self.compressStore = compressStore
        self.attachmentOfflineDays = attachmentOfflineDays
    }
}

/// api.ConfigGetResult.
public struct ConfigGetResult: Codable, Sendable, Equatable {
    public var preferences: Preferences

    public init(preferences: Preferences) {
        self.preferences = preferences
    }
}

/// api.ConfigSetParams: the preference set (read-modify-write); an absent
/// optional field is left unchanged.
public struct ConfigSetParams: Codable, Sendable, Equatable {
    public var preferences: Preferences

    public init(preferences: Preferences) {
        self.preferences = preferences
    }
}

/// api.ConfigSetResult: the effective values after validation, every field
/// set (by a daemon that knows them all).
public struct ConfigSetResult: Codable, Sendable, Equatable {
    public var preferences: Preferences

    public init(preferences: Preferences) {
        self.preferences = preferences
    }
}
