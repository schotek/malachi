// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the text cards of macos/Sources/MalachiMail/Board/BoardConversationBlock.swift
// (apply(_:), the excerpt cards and the note with Try Again); GTK:
// window/board_conversation.go (the collapsed cards' snippets). The
// detail's default conversation part (IBoardDetailPart): it rebuilds its
// cards only when the case or its messages changed, so a refresh of the
// board that left them alone keeps the text the user selected.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Boards;

/// <summary>The conversation of the board's detail as plain-text excerpts.</summary>
public sealed partial class BoardConversationExcerpts : UserControl, IBoardDetailPart
{
    private readonly BoardController controller;

    // What the cards show: the case and its messages.
    private (Core.Api.BoardCaseId Id, IReadOnlyList<Board.MessageCard> Messages)? shown;

    /// <summary>The part over <paramref name="controller"/> (Try Again asks it).</summary>
    public BoardConversationExcerpts(BoardController controller)
    {
        ArgumentNullException.ThrowIfNull(controller);
        this.controller = controller;
        InitializeComponent();
        RetryLink.Content = Board.Text.TryAgain;
    }

    /// <inheritdoc/>
    public UIElement View => this;

    /// <inheritdoc/>
    public void Apply(Board.Detail? detail)
    {
        if (detail is null)
        {
            shown = null;
            Cards.Children.Clear();
            Visibility = Visibility.Collapsed;
            return;
        }
        Visibility = Visibility.Visible;
        Heading.Text = detail.ConversationTitle;
        NoteText.Text = detail.MessagesNote;
        NoteRow.Visibility = detail.MessagesNote.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        RetryLink.Visibility = detail.MessagesRetry ? Visibility.Visible : Visibility.Collapsed;
        if (shown is { } s && s.Id == detail.Id && s.Messages.SequenceEqual(detail.Messages))
        {
            return;
        }
        shown = (detail.Id, detail.Messages);
        Cards.Children.Clear();
        foreach (var m in detail.Messages)
        {
            Cards.Children.Add(Card(m));
        }
    }

    // A message: who and when on the first line, the plain text under it.
    private Border Card(Board.MessageCard m)
    {
        var meta = new TextBlock
        {
            // Windows-only string: the separator of a line's parts, as board.go's " · ".
            Text = m.When.Length == 0 ? m.From : m.From + " · " + m.When,
            Style = (Style)Resources["ExcerptMetaStyle"],
        };
        var text = new TextBlock { Text = m.Text, Style = (Style)Resources["ExcerptTextStyle"] };
        var column = new StackPanel { Spacing = 4 };
        column.Children.Add(meta);
        column.Children.Add(text);
        var card = new Border { Style = (Style)Resources["ExcerptCardStyle"], Child = column };
        AutomationProperties.SetName(card, m.From);
        return card;
    }

    private void OnRetryClick(object sender, RoutedEventArgs e) => controller.RetryMessages();
}
