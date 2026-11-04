// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Notifications/NotificationService.swift
// (NotificationService.AlertResponse); Swift-first, no GTK counterpart yet
// (docs/calendar.md §5.9). Windows has no Dismiss: an unpackaged app's toast
// does not report its close button (AppNotificationManager raises
// NotificationInvoked only for a click on the toast or on one of its
// buttons), so the close leaves the alert to the next click, to Snooze or to
// another client's dismissal (windows/README.md, the deviation table).

namespace Malachi.Platform.Windows.Notifications;

/// <summary>What the user did with an event's alert toast (Swift <c>AlertResponse</c>).</summary>
public enum CalendarAlertResponse
{
    /// <summary>A click on the toast: the Calendar with the event's detail.</summary>
    Open,

    /// <summary>Join: the video call through the link confirmation.</summary>
    Join,

    /// <summary>Snooze: again in a few minutes.</summary>
    Snooze,
}
