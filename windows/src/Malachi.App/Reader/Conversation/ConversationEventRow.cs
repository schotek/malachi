// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/conversation_rows.go (convEventRow,
// setConvDate, convTruncatedRow); macOS: MessageView/ConversationEventRow.swift.
// A status or assignee change of an issue (ConversationItemKind.Event): not
// a card but a compact native row, never a web view and never unread: one
// line per change in small secondary text, who made it, and the time at the
// trailing edge, in the column of the cards' texts. The name gives way (it
// is cut) before the lines wrap. Every text is plain: it comes from the
// site.

using System;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using L = Malachi.Core.Model.ConversationLayout;

namespace Malachi.App.Reader.Conversation;

/// <summary>A status or assignee change of an issue in the conversation view.</summary>
internal sealed partial class ConversationEventRow : Grid
{
    // The name's widest (conversation_rows.go SetMaxWidthChars(28)).
    private const double SenderMaxWidth = 200;

    private readonly Func<string, Style> look;
    private readonly StackPanel lines = new() { Spacing = 2 };
    private readonly TextBlock sender;
    private readonly TextBlock date;
    private ConversationItem item;

    /// <summary>newConvEventRow: the row of <paramref name="item"/>.</summary>
    public ConversationEventRow(ConversationItem item, bool compact, Func<string, Style> look)
    {
        this.look = look;
        this.item = item;
        Margin = new Thickness(L.CardPaddingH, 0, L.CardPaddingH, 0);
        ColumnSpacing = 8;
        ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        sender = new TextBlock
        {
            Style = look("ConversationCaptionStyle"),
            MaxWidth = SenderMaxWidth,
            MaxLines = 1,
            TextTrimming = TextTrimming.CharacterEllipsis,
            TextWrapping = TextWrapping.NoWrap,
            VerticalAlignment = VerticalAlignment.Top,
        };
        date = new TextBlock { Style = look("ConversationCaptionStyle"), VerticalAlignment = VerticalAlignment.Top };
        SetColumn(sender, 1);
        SetColumn(date, 2);
        Children.Add(lines);
        Children.Add(sender);
        Children.Add(date);
        Update(item, compact);
    }

    /// <summary>update: shows <paramref name="next"/> (the same member, as the model has it now).</summary>
    public void Update(ConversationItem next, bool compact)
    {
        item = next;
        lines.Children.Clear();
        foreach (var text in next.EventLines)
        {
            lines.Children.Add(new TextBlock { Text = text, Style = look("ConversationCaptionStyle"), TextWrapping = TextWrapping.Wrap });
        }
        sender.Text = next.Sender;
        ToolTipService.SetToolTip(sender, next.Sender.Length > 0 ? next.Sender : null);
        sender.Visibility = next.Sender.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        // The row is read as its sentences, then who and when.
        AutomationProperties.SetName(this, next.EventText);
        SetCompact(compact);
    }

    /// <summary>setCompact: the date in full, or the list's short form in a narrow pane with the full one in the tooltip.</summary>
    public void SetCompact(bool compact) => ShowDate(date, item.Message?.Date, compact);

    /// <summary>
    /// setConvDate: <paramref name="date"/> on <paramref name="label"/>: in
    /// full, or in the list's short form with the full one as the tooltip;
    /// hidden without a date.
    /// </summary>
    public static void ShowDate(TextBlock label, DateTimeOffset? date, bool compact)
    {
        if (date is not { } d || d.IsGoZero)
        {
            label.Visibility = Visibility.Collapsed;
            return;
        }
        var full = Format.FormatDateTime(d);
        if (compact)
        {
            label.Text = Format.FormatDate(d, DateTimeOffset.Now);
            ToolTipService.SetToolTip(label, full);
        }
        else
        {
            label.Text = full;
            ToolTipService.SetToolTip(label, null);
        }
        label.Visibility = Visibility.Visible;
    }
}
