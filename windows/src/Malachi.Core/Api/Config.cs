// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Config.swift; Go:
// backend/pkg/api/types.go ("Config (daemon-owned preferences)"); contract:
// docs/api.md §4.8.

using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>
/// api.Preferences: the options that affect mail handling and therefore live
/// in the daemon, not in the app's own settings. <c>config.set</c> is
/// read-modify-write: echo the whole set from <c>config.get</c>.
/// </summary>
public sealed record Preferences
{
    /// <summary>0 = manual sync only; otherwise at least <see cref="API.Limits.SyncIntervalMin"/>.</summary>
    [JsonPropertyName("syncIntervalSeconds")]
    public required int SyncIntervalSeconds { get; init; }

    /// <summary>The default policy of <c>message.body</c>: block, knownSenders or allow.</summary>
    [JsonPropertyName("remoteContent")]
    public required RemoteContentPolicy RemoteContent { get; init; }

    /// <summary>Messages newer than this many days are kept locally; 0 keeps everything.</summary>
    [JsonPropertyName("offlineDays")]
    public required int OfflineDays { get; init; }
}

/// <summary>api.ConfigGetResult.</summary>
public sealed record ConfigGetResult
{
    /// <summary>The daemon's preferences.</summary>
    [JsonPropertyName("preferences")]
    public required Preferences Preferences { get; init; }
}

/// <summary>api.ConfigSetParams: the whole preference set.</summary>
public sealed record ConfigSetParams
{
    /// <summary>Every preference, the unchanged ones echoed.</summary>
    [JsonPropertyName("preferences")]
    public required Preferences Preferences { get; init; }
}

/// <summary>api.ConfigSetResult: the effective values after validation.</summary>
public sealed record ConfigSetResult
{
    /// <summary>The preferences now in effect.</summary>
    [JsonPropertyName("preferences")]
    public required Preferences Preferences { get; init; }
}
