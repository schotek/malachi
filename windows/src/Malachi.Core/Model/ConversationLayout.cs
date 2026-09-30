// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ConversationLayout.swift (live,
// anchoredTop, pageTop, displayOrder, root, Metrics, rails, cardWidth,
// compactDates); GTK: ui/internal/window/conversation_layout.go (liveCards,
// anchoredTop, pageTop, convDisplayOrder, convRoot, convRails,
// convCardWidth, convCompactDates and the constants), conversation_card.go
// (picturesArrived) and conversation_rows.go (clampText). GTK's order is the
// one shown (convDisplayOrder): what opened the conversation first, the rest
// newest first.
//
// The arithmetic of the conversation view that needs no WinUI: which cards
// are near enough to the viewport to hold a body and a web view, where the
// viewport goes to keep an item in place and for a page of Space, the
// timeline in the gutter beside the cards (which item gets an avatar and
// which a dot, where the line runs), the short dates of a narrow pane. How a
// card's web view follows the height of its document is WebHeightGovernor.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;

namespace Malachi.Core.Model;

/// <summary>The measures and rules of the conversation view.</summary>
public static partial class ConversationLayout
{
    /// <summary>convLiveScreens: how many viewport heights above and below the visible part of the stack count as near.</summary>
    public const double LiveScreens = 2.0;

    /// <summary>convMaxLiveWebViews: the most web views the pane keeps at once.</summary>
    public const int MaxLiveWebViews = 8;

    /// <summary>convEstimatedBodyHeight: the height a card's body is given until it is known.</summary>
    public const double EstimatedBodyHeight = 160;

    /// <summary>convInitialWebHeight: the height a web view starts from before its document reports one.</summary>
    public const double InitialWebHeight = 120;

    /// <summary>convMaxWidth: the widest the column gets, the gutter included.</summary>
    public const double MaxWidth = 900;

    /// <summary>convSideInset: between the pane's edge and the column, on both sides.</summary>
    public const double SideInset = 16;

    /// <summary>convAvatar: the avatar of a message, which is also the gutter's width.</summary>
    public const double Avatar = 28;

    /// <summary>convGutterGap: between the gutter and the cards.</summary>
    public const double GutterGap = 12;

    /// <summary>convItemGap: between two items of the stack.</summary>
    public const double ItemGap = 12;

    /// <summary>convDot: the dot of an event and of the row of older messages.</summary>
    public const double Dot = 7;

    /// <summary>convLine: the line's width.</summary>
    public const double Line = 1;

    /// <summary>convLineBreak: between the line's end and the avatar or dot it runs to.</summary>
    public const double LineBreak = 3;

    /// <summary>convCardRadius: a card's corner radius.</summary>
    public const double CardRadius = 10;

    /// <summary>convCardPaddingV: the padding above and below inside a card.</summary>
    public const double CardPaddingV = 10;

    /// <summary>convCardPaddingH: the padding left and right inside a card.</summary>
    public const double CardPaddingH = 14;

    /// <summary>convCompactHeader: a header narrower than this shows the short date of the list instead of the full date and time.</summary>
    public const double CompactHeader = 420;

    /// <summary>
    /// liveCards: the items of the stack at <paramref name="frames"/> near
    /// <paramref name="visible"/> (within <paramref name="screens"/> viewport
    /// heights of it; an empty viewport counts the first screen from its
    /// top), and of those the ones whose body is HTML (<paramref name="html"/>,
    /// same indices as the frames) that get a web view: the nearest to the
    /// visible part first, ties by position, at most <paramref name="limit"/>.
    /// </summary>
    public static Live LiveCards(IReadOnlyList<Span> frames, IReadOnlyList<bool>? html, Span visible, double screens, int limit)
    {
        ArgumentNullException.ThrowIfNull(frames);
        var height = Math.Max(visible.Max - visible.Min, 1);
        var window = Span.Of(visible.Min - (screens * height), visible.Max + (screens * height));
        var near = new HashSet<int>();
        var candidates = new List<(int Index, double Distance)>();
        for (var i = 0; i < frames.Count; i++)
        {
            var f = frames[i];
            if (f.Distance(window) != 0)
            {
                continue;
            }
            near.Add(i);
            if (html is not null && i < html.Count && html[i])
            {
                candidates.Add((i, f.Distance(visible)));
            }
        }
        var web = candidates
            .OrderBy(c => c.Distance)
            .ThenBy(c => c.Index)
            .Take(Math.Max(limit, 0))
            .Select(c => c.Index)
            .ToHashSet();
        return new Live(near, web);
    }

    /// <summary>
    /// anchoredTop: where the viewport's top goes to keep an item in place:
    /// the item that was <paramref name="offset"/> below the viewport's top
    /// is at <paramref name="itemTop"/> now; clamped to the document
    /// (<paramref name="documentHeight"/>, a viewport
    /// <paramref name="viewportHeight"/> tall).
    /// </summary>
    public static double AnchoredTop(double itemTop, double offset, double documentHeight, double viewportHeight)
    {
        var maxTop = Math.Max(0, documentHeight - viewportHeight);
        return Math.Min(Math.Max(0, itemTop + offset), maxTop);
    }

