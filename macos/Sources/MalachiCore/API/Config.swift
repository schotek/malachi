// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Daemon-owned preferences (docs/api.md §4.8; types.go "Config").

import Foundation

/// api.Preferences: the options that affect mail handling and therefore live
/// in the daemon, not in the app's own settings. `config.set` is
/// read-modify-write: echo the whole set from `config.get`.
public struct Preferences: Codable, Sendable, Equatable {
    /// 0 = manual sync only; otherwise at least `API.Limits.syncIntervalMin`.
    public var syncIntervalSeconds: Int
    /// The default policy of `message.body`: block, knownSenders or allow.
    public var remoteContent: RemoteContentPolicy
    /// Messages newer than this many days are kept locally; 0 keeps everything.
    public var offlineDays: Int

    public init(syncIntervalSeconds: Int, remoteContent: RemoteContentPolicy, offlineDays: Int) {
        self.syncIntervalSeconds = syncIntervalSeconds
        self.remoteContent = remoteContent
        self.offlineDays = offlineDays
    }
}

/// api.ConfigGetResult.
public struct ConfigGetResult: Codable, Sendable, Equatable {
    public var preferences: Preferences

    public init(preferences: Preferences) {
        self.preferences = preferences
    }
}

/// api.ConfigSetParams: the whole preference set.
public struct ConfigSetParams: Codable, Sendable, Equatable {
    public var preferences: Preferences

    public init(preferences: Preferences) {
        self.preferences = preferences
    }
}

/// api.ConfigSetResult: the effective values after validation.
public struct ConfigSetResult: Codable, Sendable, Equatable {
    public var preferences: Preferences

    public init(preferences: Preferences) {
        self.preferences = preferences
    }
}
