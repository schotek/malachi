// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/CalendarMonthView.swift
// (update(controller:), rebuild, layout, fill, view(for:)); GTK: the
// calendar's views follow (docs/calendar.md §6.4).
//
// The Month view. Rebuilt only when what it was built from changes (the
// grid, the weekday headers, the occurrences, showDeclined, the calendars'
// colours); a change of the selection only repaints. The bars of each week
// row (Cal.MonthLanes: all-day events and timed ones that cross midnight,
// the owner's decision of 2026-10-16) lie under the row's day numbers,
// one bar per span, a lane the row has no room for not drawn (macOS's
// known gap); the pills start under the row's lanes. How many pills a cell
// holds is Core's (Cal.CellCapacity with the cell's actual height and its
// row's lanes), asked again whenever the cells change size, never measured
// from text; "+N more" counts the pills only. A click on a bar or a pill
// selects it and opens its detail; a double click on a cell's empty part
// asks for New Event that day, once the second press is released
// (AfterRelease).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using Malachi.App.Controls;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Windows.UI;

namespace Malachi.App.Calendars;

/// <summary>The Month view of the calendar (macOS <c>CalendarMonthView</c>).</summary>
public sealed partial class CalendarMonthView : UserControl, ICalendarView
{
    private readonly List<MonthCellView> cells = [];
    private readonly List<MonthSpanBarView> spans = [];
    private readonly List<int> rowLanes = [];
    private readonly List<UIElement> headerLabels = [];

    // A cell's double click: New Event once the second press is released.
    private readonly AfterRelease afterRelease;
    private List<int> capacities = [];
    private CalendarController? controller;
    private Input? input;
    private Cal.Env env = Cal.Env.English(TimeZoneInfo.Local);
    private IReadOnlyList<Occurrence> occurrences = [];
    private bool showDeclined;
    private Cal.Snapshot? snapshot;
    private string? selection;

    /// <summary>An empty view; <see cref="Update"/> fills it.</summary>
    public CalendarMonthView()
    {
        InitializeComponent();
        afterRelease = new AfterRelease(this);
    }

    /// <inheritdoc/>
    public void Update(CalendarController controller)
    {
        ArgumentNullException.ThrowIfNull(controller);
        this.controller = controller;
        env = controller.Env;
        var s = controller.Snapshot;
        var g = Cal.MonthCells(controller.Anchor, controller.WeekStart, TimeProvider.System.GetUtcNow(), env.Zone);
        var heads = Cal.WeekdayHeaders(controller.WeekStart, env);
        var colors = string.Join("\n", s.CalendarAccounts.SelectMany(a => a.Calendars.Select(c => c.Id.Value + c.Color)));
        var next = new Input(g, heads, controller.Occurrences, controller.ShowDeclined, colors, controller.WeekStart);
        selection = controller.Selection;
        if (input is not null && input.Same(next))
        {
            foreach (var c in cells)
            {
                c.SetSelection(selection);
            }
            foreach (var v in spans)
            {
                v.Selected = v.Key == selection;
            }
            return;
        }
        input = next;
        snapshot = s;
        occurrences = controller.Occurrences;
        showDeclined = controller.ShowDeclined;
        Rebuild(g, heads, controller.WeekStart);
    }

    /// <inheritdoc/>
    public FrameworkElement? ElementFor(string key)
    {
        if (spans.FirstOrDefault(s => s.Key == key && s.Visibility == Visibility.Visible) is { } span)
        {
            return span;
        }
        foreach (var c in cells)
        {
            if (c.Pills.FirstOrDefault(p => p.Key == key) is { } pill)
            {
                return pill;
            }
        }
        // A pill behind "+N more": its day's cell (macOS anchors the flyout's choice there).
        return cells.FirstOrDefault(c => c.HasKey(key));
    }

