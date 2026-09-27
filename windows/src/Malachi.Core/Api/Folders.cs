// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/API/Folders.swift; Go:
// backend/pkg/api/types.go ("Folders"); contract: docs/api.md §4.2.

using System.Collections.Generic;
using System.Text.Json.Serialization;

namespace Malachi.Core.Api;

/// <summary>
/// api.Folder. <see cref="Name"/> and <see cref="Path"/> are display text
/// from the server; never interpret them as markup.
/// </summary>
public sealed record Folder
{
    /// <summary>The folder.</summary>
    [JsonPropertyName("id")]
    public required FolderId Id { get; init; }

    /// <summary>Its account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The containing folder; null at the top.</summary>
    [JsonPropertyName("parentId")]
    public FolderId? ParentId { get; init; }

    /// <summary>Leaf display name.</summary>
    [JsonPropertyName("name")]
    public required string Name { get; init; }

    /// <summary>Full display path, "/"-separated.</summary>
    [JsonPropertyName("path")]
    public required string Path { get; init; }

    /// <summary>The special-use role.</summary>
    [JsonPropertyName("role")]
    public required FolderRole Role { get; init; }

    /// <summary>Whether the user subscribed to it.</summary>
    [JsonPropertyName("subscribed")]
    public required bool Subscribed { get; init; }

    /// <summary>False for a \Noselect container.</summary>
    [JsonPropertyName("selectable")]
    public required bool Selectable { get; init; }

    /// <summary>
    /// False for a folder the daemon lists and accepts moves into but never
    /// downloads (Gmail's All Mail); a message moved there leaves the store.
    /// </summary>
    [JsonPropertyName("synced")]
    public required bool Synced { get; init; }

    /// <summary>Unread messages in the local store.</summary>
    [JsonPropertyName("unread")]
    public required int Unread { get; init; }

    /// <summary>Messages in the local store.</summary>
    [JsonPropertyName("total")]
    public required int Total { get; init; }
}

/// <summary>api.FolderListParams.</summary>
public sealed record FolderListParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>Also return folders the user has not subscribed to.</summary>
    [JsonPropertyName("includeUnsubscribed")]
    public bool? IncludeUnsubscribed { get; init; }
}

/// <summary>api.FolderListResult: role folders first, then the rest by path.</summary>
public sealed record FolderListResult
{
    /// <summary>The folders.</summary>
    [JsonPropertyName("folders")]
    [JsonConverter(typeof(NullAsEmptyListConverter<Folder>))]
    public IReadOnlyList<Folder> Folders { get; init => field = value ?? []; } = [];
}

/// <summary>api.FolderSubscribeParams (<c>folder.subscribe</c> is notImplemented).</summary>
public sealed record FolderSubscribeParams
{
    /// <summary>The account.</summary>
    [JsonPropertyName("accountId")]
    public required AccountId AccountId { get; init; }

    /// <summary>The folder.</summary>
    [JsonPropertyName("folderId")]
    public required FolderId FolderId { get; init; }

    /// <summary>Subscribe (true) or unsubscribe (false).</summary>
    [JsonPropertyName("subscribed")]
    public required bool Subscribed { get; init; }
}
