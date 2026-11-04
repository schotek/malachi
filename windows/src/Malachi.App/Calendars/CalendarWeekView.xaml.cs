// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/CalendarTimeGridView.swift
// (CalendarWeekView: update(controller:), layout's first scroll,
// scrollToWorkingHours, clipScrolled, view(for:); CalendarTimeGridView:
// set, makeHourLabels, layout, draw, mouseDown's double click; the now
// line's timer), CalendarDayHeaderView.swift and CalendarAllDayView.swift
// (the all-day row: its height by lanes, the bars, the caption, the double
// click on a day); GTK: the calendar's views follow (docs/calendar.md
// §6.4).
//
// The Week and Day views. The views are rebuilt only when what they were
// built from changes (the days, the occurrences, showDeclined, the
// calendars' colours, the zone); a change of the selection only repaints
// the blocks. The owner's findings of the live test (docs/calendar.md
// §5.3): the fills are opaque and lie above the hour lines and the day
// separators; the now line lies above the blocks and moves each minute;
// the first scroll (Cal.ScrollMinute × 60 − 10 DIP, the working hours'
// start with its label whole) is put back whenever the viewport's height
// changes until the user scrolls, and a range of other days starts the
// rule again. A click on a block or a bar selects it and opens its detail
// (CalendarController.OpenDetail, anchored on it through
// CalendarPage.AnchorFor); a double click on empty time asks for New Event
// at that slot (Cal.Origin.Slot, rounded down to the half hour by Core),
// on the all-day row for an all-day event that day, once the second press
// is released (AfterRelease: a window opened on the press came up behind
// the main window). No dragging (Q3).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Controls;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Shapes;

namespace Malachi.App.Calendars;

/// <summary>The Week and Day views of the calendar (macOS <c>CalendarWeekView</c>).</summary>
public sealed partial class CalendarWeekView : UserControl, ICalendarView
{
    private static readonly double GridHeight = Cal.DayMinutes * CalendarMetrics.HourHeight / 60;

    private readonly List<EventBlockView> blocks = [];
    private readonly List<AllDayBarView> bars = [];
    private readonly List<TextBlock> hourLabels = [];
    private readonly List<Rectangle> hourLines = [];
    private readonly List<Rectangle> halfLines = [];
    private readonly List<Rectangle> dayLines = [];
    private readonly List<Rectangle> allDayLines = [];
    private readonly Rectangle todayTint = new() { Style = CalendarStyles.Get("CalendarTodayTintStyle") };
    private readonly Rectangle nowLine = new() { Height = 2, Style = CalendarStyles.Get("CalendarNowLineStyle") };
    private readonly Ellipse nowDot = new() { Width = 8, Height = 8, Style = CalendarStyles.Get("CalendarNowDotStyle") };
    private readonly DispatcherQueueTimer timer;

    // A double click's New Event waits for the second press's release.
    private readonly AfterRelease afterRelease;

    private CalendarController? controller;
    private IReadOnlyList<DateTimeOffset> days = [];
    private Cal.Env env = Cal.Env.English(TimeZoneInfo.Local);
    private IReadOnlyList<IReadOnlyList<Cal.Block>> dayBlocks = [];
    private int lanes;
    private Input? input;

    // The first scroll: still to do (the first show, other days), and the
    // offset it put the grid at with the viewport's height it was computed
    // for, kept until the user scrolls; our own change of the view, which
    // ViewChanged reports too.
    private bool scrollPending = true;
    private (double Y, double ViewportHeight)? autoScroll;
    private bool applyingScroll;
    private int scrollMinute = 7 * 60;

    /// <summary>An empty view; <see cref="Update"/> fills it.</summary>
    public CalendarWeekView()
    {
        InitializeComponent();
        AllDayCaption.Text = Cal.Text.AllDay;
        BackLayer.Children.Add(todayTint);
        NowLayer.Children.Add(nowLine);
        NowLayer.Children.Add(nowDot);
        timer = DispatcherQueue.CreateTimer();
        timer.Interval = TimeSpan.FromMinutes(1);
        timer.Tick += (_, _) => PlaceToday();
        Loaded += (_, _) =>
        {
            PlaceToday();
            timer.Start();
        };
        Unloaded += (_, _) => timer.Stop();
        afterRelease = new AfterRelease(this);
    }

