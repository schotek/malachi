// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The place of macos/Sources/MalachiMail/Board/BoardTodayViewController.swift
// (GTK: window/board_today.go), the board's Today style: the greeting, the
// tiles, Hot and Waiting for You, the deadlines and the calendar
// placeholder. Until its port lands this placeholder shows the style's
// name; the page already drives it as every style (IBoardStyleContent) and
// shows the selected case in its sliding panel (BoardDetailPanel).

using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>The board's Today style (a placeholder until its port).</summary>
public sealed partial class BoardTodayView : UserControl, IBoardStyleContent
{
    /// <summary>The placeholder.</summary>
    public BoardTodayView()
    {
        var title = Board.Text.StyleTitle(BoardStyle.Today);
        Content = new TextBlock
        {
            Text = title,
            HorizontalAlignment = HorizontalAlignment.Center,
            VerticalAlignment = VerticalAlignment.Center,
            Style = (Style)Application.Current.Resources["SubtitleTextBlockStyle"],
        };
        IsTabStop = false;
        AutomationProperties.SetAutomationId(this, "BoardToday");
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
