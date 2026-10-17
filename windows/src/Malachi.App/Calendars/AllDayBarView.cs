// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/CalendarAllDayView.swift
// (AllDayBarView); GTK: the calendar's views follow (docs/calendar.md
// §6.4).
//
// One all-day occurrence in the all-day row of Week and Day: a bar across
// its days (Cal.AllDayLanes) in its calendar's colour with the colour bar
// at the left and the title, drawn by its state (Windows: the colour itself
// with a white or near-black title, tentative a light fill with a hatched
// bar, BlockBackground). Named by Cal.BlockLabel; its AutomationId is
// "CalendarAllDay:" and the key.

using Malachi.Core.Calendars;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Windows.UI;
using Windows.UI.Text;

namespace Malachi.App.Calendars;

/// <summary>An all-day occurrence of the all-day row (macOS <c>AllDayBarView</c>).</summary>
internal sealed partial class AllDayBarView : OccurrenceView
{
    /// <summary>The AutomationId's prefix of an all-day bar.</summary>
    public const string AutomationPrefix = "CalendarAllDay:";

    private readonly BlockBackground background = new();
    private readonly TextBlock title;

    /// <summary>The bar of <paramref name="bar"/>.</summary>
    public AllDayBarView(Cal.Bar bar, Color color, string label)
        : base(bar.Occurrence, bar.State, color, label, AutomationPrefix)
    {
        Bar = bar;
        title = new TextBlock
        {
            Style = CalendarStyles.Get(bar.State == Cal.BlockState.Cancelled ? "CalendarBlockTitleDimStyle" : "CalendarBlockTitleStyle"),
            FontSize = 11,
            Margin = new Thickness(BlockBackground.BarWidth(bar.State) + 4, 0, 4, 0),
            VerticalAlignment = VerticalAlignment.Center,
            Text = CalendarDisplay.Title(bar.Occurrence.Title),
        };
        if (bar.State is Cal.BlockState.Declined or Cal.BlockState.Cancelled)
        {
            title.TextDecorations = TextDecorations.Strikethrough;
        }
        var root = new Grid { Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent) };
        root.Children.Add(background);
        root.Children.Add(title);
        Content = root;
        Height = CalendarMetrics.LaneHeight;
        Repaint();
    }

    /// <summary>What Core placed: the lane and the days.</summary>
    public Cal.Bar Bar { get; }

    /// <inheritdoc/>
    protected override void Paint(ElementTheme theme) => BlockBackground.Ink(background.Paint(Color, State, Selected, theme, 4), title);
}
