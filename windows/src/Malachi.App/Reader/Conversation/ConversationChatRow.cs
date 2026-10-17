// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/ConversationChatRow.swift
// (ConversationChatRow); GTK: ui/internal/window/conversation_chat_row.go
// (chatRow, newChatRow, setMax, showMessage, showDraft). One bubble of the
// chat across the column (docs/plans/chat-view.md D4, D5): the gap above
// it, the sender's avatar on the bubble's outer side (leading for someone
// else's message, trailing for the user's own, top aligned with the
// bubble), then the bubble at that edge, as wide as its text up to
// ConversationLayout.ChatBubbleMax of the column; the rest of the row is the
// empty side. The avatar is decoration: the bubble names its sender to
// automation.

using System;
using Malachi.App.Main;
using Malachi.Core.Api;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using L = Malachi.Core.Model.ConversationLayout;

namespace Malachi.App.Reader.Conversation;

/// <summary>A bubble's row of the chat: the avatar and the bubble.</summary>
internal sealed partial class ConversationChatRow : Grid
{
    private readonly Avatar avatar;
    private double column = -1;

    /// <summary>
    /// The row of <paramref name="bubble"/> for member <paramref name="id"/>
    /// or draft <paramref name="draftId"/>, on the right
    /// (<paramref name="right"/>: the user's own) or the left.
    /// </summary>
    public ConversationChatRow(MessageId? id, DraftId? draftId, bool right, ConversationBubbleView bubble)
    {
        ArgumentNullException.ThrowIfNull(bubble);
        Id = id;
        DraftId = draftId;
        Right = right;
        Bubble = bubble;
        Padding = new Thickness(0, L.ItemGap, 0, 0);
        avatar = new Avatar { Size = L.Avatar, VerticalAlignment = VerticalAlignment.Top };
        AutomationProperties.SetAccessibilityView(avatar, AccessibilityView.Raw);
        bubble.VerticalAlignment = VerticalAlignment.Top;
        if (right)
        {
            ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            bubble.HorizontalAlignment = HorizontalAlignment.Right;
            bubble.Margin = new Thickness(0, 0, L.GutterGap, 0);
            SetColumn(avatar, 1);
        }
        else
        {
            ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            bubble.HorizontalAlignment = HorizontalAlignment.Left;
            bubble.Margin = new Thickness(L.GutterGap, 0, 0, 0);
            SetColumn(bubble, 1);
        }
        Children.Add(avatar);
        Children.Add(bubble);
    }

    /// <summary>The member shown (null for a draft's row).</summary>
    public MessageId? Id { get; }

    /// <summary>The draft shown (null for a message's row).</summary>
    public DraftId? DraftId { get; }

    /// <summary>The bubble stands on the right: the user's own.</summary>
    public bool Right { get; }

    /// <summary>The bubble.</summary>
    public ConversationBubbleView Bubble { get; }

    /// <summary>
    /// The avatar of <paramref name="name"/> (hostile text, only ever drawn as
    /// initials): monochrome as the list's setting says, the user's own
    /// tinted with the accent colour.
    /// </summary>
    public void ShowAvatar(string name, bool monochrome)
    {
        avatar.Text = name;
        avatar.Monochrome = monochrome;
        avatar.Accent = Right;
    }

    /// <summary>The column is <paramref name="width"/> wide (the clamp less its side insets): the bubble's widest.</summary>
    public void SetColumnWidth(double width)
    {
        if (width <= 0 || Math.Abs(width - column) < 0.5)
        {
            return;
        }
        column = width;
        Bubble.MaxWidth = Math.Max(L.ChatBubbleMax(width), (2 * L.CardPaddingH) + 1);
    }
}
