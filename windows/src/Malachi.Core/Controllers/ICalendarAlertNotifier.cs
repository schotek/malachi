// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the seam between CalendarAlertsController and the
// platform's notifications, which macOS keeps as the controller's onPresent
// and onWithdraw (macos/Sources/MalachiCore/Controllers/CalendarAlertsController.swift),
// presented by NotificationService.swift (category EVENT_ALERT). The mail's
// IDesktopNotifier carries a message's ids; an event's alert carries an
// event and offers Join and Snooze, so it has a seam of its own. The
// Windows implementation is the app's notification service over
// AppNotificationManager: Request.Tag is the toast's tag, Cal.Join and
// Cal.Snooze its buttons; its Snooze calls CalendarAlertsController.Snooze,
// a click or a dismissal CalendarAlertsController.Dismiss.

using Malachi.Core.Api;

namespace Malachi.Core.Controllers;

/// <summary>Shows the events' alerts as desktop notifications and takes them back.</summary>
public interface ICalendarAlertNotifier
{
    /// <summary>
    /// Shows <paramref name="request"/>, or replaces the one shown under its
    /// <see cref="CalendarAlertsController.Request.Tag"/> (Swift
    /// <c>onPresent</c>). Plain text only. Never throws: a platform that
    /// cannot show it logs why and drops it.
    /// </summary>
    void Present(CalendarAlertsController.Request request);

    /// <summary>
    /// Removes the notification of alert <paramref name="alertId"/> (Swift
    /// <c>onWithdraw</c>); one with nothing shown is ignored. Never throws.
    /// </summary>
    void Withdraw(CalendarAlertId alertId);
}
