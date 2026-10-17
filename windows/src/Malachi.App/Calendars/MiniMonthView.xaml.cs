// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/MiniMonthView.swift (set,
// draw's band and today's circle, mouseDown, step); GTK: the calendar's
// sidebar follows (docs/calendar.md §6.4).
//
// The sidebar's month: Cal.MiniMonth's title, headers and days, the band
// of the shown range and today; a click on a day goes to it
// (CalendarController.GoTo), the arrows change the month alone
// (StepMiniMonth). Its tooltips are Cal.Text.Previous and Next.

using System;
using Malachi.Core.Calendars;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Shapes;

namespace Malachi.App.Calendars;

/// <summary>The sidebar's month (macOS <c>MiniMonthView</c>).</summary>
public sealed partial class MiniMonthView : UserControl
{
    private readonly TextBlock[] headers = new TextBlock[7];
    private readonly Day[] days = new Day[42];
    private Cal.MiniMonth? month;

    /// <summary>An empty month; <see cref="Set"/> fills it.</summary>
    public MiniMonthView()
    {
        InitializeComponent();
        foreach (var (button, text) in new[] { (PreviousButton, Cal.Text.Previous), (NextButton, Cal.Text.Next) })
        {
            AutomationProperties.SetName(button, text);
            ToolTipService.SetToolTip(button, text);
        }
        for (var i = 0; i < 7; i++)
        {
            HeaderGrid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            DaysGrid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            var h = new TextBlock
            {
                Style = CalendarStyles.Get("CalendarSmallTextStyle"),
                FontSize = 10,
                HorizontalAlignment = HorizontalAlignment.Center,
                VerticalAlignment = VerticalAlignment.Center,
            };
            Grid.SetColumn(h, i);
            headers[i] = h;
            HeaderGrid.Children.Add(h);
        }
        for (var r = 0; r < 6; r++)
        {
            DaysGrid.RowDefinitions.Add(new RowDefinition { Height = new GridLength(CalendarMetrics.MiniRowHeight) });
        }
        for (var i = 0; i < 42; i++)
        {
            var d = new Day(i);
            d.Cell.Tapped += (_, e) =>
            {
                e.Handled = true;
                if (month is { } m && d.Index < m.Days.Count)
                {
                    DayPicked?.Invoke(this, m.Days[d.Index].Date);
                }
            };
            Grid.SetRow(d.Cell, i / 7);
            Grid.SetColumn(d.Cell, i % 7);
            days[i] = d;
            DaysGrid.Children.Add(d.Cell);
        }
    }

    /// <summary>A click on a day: its midnight.</summary>
    public event EventHandler<DateTimeOffset>? DayPicked;

    /// <summary>An arrow: -1 the month before, +1 the month after.</summary>
    public event EventHandler<int>? Stepped;

    /// <summary>The month shown, as last set.</summary>
    public Cal.MiniMonth? Month => month;

    /// <summary>Shows <paramref name="m"/>; the same month again changes nothing.</summary>
    public void Set(Cal.MiniMonth m)
    {
        ArgumentNullException.ThrowIfNull(m);
        if (m.Equals(month))
        {
            return;
        }
        month = m;
        TitleText.Text = m.Title;
        for (var i = 0; i < 7 && i < m.Headers.Count; i++)
        {
            headers[i].Text = m.Headers[i];
        }
        for (var i = 0; i < 42 && i < m.Days.Count; i++)
        {
            days[i].Show(m.Days[i]);
        }
    }

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new CalendarRegionPeer(this, nameof(MiniMonthView));

    private void OnPreviousClick(object sender, RoutedEventArgs e) => Stepped?.Invoke(this, -1);

    private void OnNextClick(object sender, RoutedEventArgs e) => Stepped?.Invoke(this, 1);

    // One day of the grid: the band, today's circle and the number.
    private sealed class Day
    {
        private readonly Border band = new() { Style = CalendarStyles.Get("CalendarBandStyle"), Visibility = Visibility.Collapsed };
        private readonly Ellipse today = new()
        {
            Width = 21,
            Height = 21,
            Style = CalendarStyles.Get("CalendarTodayCircleStyle"),
            Visibility = Visibility.Collapsed,
        };

        private readonly TextBlock number = new() { FontSize = 11 };

        public Day(int index)
        {
            Index = index;
            Cell = new Grid { Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent) };
            Cell.Children.Add(band);
            Cell.Children.Add(today);
            Cell.Children.Add(number);
        }

        public int Index { get; }

        public Grid Cell { get; }

        public void Show(Cal.MiniMonth.Day d)
        {
            number.Text = d.Number;
            number.Style = CalendarStyles.Get(
                d.Today ? "CalendarTodayNumberStyle"
                : !d.InMonth ? "CalendarOtherDayNumberStyle"
                : d.Weekend ? "CalendarWeekendNumberStyle"
                : "CalendarDayNumberStyle");
            today.Visibility = d.Today ? Visibility.Visible : Visibility.Collapsed;
            band.Visibility = d.Shown ? Visibility.Visible : Visibility.Collapsed;
            // Rounded at its row's first and last shown day, square between.
            var r = (CalendarMetrics.MiniRowHeight - 4) / 2;
            band.CornerRadius = new CornerRadius(d.BandStart ? r : 0, d.BandEnd ? r : 0, d.BandEnd ? r : 0, d.BandStart ? r : 0);
            band.Margin = new Thickness(d.BandStart ? 2 : 0, 2, d.BandEnd ? 2 : 0, 2);
        }
    }
}
