// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardTodayViewController.swift
// (BoardDueItemView: init, pressed); GTK: dueItemButton
// (window/board_today.go). A deadline of the Today page; its texts are the
// view model's (Board.DueItem, cleaned by Core), set as TextBlock.Text.

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>A deadline of the board's Today page.</summary>
public sealed partial class BoardDueItemView : UserControl
{
    private readonly BoardCaseId id;

    /// <summary>The deadline <paramref name="item"/>, red while <paramref name="overdue"/>.</summary>
    public BoardDueItemView(Board.DueItem item, bool overdue)
    {
        ArgumentNullException.ThrowIfNull(item);
        InitializeComponent();
        id = item.CaseId;
        DueText.Text = item.Label;
        DueChip.Visibility = Show(item.Label.Length > 0);
        MarkText.Text = item.TitleIsAssistant ? Board.Text.AssistantMark : "";
        MarkText.Visibility = Show(item.TitleIsAssistant);
        TitleText.Text = item.Title;
        QuoteText.Text = item.Quote.Length == 0 ? "" : Board.Text.Quoted(item.Quote);
        QuoteText.Visibility = Show(item.Quote.Length > 0);
        PersonText.Text = item.Person;
        PersonText.Visibility = Show(item.Person.Length > 0);
        // BoardDueItemView's label: the title, the deadline, the person.
        var spoken = string.Join(". ", new[] { item.SpokenTitle, Board.Text.SpokenDue(item.Label), item.Person }.Where(s => s.Length > 0)); // Windows-only string: the separator
        AutomationProperties.SetName(ItemButton, spoken);
        VisualStateManager.GoToState(this, overdue ? "DueOverdue" : "DueNormal", false);
    }

    /// <summary>The deadline was clicked: its case is to be selected.</summary>
    public event EventHandler<BoardCaseId>? Chosen;

    private void OnClick(object sender, RoutedEventArgs e) => Chosen?.Invoke(this, id);

    private static Visibility Show(bool on) => on ? Visibility.Visible : Visibility.Collapsed;
}
