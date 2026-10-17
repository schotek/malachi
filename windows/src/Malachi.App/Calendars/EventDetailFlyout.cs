// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/EventDetailPopover.swift
// (EventDetailPopover: show(eventID:recurrenceID:relativeTo:of:),
// show(occurrence:relativeTo:of:), isShown, close, present, closed); GTK:
// the event detail follows (docs/calendar.md §6.4).
//
// The event detail flyout of the application (docs/calendar.md §5.5, M6):
// one at a time, showing another closes the open one. Both ways in need
// only the main window and the anchor: the window's calendar source
// (AppState.StartedCalendarSourceFor) and the calendar's zone and formats
// (AppState.CalendarEnv) make the EventDetailController; the Mail
// preference "Ask before moving to the Trash" (confirm-delete) is its
// confirmDelete. By id, the occurrence shown meanwhile is the window's
// selection when it is that occurrence; by occurrence, its title and time
// show until event.get answers, and an occurrence without an event (an
// invitation not in a calendar) shows nothing.

using System;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;

namespace Malachi.App.Calendars;

/// <summary>The application's event detail flyout.</summary>
internal static class EventDetailFlyout
{
    private static EventDetailPresenter? current;

    /// <summary>Whether a detail flyout is open (Escape's Cal.EscapeFor).</summary>
    public static bool IsOpen => current?.IsOpen == true;

    /// <summary>
    /// The detail of event <paramref name="id"/> (occurrence
    /// <paramref name="recurrenceId"/>; null for a single event or the
    /// series) beside <paramref name="anchor"/> in <paramref name="owner"/>.
    /// </summary>
    public static void Show(AppState state, MainWindow owner, CalendarEventId id, DateOrTime? recurrenceId, FrameworkElement anchor)
    {
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(owner);
        ArgumentNullException.ThrowIfNull(anchor);
        var source = state.StartedCalendarSourceFor(owner);
        var selected = owner.CalendarController?.SelectedOccurrence;
        var shown = selected is not null && selected.EventId == id && selected.RecurrenceId == recurrenceId ? selected : null;
        var settings = state.Settings;
        var c = new EventDetailController(source, id, recurrenceId, shown, AppState.CalendarEnv(), () => settings.ConfirmDelete);
        Present(state, owner, c, anchor);
    }

    /// <summary>
    /// The detail of occurrence <paramref name="o"/> beside
    /// <paramref name="anchor"/> in <paramref name="owner"/>; nothing for an
    /// occurrence without an event.
    /// </summary>
    public static void Show(AppState state, MainWindow owner, Occurrence o, FrameworkElement anchor)
    {
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(owner);
        ArgumentNullException.ThrowIfNull(o);
        ArgumentNullException.ThrowIfNull(anchor);
        var settings = state.Settings;
        if (EventDetailController.ForOccurrence(
            state.StartedCalendarSourceFor(owner), o, AppState.CalendarEnv(), () => settings.ConfirmDelete) is not { } c)
        {
            return;
        }
        Present(state, owner, c, anchor);
    }

    /// <summary>Closes the open flyout, if one is.</summary>
    public static void Close() => current?.Close();

    /// <summary>Whether <paramref name="presenter"/> is the application's flyout (none has replaced it).</summary>
    public static bool IsCurrent(EventDetailPresenter presenter) => ReferenceEquals(current, presenter);

    /// <summary>A flyout closed (EventDetailPresenter.End).</summary>
    public static void Closed(EventDetailPresenter presenter)
    {
        if (ReferenceEquals(current, presenter))
        {
            current = null;
        }
    }

    private static void Present(AppState state, MainWindow owner, EventDetailController c, FrameworkElement anchor)
    {
        current?.Close();
        var p = new EventDetailPresenter(state, owner, c);
        current = p;
        p.Show(anchor);
    }
}
