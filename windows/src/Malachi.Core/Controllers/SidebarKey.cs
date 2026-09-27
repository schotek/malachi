// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §7.5): the key of a sidebar row,
// by which KeyedListSync keeps the rows of the WinUI ListView and the
// mailbox names the highlighted one. GTK keys its folder rows the same way
// (ui/internal/window/folders.go, rowKey: the folder and whether it is the
// row in the Favourites section); macOS keeps the outline's nodes by the
// same pair (Sidebar/FolderOutlineDataSource.swift, node(_:fav:)). The
// headers, which GTK and macOS rebuild without a key, get one here.

using Malachi.Core.Api;
using Malachi.Core.Model;

namespace Malachi.Core.Controllers;

/// <summary>
/// Names one row of the sidebar across rebuilds: the Favourites heading
/// (<see cref="Header"/> and <see cref="Favourite"/>), an account's heading
/// (<see cref="Header"/> and <see cref="Account"/>), a pinned folder's row
/// in the Favourites section (<see cref="Favourite"/>, <see cref="Account"/>
/// and <see cref="Folder"/>) or a folder's row in its account's tree.
/// Unique within <see cref="MailboxController.Entries"/>: a folder has at
/// most one row in the tree and one in the section.
/// </summary>
/// <param name="Header">A heading row.</param>
/// <param name="Favourite">A row of the Favourites section, or its heading.</param>
/// <param name="Account">The account; null only on the Favourites heading.</param>
/// <param name="Folder">The folder; null on the headings.</param>
public readonly record struct SidebarKey(bool Header, bool Favourite, AccountId? Account, FolderId? Folder)
{
    /// <summary>The key of <paramref name="entry"/>'s row.</summary>
    public static SidebarKey Of(FolderEntry entry)
    {
        System.ArgumentNullException.ThrowIfNull(entry);
        return new(entry.Header, entry.Favourite, entry.Account?.Id, entry.Folder?.Id);
    }

    /// <summary>The key of <paramref name="k"/>'s row in the tree, or in the Favourites section when <paramref name="favourite"/>.</summary>
    public static SidebarKey ForFolder(FolderKey k, bool favourite) => new(false, favourite, k.Account, k.Folder);
}
