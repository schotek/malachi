// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/EventEditor/EventEditorViewController.swift
// (render's dates, set, combine, dateChanged, showZones, popoverDidClose);
// GTK: the event editor follows (docs/calendar.md §6.4), the rules are
// EventEditorController's (SetStart moves the end with it, SetEnd,
// SetZone keeps the wall-clock times).
//
// Starts and Ends of the event editor and the time-zone link. The pickers
// show the wall clock of the editor's zone (Cal.PartsOf in Input.Zone), not
// the system's: a date picker is given the day at noon of the system's
// offset (so that no conversion moves it to another day) and is read by
// its year, month and day; a time picker by its hours and minutes. An
// all-day event shows no times and its last day as Ends (the end is the day
// after it). A changed picker sets the instant in the editor's zone, and
// the form is shown again (a value the controller refused goes back).
// Windows: CalendarDatePicker and TimePicker for macOS's text-field date
// pickers; the zone's list is a flyout under the link (TimeZonePicker).

using System;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Calendars;

/// <summary>The dates and the time zone of the event editor's form.</summary>
public sealed partial class EventEditorPane
{
    // The day of t in zone, as a date picker shows it.
    private static void SetDate(CalendarDatePicker picker, DateTimeOffset t, TimeZoneInfo zone)
    {
        var p = Cal.PartsOf(t, zone);
        if (picker.Date is { } d && d.Year == p.Year && d.Month == p.Month && d.Day == p.Day)
        {
            return;
        }
        var noon = new DateTime(p.Year, p.Month, p.Day, 12, 0, 0, DateTimeKind.Unspecified);
        picker.Date = new DateTimeOffset(noon, TimeZoneInfo.Local.GetUtcOffset(noon));
    }

    // The time of day of t in zone, as a time picker shows it.
    private static void SetTime(TimePicker picker, DateTimeOffset t, TimeZoneInfo zone)
    {
        var p = Cal.PartsOf(t, zone);
        var time = new TimeSpan(p.Hour, p.Minute, 0);
        if (picker.SelectedTime != time)
        {
            picker.SelectedTime = time;
        }
    }

    // render's dates: the pickers in the editor's zone, the times and the
    // zone's link hidden for an all-day event.
    private void RenderDates(EventEditorController c)
    {
        var input = c.Input;
        var z = input.Zone;
        SetDate(StartDate, input.Start, z);
        SetTime(StartTime, input.Start, z);
        // An all-day event's end is the day after its last: Ends shows the last.
        var shownEnd = input.AllDay ? Cal.AddDays(Cal.StartOfDay(input.End, z), -1, z) : input.End;
        SetDate(EndDate, shownEnd, z);
        SetTime(EndTime, input.End, z);
        var times = input.AllDay ? Visibility.Collapsed : Visibility.Visible;
        StartTime.Visibility = times;
        EndTime.Visibility = times;
        ZoneButton.Visibility = times;
        ZoneButton.Content = CalendarDisplay.Clean(c.TimeZoneLink);
    }

    // The day of a date picker (null: none chosen) with the time of day
    // given, in the editor's zone.
    private static DateTimeOffset? Combine(CalendarDatePicker day, int hour, int minute, TimeZoneInfo zone) =>
        day.Date is { } d ? Cal.Date(d.Year, d.Month, d.Day, hour, minute, zone) : null;

    private void OnStartDateChanged(CalendarDatePicker sender, CalendarDatePickerDateChangedEventArgs args)
    {
        if (rendering || closed || controller is not { } c)
        {
            return;
        }
        var input = c.Input;
        var p = Cal.PartsOf(input.Start, input.Zone);
        if (Combine(StartDate, input.AllDay ? 0 : p.Hour, input.AllDay ? 0 : p.Minute, input.Zone) is { } t)
        {
            c.SetStart(t);
        }
        Render();
    }

    private void OnStartTimeChanged(TimePicker sender, TimePickerSelectedValueChangedEventArgs args)
    {
        if (rendering || closed || controller is not { } c || StartTime.SelectedTime is not { } time)
        {
            return;
        }
        if (Combine(StartDate, time.Hours, time.Minutes, c.Input.Zone) is { } t)
        {
            c.SetStart(t);
        }
        Render();
    }

    private void OnEndDateChanged(CalendarDatePicker sender, CalendarDatePickerDateChangedEventArgs args)
    {
        if (rendering || closed || controller is not { } c)
        {
            return;
        }
        var input = c.Input;
        var z = input.Zone;
        if (input.AllDay)
        {
            // The last day shown: the end is the day after it.
            if (Combine(EndDate, 0, 0, z) is { } last)
            {
                c.SetEnd(Cal.AddDays(Cal.StartOfDay(last, z), 1, z));
            }
        }
        else
        {
            var p = Cal.PartsOf(input.End, z);
            if (Combine(EndDate, p.Hour, p.Minute, z) is { } t)
            {
                c.SetEnd(t);
            }
        }
        Render();
    }

    private void OnEndTimeChanged(TimePicker sender, TimePickerSelectedValueChangedEventArgs args)
    {
        if (rendering || closed || controller is not { } c || EndTime.SelectedTime is not { } time)
        {
            return;
        }
        if (Combine(EndDate, time.Hours, time.Minutes, c.Input.Zone) is { } t)
        {
            c.SetEnd(t);
        }
        Render();
    }

    // showZones: the zone's search under the link; a pick sets the zone.
    private void OnZoneClick(object sender, RoutedEventArgs e)
    {
        if (closed || controller is not { } c)
        {
            return;
        }
        if (ZoneFlyout.IsOpen)
        {
            ZoneFlyout.Hide();
            return;
        }
        var picker = new TimeZonePicker(Cal.IanaZoneName(c.Input.Zone));
        picker.Picked += (_, zone) =>
        {
            controller?.SetZone(zone);
            ZoneFlyout.Hide();
        };
        ZoneFlyout.Content = picker;
        ZoneFlyout.Opened += FocusSearch;
        ZoneFlyout.ShowAt(ZoneButton);

        void FocusSearch(object? s, object args)
        {
            ZoneFlyout.Opened -= FocusSearch;
            picker.FocusSearch();
        }
    }

    private void OnZoneFlyoutClosed(object sender, object e) => ZoneFlyout.Content = null;
}
