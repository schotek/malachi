// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/CalendarAlertsController.swift
// (CalendarAlertsController.Request); Swift-first, no GTK counterpart yet
// (docs/calendar.md §5.9). Windows: Tag is the toast's tag (at most 64
// characters, DesktopNotification.Identifier), which Swift's identifier
// needs no rule for.

using System;
using Malachi.Core.Api;
using Malachi.Core.Presentation;

namespace Malachi.Core.Controllers;

public sealed partial class CalendarAlertsController
{
    /// <summary>What the app presents for one alert (Swift <c>Request</c>).</summary>
    public sealed record Request
    {
        /// <summary>The alert.</summary>
        public required CalendarAlertId AlertId { get; init; }

        /// <summary>The event's title, cleaned (<see cref="Calendars.Cal.AlertNotification"/>).</summary>
        public required string Title { get; init; }

        /// <summary>When it starts and where ("in 5 minutes · Room B").</summary>
        public required string Body { get; init; }

        /// <summary>Offer Join (the event has a video call; its link comes from event.get when the user clicks it).</summary>
        public bool Join { get; init; }

        /// <summary>The event.</summary>
        public required CalendarEventId EventId { get; init; }

        /// <summary>The occurrence of a series.</summary>
        public DateOrTime? RecurrenceId { get; init; }

        /// <summary>The event's account.</summary>
        public required AccountId AccountId { get; init; }

        /// <summary>The day the click opens in the Calendar (<see cref="CalendarController.Show"/>).</summary>
        public DateTimeOffset? Day { get; init; }

        /// <summary>The toast's tag: "alert-" and the id, made to fit (Windows-only).</summary>
        public string Tag => TagOf(AlertId);
    }

    /// <summary>The toast tag of alert <paramref name="id"/>'s notification (Windows-only).</summary>
    public static string TagOf(CalendarAlertId id) => DesktopNotification.Identifier("alert-" + (id.Value ?? ""));
}
