// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardStyleContent.swift
// (BoardStyleContent): what the board page needs of each style's view
// (List, Columns, Today): the controller's changes, the keyboard, and
// whether it holds the detail beside its cases (the List's pane) or leaves
// it to the page's sliding panel.

using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>A style of the board, as its page drives it.</summary>
public interface IBoardStyleContent
{
    /// <summary>The style's view, which the page puts into its content.</summary>
    UIElement View { get; }

    /// <summary>
    /// Where the detail goes while the style shows it beside its cases
    /// (the List's detail pane); null for a style that never does (Columns,
    /// Today: the sliding panel).
    /// </summary>
    ContentControl? DetailHost { get; }

    /// <summary>
    /// Brings the view up to date with the controller's view model for
    /// <paramref name="changes"/>, touching only what they require. The page
    /// forwards every change while the style shows, and all of them
    /// (<see cref="BoardController.Changes"/> every bit) when it comes back.
    /// </summary>
    void Apply(BoardController.Changes changes);

    /// <summary>Gives the keyboard to the style: the selected case's row or card, else the first.</summary>
    bool FocusContent();
}