    /// <inheritdoc/>
    public bool FocusSelection()
    {
        var key = selection;
        OccurrenceView? target = spans.FirstOrDefault(s => s.Key == key && s.Visibility == Visibility.Visible);
        target ??= cells.Select(c => c.Pills.FirstOrDefault(p => p.Key == key)).FirstOrDefault(p => p is not null);
        if (target is not null)
        {
            return target.Focus(FocusState.Keyboard);
        }
        // Nothing selected: the first occurrence, without the keyboard's
        // selection (a programmatic focus selects nothing).
        target = (OccurrenceView?)spans.FirstOrDefault(s => s.Visibility == Visibility.Visible)
            ?? cells.Select(c => c.FocusTarget(null)).FirstOrDefault(p => p is not null);
        return target?.Focus(FocusState.Programmatic) == true;
    }

    /// <summary>Closes an open "+N more" flyout.</summary>
    public void CloseFlyouts()
    {
        foreach (var c in cells)
        {
            c.CloseFlyout();
        }
    }

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new CalendarRegionPeer(this, nameof(CalendarMonthView));

    private Color ColorOf(Occurrence o) => snapshot is { } s ? CalendarPalette.Of(o, s) : CalendarPalette.Accent;

    private string LabelOf(Occurrence o) => Cal.BlockLabel(o, env);

