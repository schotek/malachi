// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationLayout.swift
// (Metrics.chatGapMin, Metrics.chatGapRatio, chatGap, chatBubbleMax) and
// ConversationChatBody.swift (chatEndSlack, chatAtEnd, chatColumn,
// chatCompactDates, chatResize, chatEndTop, quickReplyIconOnly). Swift-first:
// ui/internal/conversation/chat.go leaves where the bubbles stand and how
// wide they are to the clients; GTK has the same measures in its window
// package. Swift's chatResize is ChatResizeFor here: the type it returns is
// ChatResize.
//
// The arithmetic of the chat layout of the conversation view
// (docs/plans/chat-view.md D2, D4): the empty side of a row and the widest
// a bubble gets, the column of the chat in a pane, the short dates of a
// narrow bubble, and whether the viewport shows the chat's end.

using System;

namespace Malachi.Core.Model;

public static partial class ConversationLayout
{
    /// <summary>The chat's empty side of a row (<see cref="ChatGap"/>): at least this.</summary>
    public const double ChatGapMin = 24;

    /// <summary>The chat's empty side of a row (<see cref="ChatGap"/>): else this share of the column.</summary>
    public const double ChatGapRatio = 0.2;

    /// <summary>
    /// How close to the end of the chat the viewport's bottom may be and
    /// still count as at the end (D2: an arrival keeps a chat at its end in
    /// view).
    /// </summary>
    public const double ChatEndSlack = 4;

    /// <summary>
    /// The chat's empty side of a row (docs/plans/chat-view.md D4): the
    /// larger of <see cref="ChatGapMin"/> and <see cref="ChatGapRatio"/> of
    /// the column, continuous (the two meet at a column of 120).
    /// <paramref name="column"/> is the clamp's inner width
    /// (<see cref="ChatColumn"/>): the column at most <see cref="MaxWidth"/>,
    /// less the two <see cref="SideInset"/>.
    /// </summary>
    public static double ChatGap(double column) => Math.Max(ChatGapMin, ChatGapRatio * column);

    /// <summary>
    /// The widest a chat bubble gets in a column <paramref name="column"/>
    /// wide (the clamp's inner width, as <see cref="ChatGap"/>): a row of the
    /// chat is the avatar's gutter on the bubble's side
    /// (<see cref="Avatar"/> + <see cref="GutterGap"/>), the bubble, and the
    /// empty side of at least <see cref="ChatGap"/>; never negative. A short
    /// bubble shrinks to its text, not below its header.
    /// </summary>
    public static double ChatBubbleMax(double column) => Math.Max(0, column - Avatar - GutterGap - ChatGap(column));

    /// <summary>
    /// The chat's column in a pane <paramref name="pane"/> wide: the clamp's
    /// inner width (at most <see cref="MaxWidth"/>, less the two
    /// <see cref="SideInset"/>), the column of <see cref="ChatBubbleMax"/>;
    /// never negative.
    /// </summary>
    public static double ChatColumn(double pane) => Math.Max(0, Math.Min(pane, MaxWidth) - (2 * SideInset));

    /// <summary>
    /// Whether the chat's bubbles in a column <paramref name="column"/> wide
    /// (<see cref="ChatColumn"/>) show the list's short date: their widest
    /// header, <see cref="ChatBubbleMax"/> less the padding, is narrower than
    /// <see cref="CompactHeader"/> (the cards' rule, <see cref="CompactDates"/>,
    /// measured on the bubble, which is narrower than a card).
    /// </summary>
    public static bool ChatCompactDates(double column) => ChatBubbleMax(column) - (2 * CardPaddingH) < CompactHeader;

    /// <summary>
    /// Whether the quick-reply bar under the chat in a pane
    /// <paramref name="pane"/> wide shows its mode button as the icon alone
    /// (docs/plans/chat-view.md Q11): when the bubbles show the short date
    /// (<see cref="ChatCompactDates"/> of the pane's <see cref="ChatColumn"/>),
    /// the field keeps the room the label would take.
    /// </summary>
    public static bool QuickReplyIconOnly(double pane) => ChatCompactDates(ChatColumn(pane));

    /// <summary>
    /// Whether a viewport <paramref name="viewportHeight"/> tall whose top is
    /// at <paramref name="top"/> shows the end of a document
    /// <paramref name="documentHeight"/> tall (within
    /// <paramref name="slack"/>); a document no taller than the viewport is
    /// always at its end.
    /// </summary>
    public static bool ChatAtEnd(double top, double viewportHeight, double documentHeight, double slack = ChatEndSlack) =>
        top + viewportHeight >= documentHeight - slack;

    /// <summary>
    /// What the viewport going from <paramref name="before"/> to
    /// <paramref name="after"/> (width, height) asks for (Swift
    /// <c>chatResize(from:to:pinned:wasAtEnd:)</c>): null when neither
    /// changed (by half a point or more). A change of the height alone moves
    /// nothing but the end: a chat that showed its end before
    /// (<paramref name="wasAtEnd"/>, measured before the change, or
    /// <paramref name="pinned"/>) shows it after.
    /// </summary>
    public static ChatResize? ChatResizeFor(
        (double Width, double Height) before, (double Width, double Height) after, bool pinned, bool wasAtEnd)
    {
        var width = Math.Abs(after.Width - before.Width) >= 0.5;
        var height = Math.Abs(after.Height - before.Height) >= 0.5;
        if (!width && !height)
        {
            return null;
        }
        return new ChatResize(width, pinned || wasAtEnd);
    }

    /// <summary>
    /// Where the viewport's top goes to show the end of the document (the
    /// chat opens there, <see cref="Conversation.ChatScrollTo"/>): never above
    /// its top.
    /// </summary>
    public static double ChatEndTop(double viewportHeight, double documentHeight) => Math.Max(0, documentHeight - viewportHeight);
}
