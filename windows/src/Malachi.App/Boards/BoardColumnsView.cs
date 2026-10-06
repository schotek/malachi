// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The place of macos/Sources/MalachiMail/Board/BoardColumnsViewController.swift
// (GTK: window/board_columns.go), the board's Columns style: one column of
// cards per state, Hot ending with the commitments. Until its port lands
// this placeholder shows the style's name; the page already drives it as
// every style (IBoardStyleContent) and shows the selected case in its
// sliding panel (BoardDetailPanel).

using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>The board's Columns style (a placeholder until its port).</summary>
public sealed partial class BoardColumnsView : UserControl, IBoardStyleContent
{
    /// <summary>The placeholder.</summary>
    public BoardColumnsView()
    {
        var title = Board.Text.StyleTitle(BoardStyle.Columns);
        Content = new TextBlock
        {
            Text = title,
            HorizontalAlignment = HorizontalAlignment.Center,
            VerticalAlignment = VerticalAlignment.Center,
            Style = (Style)Application.Current.Resources["SubtitleTextBlockStyle"],
        };
        IsTabStop = false;
        AutomationProperties.SetAutomationId(this, "BoardColumns");
        AutomationProperties.SetName(this, title);
    }

    /// <inheritdoc/>
    public UIElement View => this;

    /// <inheritdoc/>
    public ContentControl? DetailHost => null;

    /// <inheritdoc/>
    public void Apply(BoardController.Changes changes)
    {
    }

    /// <inheritdoc/>
    public bool FocusContent() => false;
}
