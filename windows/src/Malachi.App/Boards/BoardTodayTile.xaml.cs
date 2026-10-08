// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardTodayViewController.swift
// (BoardTodayTileView: init, colour, isHot); GTK: boardTileView
// (window/board_today.go). A count tile of the Today page.

using System.Globalization;
using Malachi.Core.Boards;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>A count tile of the board's Today page ("3 Hot").</summary>
public sealed partial class BoardTodayTile : UserControl
{
    /// <summary>An empty tile.</summary>
    public BoardTodayTile()
    {
        InitializeComponent();
    }

    /// <summary>The kind of tile, for its AutomationId: a state's name, or Commitments.</summary>
    public static string KindName(Board.Tile tile) =>
        tile is null ? "" : tile.Kind == Board.TileKind.Commitments ? "Commitments" : tile.State.ToString();

    /// <summary>Shows <paramref name="tile"/>.</summary>
    public void Show(Board.Tile tile)
    {
        if (tile is null)
        {
            return;
        }
        var count = tile.Count.ToString(CultureInfo.CurrentCulture);
        CountText.Text = count;
        TitleText.Text = tile.Title;
        // "Hot: 3 cases", as the macOS tile's VoiceOver label and GTK's
        // tooltip (Board.Text.TileToolTip).
        var spoken = tile.ToolTip.Length > 0 ? tile.ToolTip : Board.Text.TileToolTip(tile);
        AutomationProperties.SetName(this, spoken);
        ToolTipService.SetToolTip(this, spoken);
        VisualStateManager.GoToState(this, KindName(tile), false);
    }
}