    /// <inheritdoc/>
    public void Update(CalendarController controller)
    {
        ArgumentNullException.ThrowIfNull(controller);
        this.controller = controller;
        var snapshot = controller.Snapshot;
        env = controller.Env;
        var newDays = controller.Range.Days();
        var colors = string.Join("\n", snapshot.CalendarAccounts.SelectMany(a => a.Calendars.Select(c => c.Id.Value + c.Color)));
        var next = new Input(newDays, controller.Occurrences, controller.ShowDeclined, colors, env.Zone.Id);
        scrollMinute = Cal.ScrollMinute(snapshot.Preferences.WorkingHours);
        // The grid is a keyboard stop (the arrows select from it): named by its view.
        Microsoft.UI.Xaml.Automation.AutomationProperties.SetName(Scroller, Cal.Text.ViewTitle(controller.View));
        if (input is null || !input.Same(next))
        {
            if (input is null || !input.Days.SequenceEqual(newDays))
            {
                scrollPending = true;
                autoScroll = null;
            }
            input = next;
            days = newDays;
            Rebuild(controller, snapshot);
        }
        ApplySelection(controller.Selection);
        ApplyScroll();
    }

    /// <inheritdoc/>
    public FrameworkElement? ElementFor(string key)
    {
        if (blocks.FirstOrDefault(b => b.Key == key) is { } block)
        {
            return block;
        }
        return bars.FirstOrDefault(b => b.Key == key && b.Visibility == Visibility.Visible);
    }

    /// <inheritdoc/>
    public bool FocusSelection()
    {
        var key = controller?.Selection;
        OccurrenceView? target = key is null ? null : (OccurrenceView?)blocks.FirstOrDefault(b => b.Key == key) ?? bars.FirstOrDefault(b => b.Key == key);
        if (target is not null)
        {
            target.StartBringIntoView(new BringIntoViewOptions { AnimationDesired = false });
            return target.Focus(FocusState.Keyboard);
        }
        // Nothing selected: the grid itself (the arrows then select), never
        // a block (its keyboard focus would select it), never a scroll.
        return Scroller.Focus(FocusState.Programmatic);
    }

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new CalendarRegionPeer(this, nameof(CalendarWeekView));

    // The views anew from the controller's state.
    private void Rebuild(CalendarController c, Cal.Snapshot snapshot)
    {
        var zone = env.Zone;
        var now = TimeProvider.System.GetUtcNow();
        var today = Cal.StartOfDay(now, zone);
        var wide = days.Count == 1;
        Windows.UI.Color ColorOf(Occurrence o) => CalendarPalette.Of(o, snapshot);
        string Label(Occurrence o) => Cal.BlockLabel(o, env);

        BuildHeader(today);

        foreach (var b in bars)
        {
            AllDayBars.Children.Remove(b);
        }
        bars.Clear();
        var (allDay, laneCount) = Cal.AllDayLanes(c.Occurrences, days, zone, c.ShowDeclined);
        lanes = laneCount;
        foreach (var bar in allDay)
        {
            var v = new AllDayBarView(bar, ColorOf(bar.Occurrence), Label(bar.Occurrence));
            Wire(v);
            bars.Add(v);
            AllDayBars.Children.Add(v);
        }
        var shown = Math.Min(lanes, CalendarMetrics.AllDayMaxLanes);
        AllDayRow.Height = Math.Max(CalendarMetrics.AllDayMinimum, 8 + (shown * (CalendarMetrics.LaneHeight + CalendarMetrics.LaneGap)));

        foreach (var b in blocks)
        {
            BlockLayer.Children.Remove(b);
        }
        blocks.Clear();
        dayBlocks = [.. days.Select(d => Cal.LayoutDay(c.Occurrences, d, zone, c.ShowDeclined))];
        foreach (var list in dayBlocks)
        {
            foreach (var block in list)
            {
                var v = new EventBlockView(block, ColorOf(block.Occurrence), Label(block.Occurrence), env, wide);
                Wire(v);
                blocks.Add(v);
                BlockLayer.Children.Add(v);
            }
        }
        BuildBack();
        LayoutGrid();
        LayoutAllDay();
    }

    // A block's or a bar's click and keyboard selection go to the controller.
    private void Wire(OccurrenceView v)
    {
        v.Activated += (_, _) => controller?.OpenDetail(v.Occurrence);
        v.SelectionRequested += (_, _) => controller?.Select(v.Key);
    }

    private void ApplySelection(string? key)
    {
        foreach (var b in blocks)
        {
            b.Selected = b.Key == key;
        }
        foreach (var b in bars)
        {
            b.Selected = b.Key == key;
        }
    }

