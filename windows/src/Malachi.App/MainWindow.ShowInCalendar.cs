// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MainWindow/MainWindowController+ShowInCalendar.swift
// (showInCalendar); GTK: the calendar's window code follows
// (docs/calendar.md §6.4).
//
// Show in Calendar of an invitation card and an alert's click
// (docs/calendar.md §5.7, §5.9): the main window comes forward in the
// Calendar mode, on the Day view of the event's day with the event selected,
// and its detail opens once the page has laid the day out, anchored on the
// event's view (CalendarPage.AnchorFor) or, until the page knows it, on the
// page; without an event the day alone. Windows: the queue's low priority
// stands for macOS's next turn of the main queue, after the layout.

using System;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Calendars;
using Microsoft.UI.Dispatching;

namespace Malachi.App;

/// <summary>Show in Calendar of the main window.</summary>
public sealed partial class MainWindow
{
    /// <summary>
    /// The Calendar at <paramref name="day"/> (today when null) with event
    /// <paramref name="eventId"/> (occurrence <paramref name="recurrenceId"/>)
    /// selected and its detail shown; without an event the day alone (macOS
    /// <c>showInCalendar(eventID:recurrenceID:day:)</c>).
    /// </summary>
    public void ShowInCalendar(CalendarEventId? eventId, DateOrTime? recurrenceId, DateTimeOffset? day)
    {
        state.ShowMainWindow();
        SetMode(Board.Mode.Calendar);
        if (mode != Board.Mode.Calendar)
        {
            return;
        }
        StartCalendar();
        string? key = eventId is { } id ? Cal.EventKey(id, recurrenceId) : null;
        CalendarController!.Show(day ?? TimeProvider.System.GetUtcNow(), Cal.View.Day, key);
        if (eventId is not { } shown)
        {
            return;
        }
        // After the page laid the day out.
        DispatcherQueue.TryEnqueue(DispatcherQueuePriority.Low, () =>
        {
            if (mode != Board.Mode.Calendar)
            {
                return;
            }
            var anchor = CalendarView.AnchorFor(key!) ?? CalendarView;
            state.OpenEventDetail(this, shown, recurrenceId, anchor);
        });
    }
}
