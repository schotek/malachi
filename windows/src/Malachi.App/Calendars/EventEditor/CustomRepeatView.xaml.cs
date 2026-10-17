// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/EventEditor/EventEditorPopovers.swift
// (EventEditorRepeatViewController: init, fillUnits, frequency, stepped,
// intervalChanged, unitChanged, apply); GTK: the event editor follows
// (docs/calendar.md §6.4), the rule is ui/internal/calendar/recurrence.go
// (CustomRecurrence). The interval starts at the rule's (1 without one,
// clamped to the API's limit), the unit at its frequency (weekly without
// one), the days at the rule's, else the start's; Done applies
// Cal.CustomRecurrence. Windows: the interval is a NumberBox with its spin
// buttons (macOS's field and stepper), the days are toggle buttons with the
// short weekday names.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.I18n;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;

namespace Malachi.App.Calendars;

/// <summary>Repeat ▸ Custom…'s flyout of the event editor.</summary>
public sealed partial class CustomRepeatView : UserControl
{
    private static readonly RecurrenceFrequency[] Frequencies =
        [RecurrenceFrequency.Daily, RecurrenceFrequency.Weekly, RecurrenceFrequency.Monthly, RecurrenceFrequency.Yearly];

    private readonly DateTimeOffset start;
    private readonly TimeZoneInfo zone;
    private readonly List<(Cal.Weekday Day, ToggleButton Button)> days = [];
    private bool filling;

    /// <summary>
    /// The flyout for <paramref name="current"/> (the rule in force, if any)
    /// of an event starting at <paramref name="eventStart"/> in
    /// <paramref name="eventZone"/>.
    /// </summary>
    public CustomRepeatView(Recurrence? current, DateTimeOffset eventStart, TimeZoneInfo eventZone, Cal.Env env)
    {
        ArgumentNullException.ThrowIfNull(eventZone);
        ArgumentNullException.ThrowIfNull(env);
        start = eventStart;
        zone = eventZone;
        InitializeComponent();
        EveryText.Text = Cal.EveryLabel;
        DoneButton.Content = L10n.T("Done");
        IntervalBox.Maximum = API.Limits.MaxRecurrenceInterval;
        AutomationProperties.SetName(IntervalBox, Cal.EveryLabel);
        var f = current?.Frequency ?? RecurrenceFrequency.Weekly;
        var n = Math.Min(Math.Max(1, current?.Interval ?? 1), API.Limits.MaxRecurrenceInterval);
        filling = true;
        IntervalBox.Value = n;
        filling = false;
        // A week's days: the rule's, else the start's.
        var codes = new HashSet<string>(current?.ByDay ?? [], StringComparer.Ordinal);
        var first = Cal.StartOfDay(eventStart, eventZone);
        var startDay = Cal.PartsOf(eventStart, eventZone).Weekday;
        var week = new List<(Cal.Weekday Day, DateTimeOffset Date)>();
        for (var k = 0; k < 7; k++)
        {
            var d = Cal.AddDays(first, k, eventZone);
            week.Add((Cal.PartsOf(d, eventZone).Weekday, d));
        }
        foreach (var (w, d) in week.OrderBy(x => ((int)x.Day + 6) % 7))
        {
            var b = new ToggleButton
            {
                Content = Cal.Text.ShortWeekdayText(d, env),
                IsChecked = codes.Count == 0 ? w == startDay : codes.Contains(Cal.DayCode(w)),
                MinWidth = 0,
                Padding = new Thickness(8, 4, 8, 4),
            };
            AutomationProperties.SetAutomationId(b, "EventEditorRepeatDay" + Cal.DayCode(w));
            days.Add((w, b));
            DaysRow.Children.Add(b);
        }
        FillUnits(Frequencies.Contains(f) ? f : RecurrenceFrequency.Weekly);
    }

    /// <summary>Done: the rule to apply.</summary>
    public event EventHandler<Recurrence>? Applied;

    /// <summary>The keyboard to the interval (the flyout opened).</summary>
    public void FocusInterval() => IntervalBox.Focus(FocusState.Programmatic);

    private int Interval
    {
        get
        {
            var v = IntervalBox.Value;
            return double.IsNaN(v) ? 1 : Math.Min(Math.Max(1, (int)Math.Round(v)), API.Limits.MaxRecurrenceInterval);
        }
    }

    private RecurrenceFrequency Frequency => Frequencies[Math.Max(0, UnitBox.SelectedIndex)];

    // The units for the interval ("day"/"days", …), the selection kept.
    private void FillUnits(RecurrenceFrequency selected)
    {
        filling = true;
        var n = Interval;
        UnitBox.Items.Clear();
        foreach (var f in Frequencies)
        {
            UnitBox.Items.Add(Cal.IntervalUnit(f, n));
        }
        var i = Array.IndexOf(Frequencies, selected);
        UnitBox.SelectedIndex = i >= 0 ? i : 1;
        filling = false;
        DaysRow.Visibility = Frequency == RecurrenceFrequency.Weekly ? Visibility.Visible : Visibility.Collapsed;
    }

    private void OnIntervalChanged(NumberBox sender, NumberBoxValueChangedEventArgs args)
    {
        if (filling)
        {
            return;
        }
        var n = Interval;
        if (sender.Value != n)
        {
            filling = true;
            sender.Value = n;
            filling = false;
        }
        FillUnits(Frequency);
    }

    private void OnUnitChanged(object sender, SelectionChangedEventArgs e)
    {
        if (filling || UnitBox.SelectedIndex < 0)
        {
            return;
        }
        DaysRow.Visibility = Frequency == RecurrenceFrequency.Weekly ? Visibility.Visible : Visibility.Collapsed;
    }

    private void OnDoneClick(object sender, RoutedEventArgs e)
    {
        var chosen = days.Where(d => d.Button.IsChecked == true).Select(d => d.Day).ToList();
        Applied?.Invoke(this, Cal.CustomRecurrence(Frequency, Interval, chosen, start, zone));
    }
}