    // The day header: per column the weekday's abbreviation and the day's
    // number, today's in a filled accent circle, weekends grey.
    private void BuildHeader(DateTimeOffset today)
    {
        HeaderDays.Children.Clear();
        HeaderDays.ColumnDefinitions.Clear();
        HeaderDays.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(CalendarMetrics.AxisWidth) });
        for (var i = 0; i < days.Count; i++)
        {
            HeaderDays.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            var d = days[i];
            var p = Cal.PartsOf(d, env.Zone);
            var weekend = p.Weekday is Cal.Weekday.Saturday or Cal.Weekday.Sunday;
            var isToday = d == today;
            var cell = new Grid();
            var weekday = new TextBlock
            {
                Style = CalendarStyles.Get(weekend ? "CalendarDimTextStyle" : "CalendarSmallTextStyle"),
                HorizontalAlignment = HorizontalAlignment.Center,
                VerticalAlignment = VerticalAlignment.Top,
                Margin = new Thickness(0, 2, 0, 0),
                Text = Cal.Text.ShortWeekdayText(d, env),
            };
            var number = new Grid
            {
                Width = 24,
                Height = 24,
                Margin = new Thickness(0, 15, 0, 0),
                HorizontalAlignment = HorizontalAlignment.Center,
                VerticalAlignment = VerticalAlignment.Top,
            };
            if (isToday)
            {
                number.Children.Add(new Ellipse { Style = CalendarStyles.Get("CalendarTodayCircleStyle") });
            }
            number.Children.Add(new TextBlock
            {
                Style = CalendarStyles.Get(isToday ? "CalendarTodayNumberStyle" : weekend ? "CalendarOtherDayNumberStyle" : "CalendarDayNumberStyle"),
                FontSize = 14,
                Text = Cal.Text.DayText(d, env),
            });
            cell.Children.Add(weekday);
            cell.Children.Add(number);
            Grid.SetColumn(cell, i + 1);
            HeaderDays.Children.Add(cell);
        }
    }

    // The hour labels "01:00" … "23:00" in the user's time format and the
    // lines (made per rebuild: the days' count and the format may change).
    private void BuildBack()
    {
        foreach (var e in hourLabels.Cast<UIElement>().Concat(hourLines).Concat(halfLines).Concat(dayLines))
        {
            BackLayer.Children.Remove(e);
        }
        foreach (var e in allDayLines)
        {
            AllDayLines.Children.Remove(e);
        }
        hourLabels.Clear();
        hourLines.Clear();
        halfLines.Clear();
        dayLines.Clear();
        allDayLines.Clear();
        if (days.Count == 0)
        {
            return;
        }
        var p = Cal.PartsOf(days[0], env.Zone);
        for (var h = 1; h < 24; h++)
        {
            var label = new TextBlock
            {
                Style = CalendarStyles.Get("CalendarSmallTextStyle"),
                FontSize = 10,
                Width = CalendarMetrics.AxisWidth - 8,
                TextAlignment = TextAlignment.Right,
                Text = Cal.Text.TimeText(Cal.Date(p.Year, p.Month, p.Day, h, 0, env.Zone), env),
            };
            hourLabels.Add(label);
            BackLayer.Children.Add(label);
            var line = new Rectangle { Height = 1, Style = CalendarStyles.Get("CalendarLineStyle") };
            hourLines.Add(line);
            BackLayer.Children.Add(line);
        }
        for (var h = 0; h < 24; h++)
        {
            var line = new Rectangle { Height = 1, Style = CalendarStyles.Get("CalendarHalfLineStyle") };
            halfLines.Add(line);
            BackLayer.Children.Add(line);
        }
        for (var i = 0; i < days.Count; i++)
        {
            var line = new Rectangle { Width = 1, Height = GridHeight, Style = CalendarStyles.Get("CalendarLineStyle") };
            dayLines.Add(line);
            BackLayer.Children.Add(line);
        }
        for (var i = 0; i <= days.Count; i++)
        {
            var line = new Rectangle { Width = 1, Style = CalendarStyles.Get("CalendarLineStyle") };
            allDayLines.Add(line);
            AllDayLines.Children.Add(line);
        }
    }

    private double ColumnWidth(double width) => Math.Max(0, (width - CalendarMetrics.AxisWidth) / Math.Max(1, days.Count));

    private static double Y(int minute) => minute * CalendarMetrics.HourHeight / 60;

    // The grid's lines, labels, blocks and now line for its width.
    private void LayoutGrid()
    {
        var width = TimeGrid.ActualWidth;
        if (width <= 0)
        {
            return;
        }
        var cw = ColumnWidth(width);
        for (var i = 0; i < hourLabels.Count; i++)
        {
            Canvas.SetLeft(hourLabels[i], 0);
            Canvas.SetTop(hourLabels[i], Y((i + 1) * 60) - 8);
        }
        for (var i = 0; i < hourLines.Count; i++)
        {
            hourLines[i].Width = Math.Max(0, width - CalendarMetrics.AxisWidth + 4);
            Canvas.SetLeft(hourLines[i], CalendarMetrics.AxisWidth - 4);
            Canvas.SetTop(hourLines[i], Y((i + 1) * 60));
        }
        for (var i = 0; i < halfLines.Count; i++)
        {
            halfLines[i].Width = Math.Max(0, width - CalendarMetrics.AxisWidth);
            Canvas.SetLeft(halfLines[i], CalendarMetrics.AxisWidth);
            Canvas.SetTop(halfLines[i], Y((i * 60) + 30));
        }
        for (var i = 0; i < dayLines.Count; i++)
        {
            Canvas.SetLeft(dayLines[i], CalendarMetrics.AxisWidth + (i * cw));
        }
        var index = 0;
        for (var d = 0; d < dayBlocks.Count; d++)
        {
            foreach (var b in dayBlocks[d])
            {
                if (index >= blocks.Count)
                {
                    break;
                }
                var v = blocks[index++];
                var inner = cw - 4;
                var w = inner / Math.Max(1, b.Columns);
                Canvas.SetLeft(v, CalendarMetrics.AxisWidth + (d * cw) + 1 + (b.Column * w));
                Canvas.SetTop(v, Y(b.Top) + 1);
                v.Width = Math.Max(4, w - 2);
                v.Height = Math.Max(4, Y(b.Bottom) - Y(b.Top) - 2);
            }
        }
        PlaceToday();
    }

    // The all-day row's bars and column lines for its width.
    private void LayoutAllDay()
    {
        var width = AllDayRow.ActualWidth;
        if (width <= 0)
        {
            return;
        }
        var w = ColumnWidth(width);
        var height = AllDayRow.Height;
        for (var i = 0; i < allDayLines.Count; i++)
        {
            allDayLines[i].Height = height;
            Canvas.SetLeft(allDayLines[i], CalendarMetrics.AxisWidth + (i * w));
        }
        foreach (var b in bars)
        {
            var lane = b.Bar.Lane;
            Canvas.SetLeft(b, CalendarMetrics.AxisWidth + (b.Bar.First * w) + 2);
            Canvas.SetTop(b, 4 + (lane * (CalendarMetrics.LaneHeight + CalendarMetrics.LaneGap)));
            b.Width = Math.Max(0, ((b.Bar.Last - b.Bar.First + 1) * w) - 4);
            // Bars in further lanes are not drawn (the Agenda and the Month list them).
            b.Visibility = lane >= CalendarMetrics.AllDayMaxLanes ? Visibility.Collapsed : Visibility.Visible;
        }
    }

    // Today's column tint and the red now line with its dot (each minute).
    private void PlaceToday()
    {
        var width = TimeGrid.ActualWidth;
        var cw = ColumnWidth(width);
        var zone = env.Zone;
        var now = TimeProvider.System.GetUtcNow();
        var todayIndex = -1;
        var today = Cal.StartOfDay(now, zone);
        for (var i = 0; i < days.Count; i++)
        {
            if (days[i] == today)
            {
                todayIndex = i;
            }
        }
        todayTint.Visibility = todayIndex >= 0 && width > 0 ? Visibility.Visible : Visibility.Collapsed;
        if (todayIndex >= 0)
        {
            todayTint.Width = cw;
            todayTint.Height = GridHeight;
            Canvas.SetLeft(todayTint, CalendarMetrics.AxisWidth + (todayIndex * cw));
        }
        if (days.Count > 0 && width > 0
            && Cal.NowLine(now, new Cal.Range(days[0], Cal.AddDays(days[^1], 1, zone), zone), zone) is var (day, minute))
        {
            nowLine.Visibility = Visibility.Visible;
            nowDot.Visibility = Visibility.Visible;
            var x = CalendarMetrics.AxisWidth + (day * cw);
            nowLine.Width = cw;
            Canvas.SetLeft(nowLine, x);
            Canvas.SetTop(nowLine, Y(minute) - 1);
            Canvas.SetLeft(nowDot, x - 4);
            Canvas.SetTop(nowDot, Y(minute) - 4);
        }
        else
        {
            nowLine.Visibility = Visibility.Collapsed;
            nowDot.Visibility = Visibility.Collapsed;
        }
    }

    // The first scroll, or putting it back after the viewport's height
    // changed while the user has not scrolled (macOS layout()).
    private void ApplyScroll()
    {
        var viewport = Scroller.ViewportHeight;
        if (viewport <= 0 || Visibility != Visibility.Visible)
        {
            return;
        }
        if (scrollPending || (autoScroll is { } a && Math.Abs(viewport - a.ViewportHeight) > 0.5))
        {
            scrollPending = false;
            // The hour labels are centred on their lines: the first line
            // sits that half and a few DIPs below the top, its label whole.
            var y = Math.Max(0, (scrollMinute * CalendarMetrics.HourHeight / 60) - 10);
            var max = Math.Max(0, GridHeight - viewport);
            var target = Math.Min(y, max);
            // No ViewChanged comes for an offset the grid already has.
            applyingScroll = Math.Abs(Scroller.VerticalOffset - target) > 0.5;
            Scroller.ChangeView(null, target, null, disableAnimation: true);
            autoScroll = (target, viewport);
        }
    }

    private void OnScrollerSizeChanged(object sender, SizeChangedEventArgs e) =>
        DispatcherQueue.TryEnqueue(ApplyScroll);

    // The user scrolled (not a resize, not ours): the offset is theirs.
    private void OnScrollerViewChanged(object? sender, ScrollViewerViewChangedEventArgs e)
    {
        if (e.IsIntermediate && applyingScroll)
        {
            return;
        }
        if (applyingScroll)
        {
            applyingScroll = false;
            if (autoScroll is { } own)
            {
                autoScroll = (Scroller.VerticalOffset, own.ViewportHeight);
            }
            return;
        }
        if (autoScroll is { } a && Math.Abs(Scroller.ViewportHeight - a.ViewportHeight) <= 0.5
            && Math.Abs(Scroller.VerticalOffset - a.Y) > 0.5)
        {
            autoScroll = null;
        }
    }

    private void OnGridSizeChanged(object sender, SizeChangedEventArgs e) => LayoutGrid();

    private void OnAllDaySizeChanged(object sender, SizeChangedEventArgs e) => LayoutAllDay();

    // A double click on empty time: New Event at that half hour, once the
    // second press is released (AfterRelease: a window opened on the press
    // comes up behind this one).
    private void OnGridDoubleTapped(object sender, DoubleTappedRoutedEventArgs e)
    {
        if (controller is not { } c || days.Count == 0)
        {
            return;
        }
        var p = e.GetPosition(TimeGrid);
        var cw = ColumnWidth(TimeGrid.ActualWidth);
        if (p.X < CalendarMetrics.AxisWidth || cw <= 0)
        {
            return;
        }
        e.Handled = true;
        var i = Math.Min(days.Count - 1, (int)((p.X - CalendarMetrics.AxisWidth) / cw));
        var minute = Math.Clamp((int)(p.Y / CalendarMetrics.HourHeight * 60), 0, Cal.DayMinutes - 1);
        var d = Cal.PartsOf(days[i], env.Zone);
        var at = Cal.Date(d.Year, d.Month, d.Day, minute / 60, minute % 60, env.Zone);
        afterRelease.Run(() => c.NewEvent(at, Cal.Origin.Slot));
    }

    // A double click on the all-day row's empty part: an all-day event that
    // day, once the press is released.
    private void OnAllDayDoubleTapped(object sender, DoubleTappedRoutedEventArgs e)
    {
        if (controller is not { } c || days.Count == 0)
        {
            return;
        }
        var p = e.GetPosition(AllDayRow);
        var w = ColumnWidth(AllDayRow.ActualWidth);
        if (p.X < CalendarMetrics.AxisWidth || w <= 0)
        {
            return;
        }
        var i = (int)((p.X - CalendarMetrics.AxisWidth) / w);
        if (i >= 0 && i < days.Count)
        {
            e.Handled = true;
            var day = days[i];
            afterRelease.Run(() => c.NewEvent(day, Cal.Origin.AllDay));
        }
    }

    // What the views were last built from (macOS Input).
    private sealed record Input(
        IReadOnlyList<DateTimeOffset> Days, IReadOnlyList<Occurrence> Occurrences, bool ShowDeclined, string Colors, string Zone)
    {
        public bool Same(Input other) =>
            Days.SequenceEqual(other.Days) && Occurrences.SequenceEqual(other.Occurrences) && ShowDeclined == other.ShowDeclined
            && Colors == other.Colors && Zone == other.Zone;
    }
}
