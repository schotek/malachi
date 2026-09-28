// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the (FolderKey?, Bool) pair that
// macos/Sources/MalachiCore/Controllers/MailboxController.swift hands to
// onSelectionChanged and onFolderSelected; GTK:
// ui/internal/window/folders.go (selectFolder, highlightFolderRow, whose
// row key is the folder and the Favourites flag).

using Malachi.Core.Model;

namespace Malachi.Core.Controllers;

/// <summary>
/// The selected folder and which of a pinned folder's two rows the user
/// clicked last: the one in the Favourites section
/// (<see cref="Favourite"/>) or the one in the tree.
/// </summary>
/// <param name="Key">The folder; null when nothing is selected.</param>
/// <param name="Favourite">The row in the Favourites section was the one clicked.</param>
public readonly record struct FolderSelection(FolderKey? Key, bool Favourite);
