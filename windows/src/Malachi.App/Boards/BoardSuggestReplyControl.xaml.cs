// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Board/BoardSuggestReplyControl.swift
// (apply(_:case:), suggestClicked, stopClicked); GTK:
// window/board_suggest_reply.go (boardSuggestReplyControl,
// renderSuggestReply's control branch, suggestBoardReply). One instance per
// detail, kept across the detail's refreshes, so what the user typed stays
// while the same case is shown; another case empties the field. The view
// model is Core's (Board.SuggestReplyView, BoardReplyController.View): a
// missing or signed-out Claude Code (or Codex) disables the control with the
// rewrite's hint pointing to Settings → AI, as the compose rewrite and the
// other two clients do. The field is capped at the instruction's length
// (Assistant.SuggestReplyMaxInstruction; Core cuts it again).

using System;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Boards;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Boards;

/// <summary>The case detail's Suggest Reply.</summary>
public sealed partial class BoardSuggestReplyControl : UserControl
{
    private BoardCaseId? shownCase;

    /// <summary>An empty control; <see cref="Apply"/> fills it.</summary>
    public BoardSuggestReplyControl()
    {
        InitializeComponent();
        Field.MaxLength = Assistant.SuggestReplyMaxInstruction;
    }

    /// <summary>✦ Suggest Reply, or Enter in the field, with the field's text.</summary>
    public event EventHandler<string>? Suggest;

    /// <summary>Stop while the request runs for this case.</summary>
    public event EventHandler? Stop;

    /// <summary>
    /// The control gave up the keyboard while not running (it went
    /// disabled under it): the detail gives it to Reply (GTK replyButton).
    /// </summary>
    public event EventHandler? KeyboardLost;

    /// <summary>What the control shows now.</summary>
    public Board.SuggestReplyView ShownView { get; private set; } = Board.SuggestReplyView.Hidden;

    /// <summary>Shows <paramref name="v"/> for case <paramref name="id"/>; another case empties the field.</summary>
    public void Apply(Board.SuggestReplyView v, BoardCaseId id)
    {
        ArgumentNullException.ThrowIfNull(v);
        if (shownCase != id)
        {
            shownCase = id;
            Field.Text = "";
        }
        ShownView = v;
        Field.PlaceholderText = v.Placeholder;
        AutomationProperties.SetName(Field, v.Placeholder);
        SuggestButton.Content = v.Title;
        AutomationProperties.SetName(SuggestButton, v.Title);
        StopButton.Content = v.Stop;
        AutomationProperties.SetName(StopButton, v.Stop);
        ProgressText.Text = v.Progress;
        var hadFocus = FocusIn(InputRow);
        InputRow.Visibility = v.Running ? Visibility.Collapsed : Visibility.Visible;
        RunningRow.Visibility = v.Running ? Visibility.Visible : Visibility.Collapsed;
        Spinner.IsActive = v.Running;
        Field.IsEnabled = v.Enabled;
        SuggestButton.IsEnabled = v.Enabled;
        if (!v.Enabled && hadFocus)
        {
            // The field gives up the keyboard as it goes insensitive: to Stop
            // while the request runs, else to the detail's Reply.
            if (!(v.Running && StopButton.Focus(FocusState.Programmatic)))
            {
                KeyboardLost?.Invoke(this, EventArgs.Empty);
            }
        }
        NoteText.Text = v.Note;
        NoteText.Visibility = v.Note.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        NoteText.Foreground = (Brush)Application.Current.Resources[
            v.NoteIsFailure ? "SystemFillColorCriticalBrush" : "TextFillColorSecondaryBrush"];
    }

    private void OnSuggestClick(object sender, RoutedEventArgs e) => Ask();

    private void OnFieldKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == Windows.System.VirtualKey.Enter)
        {
            e.Handled = true;
            Ask();
        }
    }

    private void Ask()
    {
        if (ShownView.Enabled && !ShownView.Running)
        {
            Suggest?.Invoke(this, Field.Text);
        }
    }

    private void OnStopClick(object sender, RoutedEventArgs e) => Stop?.Invoke(this, EventArgs.Empty);

    private bool FocusIn(DependencyObject container)
    {
        if (XamlRoot is null)
        {
            return false;
        }
        for (var d = FocusManager.GetFocusedElement(XamlRoot) as DependencyObject; d is not null; d = VisualTreeHelper.GetParent(d))
        {
            if (ReferenceEquals(d, container))
            {
                return true;
            }
        }
        return false;
    }
}
