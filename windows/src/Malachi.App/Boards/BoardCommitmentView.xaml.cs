// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardViews.swift
// (BoardCommitmentContentView.configure, onTick); GTK:
// renderDetailCommitments (window/board_detail.go). The tick runs
// OnTick, which the list or the detail sets: the controller marks the
// promise done (BoardController.SetCommitmentDone) and the row leaves with
// the next view model.

using System;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>A commitment of the user's, with its tick.</summary>
public sealed partial class BoardCommitmentView : UserControl
{
    /// <summary>The commitment shown.</summary>
    public static readonly DependencyProperty CommitmentProperty = DependencyProperty.Register(
        nameof(Commitment), typeof(Board.CommitmentRow), typeof(BoardCommitmentView), new PropertyMetadata(null, OnCommitmentChanged));

    /// <summary>An empty row.</summary>
    public BoardCommitmentView()
    {
        InitializeComponent();
        ToolTipService.SetToolTip(Tick, Board.Text.MarkPromiseDone);
        AutomationProperties.SetName(Tick, Board.Text.MarkPromiseDone);
        Loaded += (_, _) => Apply();
    }

    /// <summary>The commitment shown.</summary>
    public Board.CommitmentRow? Commitment
    {
        get => (Board.CommitmentRow?)GetValue(CommitmentProperty);
        set => SetValue(CommitmentProperty, value);
    }

    /// <summary>The tick was clicked: the commitment is done (true) or open again.</summary>
    public Action<BoardCommitmentId, bool>? OnTick { get; set; }

    private static void OnCommitmentChanged(DependencyObject d, DependencyPropertyChangedEventArgs e) => ((BoardCommitmentView)d).Apply();

    private void Apply()
    {
        if (Commitment is not { } c)
        {
            return;
        }
        Tick.IsChecked = false;
        PromiseText.Text = c.Text;
        QuoteText.Text = c.Quote.Length == 0 ? "" : Board.Text.Quoted(c.Quote);
        QuoteText.Visibility = c.Quote.Length == 0 ? Visibility.Collapsed : Visibility.Visible;
        SourceMark.Text = Board.Text.AssistantMark;
        SourceMark.Visibility = c.FromIsAssistant && c.From.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        SourceText.Text = c.From;
        AutomationProperties.SetName(SourceText, c.SpokenFrom);
        DueText.Text = c.Due;
        DueChip.Visibility = c.Due.Length == 0 ? Visibility.Collapsed : Visibility.Visible;
        AutomationProperties.SetName(this, c.Text);
    }

    private void OnTickClick(object sender, RoutedEventArgs e)
    {
        if (Commitment is { } c)
        {
            OnTick?.Invoke(c.Id, Tick.IsChecked == true);
        }
    }
}
