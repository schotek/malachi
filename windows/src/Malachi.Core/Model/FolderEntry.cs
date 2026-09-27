// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/FolderEntry.swift; GTK:
// ui/internal/window/model.go (folderEntry). Immutable: the model replaces
// an entry (with) instead of changing it, so a list of entries once handed
// out stays as it was.

using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>
/// One row of the folder sidebar: an account header (<see cref="Header"/>
/// set, <see cref="Folder"/> null), the Favourites heading
/// (<see cref="Header"/> and <see cref="Favourite"/> set,
/// <see cref="Account"/> null too) or a folder at the given tree depth.
/// </summary>
public sealed record FolderEntry
{
    /// <summary>An account header or the Favourites heading.</summary>
    public bool Header { get; init; }

    /// <summary>The account; null only on the Favourites heading.</summary>
    public Account? Account { get; init; }

    /// <summary>The folder; null on header rows.</summary>
    public Folder? Folder { get; init; }

    /// <summary>The tree depth.</summary>
    public int Depth { get; init; }

    /// <summary>
    /// Set on the rows of the Favourites section at the top of the sidebar:
    /// its heading and one row per pinned folder, each at depth 0 without
    /// children. The same folder has a second row in its account's tree, so
    /// a folder key alone does not name a row.
    /// </summary>
    public bool Favourite { get; init; }

    /// <summary>
    /// The folder is pinned: its star is filled, on the row in the section
    /// and on the one in the tree alike.
    /// </summary>
    public bool Starred { get; init; }

    /// <summary>The row can be folded.</summary>
    public bool HasChildren { get; init; }

    /// <summary>The row is folded; its descendants are left out of the list entirely.</summary>
    public bool Collapsed { get; init; }

    /// <summary>
    /// Set on every row of an account whose tree has at least one parent, so
    /// childless rows can reserve the width of the arrow and their titles
    /// line up. An account with a flat folder list looks as before.
    /// </summary>
    public bool Nested { get; init; }

    /// <summary>
    /// The number the row shows: the folder's own unread count, or that plus
    /// every hidden descendant's while it is collapsed.
    /// </summary>
    public int Badge { get; init; }

    /// <summary>The folder this row stands for; null on a header row.</summary>
    public FolderKey? Key => Account is { } a && Folder is { } f ? new FolderKey(a.Id, f.Id) : null;
}