    /// <summary>
    /// pageTop: where the viewport's top goes for one page down (or
    /// <paramref name="up"/>) of Space: the viewport's height less
    /// <paramref name="overlap"/>, at least half a viewport, clamped to the
    /// document.
    /// </summary>
    public static double PageTop(double from, bool up, double viewportHeight, double documentHeight, double overlap)
    {
        var step = Math.Max(viewportHeight - overlap, viewportHeight / 2);
        if (up)
        {
            step = -step;
        }
        var maxTop = Math.Max(0, documentHeight - viewportHeight);
        return Math.Min(Math.Max(0, from + step), maxTop);
    }

    /// <summary>
    /// convDisplayOrder: the order the pane shows the model's items in, as
    /// Jira shows an issue: what opened the conversation first
    /// (<see cref="Root"/>: the issue's description, or the oldest message of
    /// a mail conversation that thread.get did not cut), then the rest newest
    /// first, so that the newest is what the pane opens on right under it,
    /// and the row of older members left out last. The opening card starts
    /// folded while another message card follows it (a conversation of one
    /// message and its status changes shows that message whole). The model
    /// keeps the items oldest first; <paramref name="items"/> is not changed.
    /// </summary>
    public static Display DisplayOrder(IReadOnlyList<ConversationItem> items)
    {
        ArgumentNullException.ThrowIfNull(items);
        var shown = new List<ConversationItem>(items.Count);
        var root = Root(items);
        var shownRoot = -1;
        var folded = false;
        if (root >= 0)
        {
            shown.Add(items[root]);
            shownRoot = 0;
        }
        var truncated = new List<ConversationItem>();
        for (var i = items.Count - 1; i >= 0; i--)
        {
            if (i == root)
            {
                continue;
            }
            if (items[i].Kind == ConversationItemKind.Truncated)
            {
                truncated.Add(items[i]);
                continue;
            }
            shown.Add(items[i]);
            if (items[i].Kind == ConversationItemKind.Message)
            {
                folded = root >= 0;
            }
        }
        shown.AddRange(truncated);
        return new Display(shown, shownRoot, folded);
    }

    /// <summary>
    /// convRoot: the index in <paramref name="items"/> (the model's, oldest
    /// first) of the item that opened the conversation: the description of a
    /// Jira issue wherever it is, else the oldest member when it is a message
    /// card and no older member is left out (no truncated row before it); -1
    /// for none.
    /// </summary>
    public static int Root(IReadOnlyList<ConversationItem> items)
    {
        ArgumentNullException.ThrowIfNull(items);
        for (var i = 0; i < items.Count; i++)
        {
            if (items[i] is { Kind: ConversationItemKind.Message, Message.Issue.Item: var kind } && kind == IssueItemKind.Description)
            {
                return i;
            }
        }
        return items.Count > 0 && items[0].Kind == ConversationItemKind.Message ? 0 : -1;
    }

    /// <summary>
    /// convRails: the timeline of <paramref name="items"/>, one piece each: a
    /// message has its sender's avatar (accent-tinted when it is the user's
    /// own), an event and the row of older messages a dot. The line runs
    /// between the markers, from the first item's to the last item's, so a
    /// conversation of one item has none.
    /// </summary>
    public static IReadOnlyList<Rail> Rails(IReadOnlyList<ConversationItem> items)
    {
        ArgumentNullException.ThrowIfNull(items);
        var rails = new Rail[items.Count];
        for (var i = 0; i < items.Count; i++)
        {
            var message = items[i].Kind == ConversationItemKind.Message;
            rails[i] = new Rail(
                message ? RailMarker.Avatar : RailMarker.Dot,
                Accent: message && items[i].Mine,
                Above: i > 0,
                Below: i < items.Count - 1);
        }
        return rails;
    }

    /// <summary>convCardWidth: the width of the cards in a pane <paramref name="pane"/> wide: the column less the insets and the gutter; never negative.</summary>
    public static double CardWidth(double pane) =>
        Math.Max(0, Math.Min(pane, MaxWidth) - (2 * SideInset) - Avatar - GutterGap);

    /// <summary>convCompactDates: whether the cards of a pane <paramref name="pane"/> wide show the short date (their header is narrower than <see cref="CompactHeader"/>).</summary>
    public static bool CompactDates(double pane) => CardWidth(pane) - (2 * CardPaddingH) < CompactHeader;

    /// <summary>
    /// conversation_card.go <c>picturesArrived</c>: a card reloads its
    /// document only when the pictures of the body it shows were downloaded:
    /// the same message, a new answer, pictures counted on the server before.
    /// </summary>
    public static bool PicturesArrived(MessageBodyResult? before, MessageBodyResult? now) =>
        before is not null && now is not null && !ReferenceEquals(before, now)
        && before.MessageId == now.MessageId && before.RemotePictureCount > 0;

    /// <summary>
    /// conversation_rows.go <c>clampText</c>: <paramref name="s"/> cut to
    /// <paramref name="n"/> characters (Unicode scalars, as Go's runes), for
    /// the status line: a link is content of the mail.
    /// </summary>
    public static string ClampText(string s, int n)
    {
        ArgumentNullException.ThrowIfNull(s);
        if (s.Length <= n)
        {
            return s;
        }
        var count = 0;
        var end = 0;
        foreach (var rune in s.EnumerateRunes())
        {
            if (count == n)
            {
                return s[..end];
            }
            end += rune.Utf16SequenceLength;
            count++;
        }
        return s;
    }
}
