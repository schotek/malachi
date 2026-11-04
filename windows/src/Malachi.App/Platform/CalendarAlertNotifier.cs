// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Integration+Calendar.swift
// (wireCalendarNotifications' onPresent and onWithdraw, alertAnswered) and
// of NotificationService.swift (presentAlert, withdrawAlert); Swift-first,
// no GTK counterpart yet (docs/calendar.md §5.9).
//
// The events' alerts as toasts: CalendarAlertsController asks for one
// (Present) once Preferences → Notifications allows it, and this plays the
// new-mail sound under its own setting (notification-sound) and outside
// Windows' quiet hours, as for mail, then shows the toast through the
// platform services (PlatformServices.PresentAlert: the title, the body,
// Join only with a video call, Snooze, the alert's tag). An alert shows even
// while the main window is active: it is about the time, not about a
// folder. Withdraw removes it by its tag (dismissed by any client, snoozed).
//
// What the user did with it comes back through Program (Answer, on the UI
// thread): a click on the toast dismisses the alert for every client and
// opens the Calendar on the event's day with its detail
// (MainWindow.ShowInCalendar); Join dismisses it and opens the video call
// through the app's "Open This Link?" (Integration.JoinVideoCall, whose
// link comes from event.get, never from the toast), in macOS's order; Snooze
// puts it off Cal.SnoozeMinutes. Deviation (named, windows/README.md): the
// toast's close button is not reported to an unpackaged app
// (AppNotificationManager raises nothing for it), so closing a toast does
// not dismiss the alert for the other clients as macOS's customDismissAction
// does; it stays in the notification centre, where a later click still
// dismisses it.

using System;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Presentation;
using Malachi.Platform.Windows.Notifications;

namespace Malachi.App.Platform;

/// <summary>The events' alerts as Windows toasts, and the answers to them.</summary>
internal sealed class CalendarAlertNotifier : ICalendarAlertNotifier
{
    private readonly AppState state;
    private readonly INewMailSound sound;

    /// <summary>A notifier for <paramref name="state"/>'s alerts, with <paramref name="sound"/> as mail's.</summary>
    public CalendarAlertNotifier(AppState state, INewMailSound sound)
    {
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(sound);
        this.state = state;
        this.sound = sound;
    }

    /// <inheritdoc/>
    public void Present(CalendarAlertsController.Request request)
    {
        ArgumentNullException.ThrowIfNull(request);
        if (state.Settings.NotificationSound && !sound.IsQuietTime)
        {
            sound.Play();
        }
        PlatformServices.PresentAlert(request);
    }

    /// <inheritdoc/>
    public void Withdraw(CalendarAlertId alertId) => PlatformServices.WithdrawAlert(alertId);

    /// <summary>
    /// A click on an alert's toast or one of its buttons (macOS
    /// <c>alertAnswered</c>), on the UI thread.
    /// </summary>
    public void Answer(CalendarAlertActivation alert)
    {
        ArgumentNullException.ThrowIfNull(alert);
        var alerts = state.CalendarAlerts;
        switch (alert.Response)
        {
            case CalendarAlertResponse.Open:
                alerts.Dismiss(alert.AlertId);
                state.MainWindow?.ShowInCalendar(alert.EventId, alert.RecurrenceId, alert.Day);
                break;
            case CalendarAlertResponse.Join:
                alerts.Dismiss(alert.AlertId);
                state.Integration?.JoinVideoCall(alert.EventId, alert.RecurrenceId);
                break;
            case CalendarAlertResponse.Snooze:
                alerts.Snooze(alert.AlertId);
                break;
        }
    }
}
