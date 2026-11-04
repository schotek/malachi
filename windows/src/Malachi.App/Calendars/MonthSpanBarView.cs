// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/MonthCellView.swift
// (MonthSpanBarView); GTK: the calendar's views follow (docs/calendar.md
// §6.4).
//
// An all-day or multi-day occurrence as one bar across the days it covers
// in a week row of the Month view (Cal.MonthLanes, the owner's decision of
// 2026-10-16): its calendar's colour with the colour bar at the left
// (Windows: the colour itself with a white or near-black title, tentative
// a light fill with a hatched bar, BlockBackground), rounded only at the
// ends where it does not continue, the title once. Named by Cal.BlockLabel;
// its AutomationId is "CalendarMonthBar:" and the key.

using Malachi.Core.Calendars;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Windows.UI;
using Windows.UI.Text;

namespace Malachi.App.Calendars;

/// <summary>A spanning bar of a week row of the Month view (macOS <c>MonthSpanBarView</c>).</summary>
internal sealed partial class MonthSpanBarView : OccurrenceView
{
    /// <summary>The AutomationId's prefix of a month bar.</summary>
    public const string AutomationPrefix = "CalendarMonthBar:";

    private readonly BlockBackground background = new();
    private readonly TextBlock title;

    /// <summary>The bar of <paramref name="span"/> in week row <paramref name="row"/>.</summary>
    public MonthSpanBarView(Cal.MonthSpan span, int row, Color color, string label)
        : base(span.Occurrence, span.State, color, label, AutomationPrefix)
    {
        Span = span;
        Row = row;
        title = new TextBlock
        {
            Style = CalendarStyles.Get("CalendarPillTextStyle"),
            Margin = new Thickness(span.ContinuesBefore ? 5 : BlockBackground.BarWidth(span.State) + 5, 0, 4, 0),
            VerticalAlignment = VerticalAlignment.Center,
            Text = CalendarDisplay.Clean(span.Title),
        };
        if (span.State is Cal.BlockState.Declined or Cal.BlockState.Cancelled)
        {
            title.TextDecorations = TextDecorations.Strikethrough;
        }
        var root = new Grid { Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent) };
        root.Children.Add(background);
        root.Children.Add(title);
        Content = root;
        Repaint();
    }

    /// <summary>What Core placed: the lane and the columns.</summary>
    public Cal.MonthSpan Span { get; }

    /// <summary>The week row of the month (0–5).</summary>
    public int Row { get; }

    /// <inheritdoc/>
    protected override void Paint(ElementTheme theme) =>
        BlockBackground.Ink(
            background.Paint(Color, State, Selected, theme, 3, continuesBefore: Span.ContinuesBefore, continuesAfter: Span.ContinuesAfter),
            title);
}
