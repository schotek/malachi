// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/MonthCellView.swift
// (MonthCellView); GTK: the calendar's views follow (docs/calendar.md
// §6.4).
//
// One day of the Month view (docs/calendar.md §5.4): the day's number at
// the top right (today's in a filled accent circle, other months grey),
// weekends and the days of other months shaded, the pills Core chose for
// the cell's capacity (Cal.CellItems; they start under the row's lanes of
// spanning bars) and "+N more", a button whose flyout lists the day's
// pills. A click on a pill opens its detail; in the flyout the flyout
// closes first and the detail is anchored on the cell. A double click on
// the cell's empty part asks for New Event that day (Cal.Origin.MonthCell,
// at Cal.MonthCellHour). Known gap, as on macOS: the flyout lists pills
// only, not the day's bars.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Shapes;
using Windows.UI;

namespace Malachi.App.Calendars;

/// <summary>A day of the Month view (macOS <c>MonthCellView</c>).</summary>
internal sealed partial class MonthCellView : Grid
{
    private readonly Cal.Env env;
    private readonly StackPanel pillPanel = new() { Spacing = 1, Margin = new Thickness(3, CalendarMetrics.CellNumberLine, 3, 0) };
    private readonly List<MonthPillView> pills = [];
    private readonly HashSet<string> allKeys = new(StringComparer.Ordinal);
    private Flyout? moreFlyout;

    /// <summary>The cell of <paramref name="cell"/>, its texts in <paramref name="env"/>.</summary>
    public MonthCellView(Cal.Cell cell, Cal.Env env)
    {
        Cell = cell;
        this.env = env;
        Background = new SolidColorBrush(Microsoft.UI.Colors.Transparent);
        if (!cell.InMonth)
        {
            Children.Add(new Rectangle { Style = CalendarStyles.Get("CalendarOtherMonthStyle") });
        }
        else if (cell.Weekend)
        {
            Children.Add(new Rectangle { Style = CalendarStyles.Get("CalendarWeekendStyle") });
        }
        Children.Add(new Rectangle { Height = 1, VerticalAlignment = VerticalAlignment.Bottom, Style = CalendarStyles.Get("CalendarLineStyle") });
        Children.Add(new Rectangle { Width = 1, HorizontalAlignment = HorizontalAlignment.Right, Style = CalendarStyles.Get("CalendarLineStyle") });
        var number = new Grid
        {
            Width = 20,
            Height = 20,
            Margin = new Thickness(0, 1, 4, 0),
            HorizontalAlignment = HorizontalAlignment.Right,
            VerticalAlignment = VerticalAlignment.Top,
            IsHitTestVisible = false,
        };
        if (cell.Today)
        {
            number.Children.Add(new Ellipse { Style = CalendarStyles.Get("CalendarTodayCircleStyle") });
        }
        number.Children.Add(new TextBlock
        {
            Style = CalendarStyles.Get(cell.Today ? "CalendarTodayNumberStyle" : cell.InMonth ? "CalendarDayNumberStyle" : "CalendarOtherDayNumberStyle"),
            Text = Cal.Text.DayText(cell.Day, env),
        });
        Children.Add(number);
        Children.Add(pillPanel);
        DoubleTapped += OnDoubleTapped;
    }

    /// <summary>A pill's click (in the cell or its flyout): the occurrence and the element to anchor its detail on.</summary>
    public event EventHandler<(OccurrenceView Pill, FrameworkElement Anchor)>? PillActivated;

    /// <summary>A pill's keyboard selection.</summary>
    public event EventHandler<string>? PillSelectionRequested;

    /// <summary>A double click on the cell's empty part: New Event that day.</summary>
    public event EventHandler<DateTimeOffset>? NewEventRequested;

    /// <summary>The day.</summary>
    public Cal.Cell Cell { get; }

    /// <summary>The pills shown in the cell.</summary>
    public IReadOnlyList<MonthPillView> Pills => pills;

    /// <summary>Where the pills start: under the day number and the row's lanes of spanning bars.</summary>
    public double PillTop
    {
        get => pillPanel.Margin.Top;
        set => pillPanel.Margin = new Thickness(3, value, 3, 0);
    }