    private void Rebuild(Cal.MonthGrid g, IReadOnlyList<string> heads, Cal.Weekday weekStart)
    {
        // The weekday header: Saturday and Sunday greyer.
        foreach (var h in headerLabels)
        {
            Headers.Children.Remove(h);
        }
        headerLabels.Clear();
        Headers.ColumnDefinitions.Clear();
        Headers.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(CalendarMetrics.WeekNumberWidth) });
        for (var i = 0; i < 7 && i < heads.Count; i++)
        {
            Headers.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            var wd = (Cal.Weekday)(((int)weekStart + i) % 7);
            var t = new TextBlock
            {
                Style = CalendarStyles.Get(wd is Cal.Weekday.Saturday or Cal.Weekday.Sunday ? "CalendarDimTextStyle" : "CalendarSmallTextStyle"),
                HorizontalAlignment = HorizontalAlignment.Center,
                VerticalAlignment = VerticalAlignment.Center,
                Text = heads[i],
            };
            Grid.SetColumn(t, i + 1);
            headerLabels.Add(t);
            Headers.Children.Add(t);
        }

        Cells.Children.Clear();
        Cells.ColumnDefinitions.Clear();
        Cells.RowDefinitions.Clear();
        Cells.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(CalendarMetrics.WeekNumberWidth) });
        for (var i = 0; i < 7; i++)
        {
            Cells.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        }
        for (var r = 0; r < 6; r++)
        {
            Cells.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        }
        for (var r = 0; r < g.WeekNumbers.Count && r < 6; r++)
        {
            var n = g.WeekNumbers[r];
            var t = new TextBlock
            {
                Style = CalendarStyles.Get("CalendarDimTextStyle"),
                FontSize = 10,
                HorizontalAlignment = HorizontalAlignment.Center,
                VerticalAlignment = VerticalAlignment.Top,
                Margin = new Thickness(0, 5, 0, 0),
                Text = n.ToString(CultureInfo.CurrentCulture),
            };
            AutomationProperties.SetName(t, Cal.WeekLabel(n));
            ToolTipService.SetToolTip(t, Cal.WeekLabel(n));
            Grid.SetRow(t, r);
            Cells.Children.Add(t);
        }
        cells.Clear();
        for (var i = 0; i < g.Cells.Count && i < 42; i++)
        {
            var v = new MonthCellView(g.Cells[i], env);
            v.PillActivated += (_, a) => controller?.OpenDetail(a.Pill.Occurrence);
            v.PillSelectionRequested += (_, key) => controller?.Select(key);
            // After the release: a window opened on the second press comes
            // up behind this one (AfterRelease).
            v.NewEventRequested += (_, day) => afterRelease.Run(() => controller?.NewEvent(day, Cal.Origin.MonthCell));
            Grid.SetRow(v, i / 7);
            Grid.SetColumn(v, (i % 7) + 1);
            cells.Add(v);
            Cells.Children.Add(v);
        }

        // One bar per all-day or multi-day occurrence and week row.
        Spans.Children.Clear();
        spans.Clear();
        rowLanes.Clear();
        for (var row = 0; row < 6 && (row * 7) + 7 <= g.Cells.Count; row++)
        {
            var weekDays = g.Cells.Skip(row * 7).Take(7).Select(c => c.Day).ToList();
            var list = Cal.MonthLanes(occurrences, weekDays, env.Zone, showDeclined);
            rowLanes.Add(Cal.LaneCount(list));
            foreach (var s in list)
            {
                var v = new MonthSpanBarView(s, row, ColorOf(s.Occurrence), LabelOf(s.Occurrence)) { Selected = s.Key == selection };
                v.Activated += (_, _) => controller?.OpenDetail(v.Occurrence);
                v.SelectionRequested += (_, _) => controller?.Select(v.Key);
                spans.Add(v);
                Spans.Children.Add(v);
            }
        }
        capacities = [];
        Layout();
    }

    private void OnBodySizeChanged(object sender, SizeChangedEventArgs e) => Layout();

    // The bars and the pills' top for the cells' size, then the capacity
    // of each row from Core whenever it changes what fits.
    private void Layout()
    {
        var width = Body.ActualWidth - CalendarMetrics.WeekNumberWidth;
        var height = Body.ActualHeight;
        if (width <= 0 || height <= 0)
        {
            return;
        }
        var cw = width / 7;
        var ch = height / 6;
        for (var i = 0; i < cells.Count; i++)
        {
            var lanes = i / 7 < rowLanes.Count ? rowLanes[i / 7] : 0;
            cells[i].PillTop = CalendarMetrics.CellNumberLine + (lanes * CalendarMetrics.PillLine);
        }
        foreach (var v in spans)
        {
            var rowTop = v.Row * ch;
            var y = rowTop + CalendarMetrics.CellNumberLine + (v.Span.Lane * CalendarMetrics.PillLine);
            var x0 = (v.Span.StartCol * cw) + 3;
            var x1 = ((v.Span.EndCol + 1) * cw) - 3;
            Canvas.SetLeft(v, x0);
            Canvas.SetTop(v, y);
            v.Width = Math.Max(0, x1 - x0);
            v.Height = CalendarMetrics.PillLine - 1;
            // A lane the row has no room for is not drawn.
            v.Visibility = y + CalendarMetrics.PillLine > rowTop + ch ? Visibility.Collapsed : Visibility.Visible;
        }
        var caps = rowLanes.Select(lanes => Cal.CellCapacity(
            ch - CalendarMetrics.CellNumberLine, CalendarMetrics.PillLine, lanes, CalendarMetrics.PillLine)).ToList();
        if (!caps.SequenceEqual(capacities))
        {
            capacities = caps;
            Fill();
        }
    }

    // The pills that fit each cell, "+N more" for the rest.
    private void Fill()
    {
        for (var i = 0; i < cells.Count; i++)
        {
            var capacity = i / 7 < capacities.Count ? capacities[i / 7] : 0;
            var day = cells[i].Cell.Day;
            var (pills, more) = Cal.CellItems(occurrences, day, capacity, env.Zone, showDeclined);
            var all = more > 0 ? Cal.CellItems(occurrences, day, int.MaxValue, env.Zone, showDeclined).Pills : pills;
            cells[i].Set(pills, more, all, ColorOf, LabelOf, selection);
        }
    }

    // What the view was last built from (macOS Input).
    private sealed record Input(
        Cal.MonthGrid Grid, IReadOnlyList<string> Headers, IReadOnlyList<Occurrence> Occurrences, bool ShowDeclined, string Colors,
        Cal.Weekday WeekStart)
    {
        public bool Same(Input other) =>
            Grid == other.Grid && Headers.SequenceEqual(other.Headers) && Occurrences.SequenceEqual(other.Occurrences)
            && ShowDeclined == other.ShowDeclined && Colors == other.Colors && WeekStart == other.WeekStart;
    }
}
