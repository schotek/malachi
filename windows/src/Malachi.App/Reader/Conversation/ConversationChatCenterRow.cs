// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/ConversationChatRow.swift
// (ConversationChatCenterRow); GTK: ui/internal/window/conversation_chat_row.go
// (chatCenterRow). A row of the chat without a bubble
// (docs/plans/chat-view.md D1): an issue's status or assignee change, or the
// row of older messages left out, centred in the column no wider than a
// bubble, in small secondary text: the lines of the change
// (ConversationItem.EventLines) and under them who made it and when (the
// list's short date in a narrow column, the full one in the tooltip), the
// texts of the timeline's event row (ConversationEventRow); the row of
// older messages its sentence. Plain text only; the row is read as the
// change's sentences, as the timeline's event row is.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Malachi.Core.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using L = Malachi.Core.Model.ConversationLayout;

namespace Malachi.App.Reader.Conversation;

/// <summary>A centred row of the chat: an issue's event, or the row of older messages.</summary>
internal sealed partial class ConversationChatCenterRow : Grid
{
    private readonly Func<string, Style> look;
    private readonly StackPanel stack;
    private readonly StackPanel lines = new() { Spacing = 2 };
    private readonly TextBlock meta;
    private ConversationItem? item;
    private bool compactDate;
    private double column = -1;

    /// <summary>An empty row, styled from <paramref name="look"/>.</summary>
    public ConversationChatCenterRow(Func<string, Style> look)
    {
        ArgumentNullException.ThrowIfNull(look);
        this.look = look;
        Padding = new Thickness(0, L.ItemGap, 0, 0);
        meta = new TextBlock
        {
            Style = look("ConversationCaptionStyle"),
            HorizontalAlignment = HorizontalAlignment.Center,
            TextAlignment = TextAlignment.Center,
            MaxLines = 1,
            TextTrimming = TextTrimming.CharacterEllipsis,
            TextWrapping = TextWrapping.NoWrap,
            Visibility = Visibility.Collapsed,
        };
        lines.HorizontalAlignment = HorizontalAlignment.Center;
        stack = new StackPanel { Spacing = 2, HorizontalAlignment = HorizontalAlignment.Center };
        stack.Children.Add(lines);
        stack.Children.Add(meta);
        Children.Add(stack);
    }

    /// <summary>The short date of the list instead of the full date and time.</summary>
    public bool CompactDate
    {
        get => compactDate;
        set
        {
            if (value == compactDate)
            {
                return;
            }
            compactDate = value;
            if (item is not null)
            {
                ShowEvent(item);
            }
        }
    }

    /// <summary>An issue event (<paramref name="next"/>, as the model has it now).</summary>
    public void ShowEvent(ConversationItem next)
    {
        ArgumentNullException.ThrowIfNull(next);
        item = next;
        ShowLines(next.EventLines);
        var full = "";
        var shortDate = "";
        if (next.Message?.Date is { } d && !d.IsGoZero)
        {
            full = Format.FormatDateTime(d);
            shortDate = Format.FormatDate(d, DateTimeOffset.Now);
        }
        var when = compactDate && full.Length > 0 ? shortDate : full;
        meta.Text = string.Join(" · ", new[] { next.Sender, when }.Where(t => t.Length > 0));
        ToolTipService.SetToolTip(meta, compactDate && full.Length > 0 ? full : null);
        meta.Visibility = meta.Text.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        // The row is read as its sentences, then who and when.
        AutomationProperties.SetName(this, next.EventText);
    }

    /// <summary>The row of older messages: its sentence.</summary>
    public void ShowText(string text)
    {
        item = null;
        ShowLines([text ?? ""]);
        meta.Visibility = Visibility.Collapsed;
        AutomationProperties.SetName(this, text ?? "");
    }

    /// <summary>The column is <paramref name="width"/> wide: the row's text no wider than a bubble.</summary>
    public void SetColumnWidth(double width)
    {
        if (width <= 0 || Math.Abs(width - column) < 0.5)
        {
            return;
        }
        column = width;
        stack.MaxWidth = Math.Max(L.ChatBubbleMax(width), 1);
    }

    private void ShowLines(IReadOnlyList<string> texts)
    {
        lines.Children.Clear();
        foreach (var text in texts)
        {
            lines.Children.Add(new TextBlock
            {
                Text = text,
                Style = look("ConversationCaptionStyle"),
                TextWrapping = TextWrapping.Wrap,
                TextAlignment = TextAlignment.Center,
                HorizontalAlignment = HorizontalAlignment.Center,
            });
        }
    }
}
