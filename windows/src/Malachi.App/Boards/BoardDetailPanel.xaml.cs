// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardPageViewController.swift
// (panelWidth, panelMargin, updatePanel, cancelOperation) and
// window/board_panel.go (wirePanel's Escape, boardPanelShown). The page
// says when it shows (Board.ViewModel.ShowsPanel on a board with cases)
// and how wide the page is; Escape asks the page to close it, which
// clears the selection.

using System;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using WinKey = Windows.System.VirtualKey;

namespace Malachi.App.Boards;

/// <summary>The sliding panel of the board's detail.</summary>
public sealed partial class BoardDetailPanel : UserControl
{
    /// <summary>The panel's preferred width.</summary>
    public const double PanelWidth = 640;

    /// <summary>What it leaves of the page beside it at least.</summary>
    public const double PanelMargin = 40;

    /// <summary>A hidden, empty panel.</summary>
    public BoardDetailPanel()
    {
        InitializeComponent();
    }

    /// <summary>Escape in the panel: the page closes it.</summary>
    public event EventHandler? CloseRequested;

    /// <summary>Where the page puts the detail while the panel shows.</summary>
    public ContentControl DetailHost => Host;

    /// <summary>Whether the panel is in.</summary>
    public bool IsShown => Visibility == Visibility.Visible;

    /// <summary>Slides the panel in or out.</summary>
    public void SetShown(bool shown) => Visibility = shown ? Visibility.Visible : Visibility.Collapsed;

    /// <summary>The page is <paramref name="pageWidth"/> wide: the panel takes up to 640 of it, leaving 40.</summary>
    public void Fit(double pageWidth) => Width = Math.Max(0, Math.Min(PanelWidth, pageWidth - PanelMargin));

    // Escape closes the panel, unless a menu or a field inside took it.
    private void OnPreviewKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == WinKey.Escape && IsShown)
        {
            e.Handled = true;
            CloseRequested?.Invoke(this, EventArgs.Empty);
        }
    }
}
