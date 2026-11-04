// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Storage.swift; Go:
// backend/pkg/api/types.go (SystemStorageParams, StorageConversion,
// SystemStorageResult); contract: docs/api.md §4.0 system.storage. How much
// disk the mail store uses, for the Disk Space Used row of the preferences.
// The params are EmptyParams, as Swift's: api.SystemStorageParams is an
// empty struct.

using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>
/// api.StorageConversion: the state of the background pass that brings the
/// stored mail to the current <see cref="Preferences.CompressStore"/> and
/// <see cref="Preferences.AttachmentOfflineDays"/>.
/// </summary>
[JsonConverter(typeof(StringWireValueConverter<StorageConversion>))]
public readonly record struct StorageConversion(string Value) : IWireEnumeration<StorageConversion>
{
    /// <summary>Nothing left to convert.</summary>
    public const string Idle = "idle";

    /// <summary>Converting in the background.</summary>
    public const string Running = "running";

    /// <summary>
    /// Stopped: the disk is full; tried again after a <c>config.set</c> or a
    /// restart of the daemon.
    /// </summary>
    public const string NoSpace = "noSpace";

    /// <summary>The value of a wire string.</summary>
    public static implicit operator StorageConversion(string value) => new(value);

    /// <summary>The wire string.</summary>
    public override string ToString() => Value ?? "";
}

/// <summary>
/// api.SystemStorageResult. Byte counts are file lengths, not allocated
/// blocks (a file system that compresses by itself may use less). The
/// daemon writes every member and decoding requires them, as Swift's;
/// made in code, a record starts from zero and idle.
/// </summary>
public sealed record SystemStorageResult
{
    /// <summary><c>databaseBytes + messageBytes + attachmentBytes</c>.</summary>
    [JsonPropertyName("totalBytes")]
    [JsonRequired]
    public long TotalBytes { get; init; }

    /// <summary>store.db with its -wal and -shm.</summary>
    [JsonPropertyName("databaseBytes")]
    [JsonRequired]
    public long DatabaseBytes { get; init; }

    /// <summary>The stored raw messages as they are stored (compressed or not).</summary>
    [JsonPropertyName("messageBytes")]
    [JsonRequired]
    public long MessageBytes { get; init; }

    /// <summary>Their content; equals <see cref="MessageBytes"/> without compression.</summary>
    [JsonPropertyName("messageUncompressedBytes")]
    [JsonRequired]
    public long MessageUncompressedBytes { get; init; }

    /// <summary>What compression saves: <c>messageUncompressedBytes - messageBytes</c>.</summary>
    [JsonPropertyName("savedBytes")]
    [JsonRequired]
    public long SavedBytes { get; init; }

    /// <summary>The compose-side attachment store (drafts, <c>attachment.import</c>).</summary>
    [JsonPropertyName("attachmentBytes")]
    [JsonRequired]
    public long AttachmentBytes { get; init; }

    /// <summary>The decoded size of the attachments kept on the server only.</summary>
    [JsonPropertyName("remoteAttachmentBytes")]
    [JsonRequired]
    public long RemoteAttachmentBytes { get; init; }

    /// <summary>Messages with a stored raw file.</summary>
    [JsonPropertyName("messages")]
    [JsonRequired]
    public int Messages { get; init; }

    /// <summary>Of those, the compressed ones.</summary>
    [JsonPropertyName("compressedMessages")]
    [JsonRequired]
    public int CompressedMessages { get; init; }

    /// <summary>Messages with attachments on the server only.</summary>
    [JsonPropertyName("partialMessages")]
    [JsonRequired]
    public int PartialMessages { get; init; }

    /// <summary>The background conversion; a state a newer daemon adds decodes as itself.</summary>
    [JsonPropertyName("conversion")]
    [JsonRequired]
    public StorageConversion Conversion { get; init; } = StorageConversion.Idle;
}
