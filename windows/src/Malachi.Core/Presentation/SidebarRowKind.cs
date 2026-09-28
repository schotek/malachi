// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the three kinds of row ui/internal/window/folders.go
// rebuildFolderList builds (newHeaderRow for the Favourites section and an
// account, newFolderRow); macOS's FavouritesNode, AccountNode and FolderNode
// (Sidebar/FolderOutlineDataSource.swift).

namespace Malachi.Core.Presentation;

/// <summary>What a row of the sidebar is (<see cref="SidebarRow"/>).</summary>
public enum SidebarRowKind
{
    /// <summary>The Favourites section's heading, which does not fold.</summary>
    FavouritesHeading,

    /// <summary>An account's heading, which folds its tree.</summary>
    AccountHeading,

    /// <summary>A folder, in the Favourites section or in its account's tree.</summary>
    Folder,
}
