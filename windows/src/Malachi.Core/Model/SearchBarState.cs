// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/SearchModel.swift
// (SearchBarState); GTK: ui/internal/window/search.go (refreshSearchScope).

using Malachi.Core.Settings;

namespace Malachi.Core.Model;

/// <summary>
/// The scope bar over the list while a search is on: the chosen scope,
/// whether Folder and Account can be chosen (they need a selected folder),
/// and what each searches, for the tooltips. Plain text: WinUI tooltips take
/// no markup.
/// </summary>
public sealed record SearchBarState
{
    /// <summary>The chosen scope.</summary>
    public required SearchScope Scope { get; init; }

    /// <summary>Folder and Account can be chosen.</summary>
    public required bool NarrowEnabled { get; init; }

    /// <summary>What Folder searches.</summary>
    public required string FolderTooltip { get; init; }

    /// <summary>What Account searches; empty without a selected folder.</summary>
    public required string AccountTooltip { get; init; }

    /// <summary>What All searches.</summary>
    public required string AllTooltip { get; init; }
}
