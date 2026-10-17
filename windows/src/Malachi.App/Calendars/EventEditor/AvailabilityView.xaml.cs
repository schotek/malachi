// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/EventEditor/AvailabilityView.swift
// (Content, axis, x, instant, draw, updateAccessibility); GTK: the event
// editor follows (docs/calendar.md §6.4), the rows are
// ui/internal/calendar/availability.go (via
// EventEditorController.AvailabilityRows). The names are plain text
// (Cal.CleanLine in Core), drawn as TextBlock.Text, never markup. The
// drawing is made again when the content, the width or the theme changes;
// Narrator reads the rows from the help text, one line each.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Shapes;

namespace Malachi.App.Calendars;

/// <summary>The event editor's availability rows.</summary>
public sealed partial class AvailabilityView : UserControl
{
    /// <summary>The axis's first hour.</summary>
    public const int AxisStartHour = 8;

    /// <summary>The axis's last hour.</summary>
    public const int AxisEndHour = 18;

    private const double RowHeight = 20;
    private const double AxisHeight = 16;
    private const double NameWidth = 120;
    private const double SmallText = 11;

    private IReadOnlyList<Cal.AvailabilityRow> rows = [];
    private DateTimeOffset from;
    private DateTimeOffset to;
    private DateTimeOffset start;
    private DateTimeOffset end;
    private Cal.Env? env;

    /// <summary>No rows.</summary>
    public AvailabilityView()
    {
        InitializeComponent();
        AutomationProperties.SetName(this, Cal.FieldLabel(Cal.Field.Availability));
        SizeChanged += (_, _) => Redraw();
        ActualThemeChanged += (_, _) => Redraw();
    }

    /// <summary>
    /// Shows <paramref name="people"/> over the day
    /// [<paramref name="dayFrom"/>, <paramref name="dayTo"/>) (the rows'
    /// fractions span it) with the proposed time
    /// [<paramref name="proposedStart"/>, <paramref name="proposedEnd"/>).
    /// </summary>
    public void Show(
        IReadOnlyList<Cal.AvailabilityRow> people, DateTimeOffset dayFrom, DateTimeOffset dayTo,
        DateTimeOffset proposedStart, DateTimeOffset proposedEnd, Cal.Env zoneAndFormats)
    {
        ArgumentNullException.ThrowIfNull(people);
        ArgumentNullException.ThrowIfNull(zoneAndFormats);
        rows = people;
        from = dayFrom;
        to = dayTo;
        start = proposedStart;
        end = proposedEnd;
        env = zoneAndFormats;
        Height = AxisHeight + (people.Count * RowHeight);
        var lines = people.Select(r => r.Known ? r.Label : Cal.Text.Dot(r.Label, Cal.Unknown));
        AutomationProperties.SetHelpText(this, string.Join("\n", lines));
        Redraw();
    }

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new FrameworkElementAutomationPeer(this);

    // The axis's instants: 8:00 and 18:00 of the day, in its zone.
    private (DateTimeOffset From, DateTimeOffset To) Axis()
    {
        var p = Cal.PartsOf(from, env!.Zone);
        return (Cal.Date(p.Year, p.Month, p.Day, AxisStartHour, 0, env.Zone), Cal.Date(p.Year, p.Month, p.Day, AxisEndHour, 0, env.Zone));
    }

    // An instant as an x of the lanes (clamped).
    private double X(DateTimeOffset t, double left, double width)
    {
        var (a, b) = Axis();
        var total = (b - a).TotalSeconds;
        if (total <= 0)
        {
            return left;
        }
        var f = Math.Clamp((t - a).TotalSeconds / total, 0, 1);
        return left + (f * width);
    }

    // A fraction of the whole day as an instant.
    private DateTimeOffset Instant(double f) => from + TimeSpan.FromTicks((long)(f * (to - from).Ticks));

    private void Redraw()
    {
        Surface.Children.Clear();
        if (env is null || ActualWidth <= NameWidth)
        {
            return;
        }
        var left = NameWidth;
        var width = ActualWidth - NameWidth;
        var laneHeight = Math.Max(0, rows.Count * RowHeight);

        // The hours along the top, every two hours, and their hairlines.
        var p = Cal.PartsOf(from, env.Zone);
        for (var h = AxisStartHour; h <= AxisEndHour; h++)
        {
            var t = Cal.Date(p.Year, p.Month, p.Day, h, 0, env.Zone);
            var tx = X(t, left, width);
            Add(new Rectangle { Width = 1, Height = laneHeight, Fill = TickSwatch.Fill }, tx, AxisHeight);
            if ((h - AxisStartHour) % 2 == 0)
            {
                var label = new TextBlock { Text = Cal.Text.TimeText(t, env), FontSize = SmallText, Foreground = DimSwatch.Fill };
                label.Measure(new Windows.Foundation.Size(double.PositiveInfinity, double.PositiveInfinity));
                var w = label.DesiredSize.Width;
                Add(label, Math.Clamp(tx - (w / 2), left, Math.Max(left, left + width - w)), 0);
            }
        }

        // The proposed time, behind the rows.
        var sx = X(start, left, width);
        var ex = X(end, left, width);
        if (ex > sx)
        {
            Add(new Rectangle { Width = ex - sx, Height = laneHeight, Fill = ProposedSwatch.Fill }, sx, AxisHeight);
            Add(new Rectangle { Width = 1.5, Height = laneHeight, Fill = ProposedEdgeSwatch.Fill }, sx, AxisHeight);
            Add(new Rectangle { Width = 1.5, Height = laneHeight, Fill = ProposedEdgeSwatch.Fill }, ex - 1.5, AxisHeight);
        }

        for (var i = 0; i < rows.Count; i++)
        {
            var row = rows[i];
            var y = AxisHeight + (i * RowHeight);
            Add(
                new TextBlock
                {
                    Text = row.Label,
                    FontSize = SmallText,
                    Foreground = NameSwatch.Fill,
                    Width = NameWidth - 8,
                    MaxLines = 1,
                    TextTrimming = TextTrimming.CharacterEllipsis,
                    TextWrapping = TextWrapping.NoWrap,
                },
                0,
                y + 3);
            Add(new Rectangle { Width = width, Height = RowHeight - 8, RadiusX = 3, RadiusY = 3, Fill = LaneSwatch.Fill }, left, y + 4);
            if (!row.Known)
            {
                Add(new TextBlock { Text = Cal.Unknown, FontSize = SmallText - 1, Foreground = UnknownSwatch.Fill }, left + 6, y + 3);
                continue;
            }
            foreach (var span in row.Busy)
            {
                var bx = X(Instant(span.From), left, width);
                var bw = X(Instant(span.To), left, width) - bx;
                if (bw <= 0)
                {
                    continue;
                }
                var fill = span.ShowAs == ShowAs.Tentative ? TentativeSwatch.Fill : BusySwatch.Fill;
                Add(new Rectangle { Width = bw, Height = RowHeight - 8, RadiusX = 2, RadiusY = 2, Fill = fill }, bx, y + 4);
            }
        }
    }

    private void Add(FrameworkElement element, double x, double y)
    {
        Canvas.SetLeft(element, x);
        Canvas.SetTop(element, y);
        AutomationProperties.SetAccessibilityView(element, AccessibilityView.Raw);
        Surface.Children.Add(element);
    }
}
