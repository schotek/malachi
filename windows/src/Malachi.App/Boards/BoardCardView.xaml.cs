// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardViews.swift (BoardCardRowView's
// style for a card, BoardCaseContentView.configure); GTK: rowFor's case
// (window/board_columns.go). A case of the Columns style as a card.

using Malachi.Core.Boards;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>A case of the board's columns, on its card.</summary>
public sealed partial class BoardCardView : UserControl
{
    /// <summary>The row shown.</summary>
    public static readonly DependencyProperty RowProperty = DependencyProperty.Register(
        nameof(Row), typeof(Board.Row), typeof(BoardCardView), new PropertyMetadata(null, OnRowChanged));

    /// <summary>An empty card.</summary>
    public BoardCardView()
    {
        InitializeComponent();
        Loaded += (_, _) => Apply();
    }

    /// <summary>The row shown.</summary>
    public Board.Row? Row
    {
        get => (Board.Row?)GetValue(RowProperty);
        set => SetValue(RowProperty, value);
    }

    private static void OnRowChanged(DependencyObject d, DependencyPropertyChangedEventArgs e) => ((BoardCardView)d).Apply();

    private void Apply()
    {
        if (Row is not { } r)
        {
            return;
        }
        RowView.Row = r;
        VisualStateManager.GoToState(this, r.State == Board.State.Hot ? "Hot" : "Plain", false);
    }
}
