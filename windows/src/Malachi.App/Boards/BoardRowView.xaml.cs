// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardViews.swift
// (BoardCaseContentView.configure, the List's non-compact case); GTK:
// widget.BoardRow.SetRow with boardRowData (window/board_list.go). The row's
// texts come from the view model (Board.Row, cleaned by Core) and go into
// TextBlock.Text only; the parts without a value hide.

using Malachi.Core.Boards;
using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>A case of the board's list.</summary>
public sealed partial class BoardRowView : UserControl
{
    /// <summary>The row shown.</summary>
    public static readonly DependencyProperty RowProperty = DependencyProperty.Register(
        nameof(Row), typeof(Board.Row), typeof(BoardRowView), new PropertyMetadata(null, OnRowChanged));

    /// <summary>An empty row.</summary>
    public BoardRowView()
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

    private static void OnRowChanged(DependencyObject d, DependencyPropertyChangedEventArgs e) => ((BoardRowView)d).Apply();

    private void Apply()
    {
        if (Row is not { } r)
        {
            return;
        }
        Bar.State = r.State;
        PersonText.Text = r.Person;
        PersonText.FontWeight = r.Unread ? FontWeights.Bold : FontWeights.SemiBold;
        UnreadDot.Visibility = Show(r.Unread);
        TimeText.Text = r.Time;
        MarkText.Text = r.MarksAssistant ? Board.Text.AssistantMark : "";
        MarkText.Visibility = Show(r.MarksAssistant);
        TitleText.Text = r.Title;
        SnippetText.Text = r.Snippet;
        SnippetText.Visibility = Show(r.Snippet.Length > 0);
        StatePill.State = r.State;
        StatePill.Text = Board.Text.StateName(r.State);
        DueText.Text = r.Due;
        DueChip.Visibility = Show(r.Due.Length > 0);
        VisualStateManager.GoToState(this, r.DueOverdue ? "DueOverdue" : "DueNormal", false);
        AccountText.Text = r.Account;
        ToolTipService.SetToolTip(AccountTag, r.Account.Length > 0 ? r.Account : null);
        AccountTag.Visibility = Show(r.Account.Length > 0);
        // The issue's key and status in one tag, as BoardTag.issue.
        IssueTag.StatusStyle = r.IssueStyle;
        IssueTag.Text = IssueLabel(r.IssueKey, r.IssueStatus);
        RemindText.Text = r.Remind;
        RemindText.Visibility = Show(r.Remind.Length > 0);
        AttachmentIcon.Visibility = Show(r.Attachments);
        CountText.Text = r.CountText;
        CountText.Visibility = Show(r.CountText.Length > 0);
        AutomationProperties.SetName(this, r.Spoken);
    }

    // BoardTag.issue: the key, then the status ("MAL-12 In Progress"); the
    // two are the site's words, not a sentence.
    private static string IssueLabel(string key, string status) =>
        key.Length == 0 ? status : status.Length == 0 ? key : key + " " + status;

    private static Visibility Show(bool on) => on ? Visibility.Visible : Visibility.Collapsed;
}
