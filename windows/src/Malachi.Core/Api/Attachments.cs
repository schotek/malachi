// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Attachments.swift; Go:
// backend/pkg/api/types.go ("Attachments (compose-side store)"); contract:
// docs/api.md §4.10.

using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>
/// api.AttachmentImportParams: exactly one of <see cref="Path"/> (absolute, a
/// regular file) and <see cref="Data"/> (at most
/// <see cref="API.Limits.MaxAttachmentDataBytes"/>; needs
/// <see cref="Filename"/>). <see cref="Inline"/> marks an image to be
/// referenced as <c>cid:&lt;contentId&gt;</c>.
/// </summary>
public sealed record AttachmentImportParams
{
    /// <summary>The account whose store takes the file.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>A local file the user picked.</summary>
    [JsonPropertyName("path")]
    public string? Path { get; init; }

    /// <summary>Pasted or dropped content without a path; base64 on the wire.</summary>
    [JsonPropertyName("data")]
    public byte[]? Data { get; init; }

    /// <summary>Required with <see cref="Data"/>; overrides the basename of <see cref="Path"/>.</summary>
    [JsonPropertyName("filename")]
    public string? Filename { get; init; }

    /// <summary>An image to be referenced from the HTML body.</summary>
    [JsonPropertyName("inline")]
    public bool? Inline { get; init; }
}

/// <summary>api.AttachmentImportResult.</summary>
public sealed record AttachmentImportResult
{
    /// <summary>What the store holds now.</summary>
    [JsonPropertyName("attachment")]
    public required DraftAttachment Attachment { get; init; }
}

/// <summary>api.AttachmentRemoveParams. Removing an unknown id is not an error.</summary>
public sealed record AttachmentRemoveParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The attachment.</summary>
    [JsonPropertyName("attachmentId")]
    public required string AttachmentId { get; init; }
}

/// <summary>
/// api.AttachmentGetParams: reads a stored attachment back, what an editor
/// shows for a <c>cid:</c> reference the daemon minted.
/// </summary>
public sealed record AttachmentGetParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The attachment.</summary>
    [JsonPropertyName("attachmentId")]
    public required string AttachmentId { get; init; }
}

/// <summary>
/// api.AttachmentGetResult: the whole file; one over
/// <see cref="API.Limits.MaxAttachmentDataBytes"/> is attachmentTooBig.
/// </summary>
public sealed record AttachmentGetResult
{
    /// <summary>The attachment.</summary>
    [JsonPropertyName("attachmentId")]
    public required string AttachmentId { get; init; }

    /// <summary>Sanitised by the daemon.</summary>
    [JsonPropertyName("filename")]
    public required string Filename { get; init; }

    /// <summary>Detected from the content.</summary>
    [JsonPropertyName("contentType")]
    public required string ContentType { get; init; }

    /// <summary>In bytes.</summary>
    [JsonPropertyName("size")]
    public required long Size { get; init; }

    /// <summary>The file; base64 on the wire.</summary>
    [JsonPropertyName("data")]
    public required byte[] Data { get; init; }
}