    /// <summary>Whether <paramref name="key"/> is one of the day's pills, shown or behind "+N more".</summary>
    public bool HasKey(string key) => allKeys.Contains(key);

    /// <summary>
    /// Fills the cell: <paramref name="shown"/> pills and <paramref name="more"/>
    /// behind the "+N more" button (both from Cal.CellItems), <paramref name="all"/>
    /// for its flyout.
    /// </summary>
    public void Set(
        IReadOnlyList<Cal.Pill> shown, int more, IReadOnlyList<Cal.Pill> all,
        Func<Occurrence, Color> colorOf, Func<Occurrence, string> labelOf, string? selection)
    {
        pillPanel.Children.Clear();
        pills.Clear();
        allKeys.Clear();
        foreach (var p in all)
        {
            allKeys.Add(Cal.OccurrenceKey(p.Occurrence));
        }
        foreach (var p in shown)
        {
            var v = new MonthPillView(p, colorOf(p.Occurrence), labelOf(p.Occurrence), env) { Selected = Cal.OccurrenceKey(p.Occurrence) == selection };
            v.Activated += (_, _) => PillActivated?.Invoke(this, (v, v));
            v.SelectionRequested += (_, _) => PillSelectionRequested?.Invoke(this, v.Key);
            pills.Add(v);
            pillPanel.Children.Add(v);
        }
        moreFlyout = null;
        if (more > 0)
        {
            var b = new Button
            {
                Padding = new Thickness(2, 0, 2, 0),
                MinHeight = 0,
                Height = CalendarMetrics.PillLine - 1,
                HorizontalAlignment = HorizontalAlignment.Left,
                Style = (Style)Application.Current.Resources["SubtleButtonStyle"],
                Content = new TextBlock { Style = CalendarStyles.Get("CalendarSmallTextStyle"), Text = Cal.More(more) },
            };
            AutomationProperties.SetName(b, Cal.More(more));
            AutomationProperties.SetAutomationId(b, "CalendarMore:" + Cal.DateOf(Cell.Day, env.Zone).Value);
            b.DoubleTapped += (_, e) => e.Handled = true;
            var flyout = new Flyout { Placement = FlyoutPlacementMode.Bottom };
            b.Click += (_, _) =>
            {
                flyout.Content = MoreList(all, colorOf, labelOf, flyout);
                flyout.ShowAt(b);
            };
            moreFlyout = flyout;
            pillPanel.Children.Add(b);
        }
    }

    /// <summary>Marks the selected pill.</summary>
    public void SetSelection(string? key)
    {
        foreach (var p in pills)
        {
            p.Selected = p.Key == key;
        }
    }

    /// <summary>Closes the "+N more" flyout, if it is open.</summary>
    public void CloseFlyout() => moreFlyout?.Hide();

    // The flyout's list: the day's heading and every pill.
    private StackPanel MoreList(IReadOnlyList<Cal.Pill> all, Func<Occurrence, Color> colorOf, Func<Occurrence, string> labelOf, Flyout flyout)
    {
        var list = new StackPanel { Spacing = 1, Width = 240 };
        list.Children.Add(new TextBlock
        {
            Style = (Style)Application.Current.Resources["BodyStrongTextBlockStyle"],
            Margin = new Thickness(0, 0, 0, 6),
            Text = Cal.Text.WeekdayDateText(Cell.Day, env),
        });
        foreach (var p in all)
        {
            var v = new MonthPillView(p, colorOf(p.Occurrence), labelOf(p.Occurrence), env);
            v.Activated += (_, _) =>
            {
                flyout.Hide();
                PillActivated?.Invoke(this, (v, this));
            };
            list.Children.Add(v);
        }
        return list;
    }

    private void OnDoubleTapped(object sender, DoubleTappedRoutedEventArgs e)
    {
        e.Handled = true;
        NewEventRequested?.Invoke(this, Cell.Day);
    }

    /// <summary>The first focusable pill, the selected one first.</summary>
    public MonthPillView? FocusTarget(string? key) => pills.FirstOrDefault(p => p.Key == key) ?? pills.FirstOrDefault();
}
