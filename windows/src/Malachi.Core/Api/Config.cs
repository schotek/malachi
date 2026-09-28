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
/// <remarks>
/// <see cref="CompressStore"/>, <see cref="AttachmentOfflineDays"/> and
/// <see cref="NeverStoreAttachments"/> were added later and follow other
/// rules: in <c>config.set</c> an absent (null) one is left unchanged, so an
/// older daemon's answer, which lacks them, goes back without them;
/// <c>config.get</c> and the <c>config.set</c> result of a daemon that knows
/// them always carry them all. Null is never written (Go's
/// <c>omitempty</c> pointers; the wire options leave nulls out), while
/// false and 0 are.
/// </remarks>
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

    /// <summary>
    /// Raw messages are stored zstd-compressed; changing it converts the
    /// stored mail in the background. Null: a daemon that does not know it.
    /// </summary>
    [JsonPropertyName("compressStore")]
    public bool? CompressStore { get; init; }

    /// <summary>
    /// Which large attachments (<c>api.LargeAttachmentMinBytes</c>, 100 KiB,
    /// and up) are stored locally: 0 all, 1 to
    /// <see cref="API.Limits.AttachmentOfflineDaysMax"/> those of the
    /// messages of the last N days, <see cref="API.Limits.AttachmentOfflineNone"/>
    /// (-1) none; the others stay on the server (<c>Attachment.remote</c>).
    /// Null: a daemon that does not know it.
    /// </summary>
    [JsonPropertyName("attachmentOfflineDays")]
    public int? AttachmentOfflineDays { get; init; }

    /// <summary>
    /// No attachment of any size is stored (the smaller pictures the HTML
    /// shows are); <c>message.download</c> then holds the message in the
    /// daemon's memory only, until it quits. Overrides
    /// <see cref="AttachmentOfflineDays"/>. Null: a daemon that does not know it.
    /// </summary>
    [JsonPropertyName("neverStoreAttachments")]
    public bool? NeverStoreAttachments { get; init; }
}

/// <summary>api.ConfigGetResult.</summary>
public sealed record ConfigGetResult
{
    /// <summary>The daemon's preferences.</summary>
    [JsonPropertyName("preferences")]
    public required Preferences Preferences { get; init; }
}

/// <summary>
/// api.ConfigSetParams: the preference set (read-modify-write); an absent
/// optional field is left unchanged.
/// </summary>
public sealed record ConfigSetParams
{
    /// <summary>Every preference, the unchanged ones echoed.</summary>
    [JsonPropertyName("preferences")]
    public required Preferences Preferences { get; init; }
}

/// <summary>
/// api.ConfigSetResult: the effective values after validation, every field
/// set (by a daemon that knows them all).
/// </summary>
public sealed record ConfigSetResult
{
    /// <summary>The preferences now in effect.</summary>
    [JsonPropertyName("preferences")]
    public required Preferences Preferences { get; init; }
}
