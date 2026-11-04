// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/MonthCellView.swift
// (MonthPillView); GTK: the calendar's views follow (docs/calendar.md
// §6.4).
//
// A pill of the Month view (Cal.CellItems: a timed occurrence within one
// day): the colour dot, the time and the title side by side, never one
// sentence, in the theme's texts without a fill; an all-day pill filled
// with its calendar's colour (Windows: the colour itself with a white or
// near-black title, BlockBackground); an unanswered invitation dashed,
// tentative (Windows: Outlook's) a light fill with a hatched bar when all
// day and a hollow dot when timed, declined struck through, a selected
// pill ringed (Windows; macOS fills it). Named by Cal.BlockLabel; its
// AutomationId is "CalendarPill:" and the key.

using Malachi.Core.Calendars;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Shapes;
using Windows.UI;
using Windows.UI.Text;

namespace Malachi.App.Calendars;

/// <summary>A pill of a month cell (macOS <c>MonthPillView</c>).</summary>
internal sealed partial class MonthPillView : OccurrenceView
{
    /// <summary>The AutomationId's prefix of a pill.</summary>
    public const string AutomationPrefix = "CalendarPill:";

    private readonly BlockBackground background = new();
    private readonly TextBlock? time;
    private readonly TextBlock title;

    /// <summary>The pill of <paramref name="pill"/>.</summary>
    public MonthPillView(Cal.Pill pill, Color color, string label, Cal.Env env)
        : base(pill.Occurrence, pill.State, color, label, AutomationPrefix)
    {
        Pill = pill;
        var o = pill.Occurrence;
        // An all-day tentative pill's text starts after its hatched bar.
        var start = HatchedBar ? BlockBackground.BarWidth(pill.State) + 3 : 3;
        var row = new Grid { ColumnSpacing = 3, Margin = new Thickness(start, 0, 2, 0), IsHitTestVisible = false };
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        row.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        if (!o.AllDay)
        {
            // Tentative: a ring in the colour (Windows), the pill unfilled.
            var tentative = pill.State == Cal.BlockState.Tentative;
            row.Children.Add(new Ellipse
            {
                Width = 6,
                Height = 6,
                Fill = tentative ? null : new SolidColorBrush(color),
                Stroke = tentative ? new SolidColorBrush(color) : null,
                StrokeThickness = tentative ? 1.5 : 0,
                VerticalAlignment = VerticalAlignment.Center,
                IsHitTestVisible = false,
            });
        }
        if (!o.AllDay && !pill.ContinuesBefore)
        {
            time = new TextBlock
            {
                Style = CalendarStyles.Get("CalendarSmallTextStyle"),
                VerticalAlignment = VerticalAlignment.Center,
                Text = Cal.Text.TimeText(pill.Start, env),
                TextTrimming = TextTrimming.None,
            };
            Grid.SetColumn(time, 1);
            row.Children.Add(time);
        }
        title = new TextBlock
        {
            Style = CalendarStyles.Get("CalendarPillTextStyle"),
            VerticalAlignment = VerticalAlignment.Center,
            Text = CalendarDisplay.Title(o.Title),
        };
        if (pill.State is Cal.BlockState.Declined or Cal.BlockState.Cancelled)
        {
            title.TextDecorations = TextDecorations.Strikethrough;
        }
        Grid.SetColumn(title, 2);
        row.Children.Add(title);
        var root = new Grid { Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent) };
        root.Children.Add(background);
        root.Children.Add(row);
        Content = root;
        Height = CalendarMetrics.PillLine - 1;
        Repaint();
    }

    /// <summary>What Core chose for the cell.</summary>
    public Cal.Pill Pill { get; }

    // An all-day tentative pill: light, with the hatched bar (Windows).
    private bool HatchedBar => Pill.Occurrence.AllDay && State == Cal.BlockState.Tentative;

    /// <inheritdoc/>
    protected override void Paint(ElementTheme theme)
    {
        // A timed pill is filled only when unanswered; tentative marks it by its dot.
        var filled = Pill.Occurrence.AllDay || State == Cal.BlockState.Unanswered;
        BlockBackground.Ink(background.Paint(Color, State, Selected, theme, 3, showBar: HatchedBar, filled: filled), time, title);
    }
}
