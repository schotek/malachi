// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/CalendarAlertsController.swift
// (CalendarAlertsController; its Request is CalendarAlertsController.Request.cs);
// Swift-first, no GTK counterpart yet (docs/calendar.md §5.9).
//
// The events' alerts for the whole application, owned by the app state:
// notify.eventAlert due becomes a notification request the app presents
// (Join only with a video call, Snooze), dismissed (from any client)
// withdraws it; Snooze is event.snoozeAlert for Cal.SnoozeMinutes, a click
// or the close button dismisses (event.dismissAlert). The app routes the
// daemon's notifications here itself (Handle), not through a window's
// source, so that an alert is shown once whatever the number of windows.
//
// Windows: Swift's onPresent and onWithdraw are the injected
// ICalendarAlertNotifier, its onError the event Failed (a log line, not a
// toast; the controller logs the method and the error class itself), its
// now closure the TimeProvider and its enabled closure a Func. The writes
// go through a ControllerScope and are sent even when it closes right
// after (PerformPastClose), as Swift's Task is. Create it, and call it, on
// the UI thread.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Text;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>The events' alerts for the whole application (Swift <c>CalendarAlertsController</c>).</summary>
public sealed partial class CalendarAlertsController : IDisposable
{
    private readonly ICalendarAlertNotifier notifier;
    private readonly TimeProvider time;
    private readonly Func<bool> enabled;
    private readonly ILogger logger;
    private readonly ControllerScope scope;
    private readonly Dictionary<CalendarAlertId, Request> shown = [];

    /// <summary>The controller, on the calling (UI) thread.</summary>
    /// <param name="source">Where Snooze and a dismissal are written.</param>
    /// <param name="notifier">Presents and withdraws the notifications.</param>
    /// <param name="env">The zone and the date formats of the texts; the local zone and the current culture when null.</param>
    /// <param name="time">The clock of "now" (the "in 5 minutes"); the system's when null.</param>
    /// <param name="enabled">
    /// Reads Preferences → Notifications (<c>desktop-notifications</c>): off
    /// presents nothing (a dismissal still withdraws); always on when null.
    /// </param>
    /// <param name="logger">Receives method names and error classes, never an event's text.</param>
    /// <param name="pending">Counts the background work; one of its own when null.</param>
    public CalendarAlertsController(
        ICalendarDataSource source,
        ICalendarAlertNotifier notifier,
        Cal.Env? env = null,
        TimeProvider? time = null,
        Func<bool>? enabled = null,
        ILogger<CalendarAlertsController>? logger = null,
        PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(source);
        ArgumentNullException.ThrowIfNull(notifier);
        Source = source;
        this.notifier = notifier;
        Env = env ?? Cal.Env.ForCulture(TimeZoneInfo.Local, CultureInfo.CurrentCulture);
        this.time = time ?? TimeProvider.System;
        this.enabled = enabled ?? (() => true);
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <summary>A snooze or a dismissal failed: its sentence, for a log line, not a toast (Swift <c>onError</c>).</summary>
    public event EventHandler<string>? Failed;

    /// <summary>Where Snooze and a dismissal are written.</summary>
    public ICalendarDataSource Source { get; }

    /// <summary>The zone and the date formats of the texts.</summary>
    public Cal.Env Env { get; }

    /// <summary>The alerts presented and not withdrawn.</summary>
    public IReadOnlyDictionary<CalendarAlertId, Request> Shown => shown;

    /// <summary>A notification of the daemon; only <c>notify.eventAlert</c> concerns this controller.</summary>
    public void Handle(DaemonNotification notification)
    {
        ArgumentNullException.ThrowIfNull(notification);
        if (notification is DaemonNotification.EventAlert a)
        {
            Handle(a.Payload.Alert);
        }
    }

    /// <summary>An alert: due presents it (unless notifications are off), dismissed withdraws it.</summary>
    public void Handle(EventAlert a)
    {
        ArgumentNullException.ThrowIfNull(a);
        scope.VerifyAccess();
        switch (a.State.Value)
        {
            case EventAlertState.Due:
                if (!enabled())
                {
                    return;
                }
                var text = Cal.AlertNotification(a, time.GetUtcNow(), Env);
                var r = new Request
                {
                    AlertId = a.AlertId,
                    Title = text.Title,
                    Body = text.Body,
                    Join = a.VideoCall ?? false,
                    EventId = a.EventId,
                    RecurrenceId = a.RecurrenceId,
                    AccountId = a.AccountId,
                    Day = a.Start ?? Cal.ParseDate(a.StartDate, Env.Zone),
                };
                shown[a.AlertId] = r;
                notifier.Present(r);
                break;
            case EventAlertState.Dismissed:
                Withdraw(a.AlertId);
                break;
        }
    }

    /// <summary>Snooze: again in <see cref="Cal.SnoozeMinutes"/> minutes (the daemon sends due then).</summary>
    public void Snooze(CalendarAlertId id)
    {
        scope.VerifyAccess();
        Withdraw(id);
        Write(API.EventSnoozeAlert.Name, async () =>
        {
            await Source.SnoozeAlertAsync(new EventSnoozeAlertParams { AlertId = id, Minutes = Cal.SnoozeMinutes });
            return true;
        });
    }

    /// <summary>The notification was clicked or closed: dismissed for every client.</summary>
    public void Dismiss(CalendarAlertId id)
    {
        scope.VerifyAccess();
        Withdraw(id);
        Write(API.EventDismissAlert.Name, async () =>
        {
            await Source.DismissAlertAsync(new EventDismissAlertParams { AlertId = id });
            return true;
        });
    }

    /// <summary>Stops, for good: no failure is reported any more (a write asked for is still sent).</summary>
    public void Dispose() => scope.Close();

    private void Withdraw(CalendarAlertId id)
    {
        if (!shown.Remove(id))
        {
            return;
        }
        notifier.Withdraw(id);
    }

    private void Write(string method, Func<Task<bool>> call) =>
        scope.PerformPastClose(call, outcome =>
        {
            if (outcome.IsSuccess)
            {
                return;
            }
            LogCallFailed(method, outcome.Error);
            scope.Raise(Failed, this, Cal.ErrorMessage(outcome.Error));
        });

    private void LogCallFailed(string method, Exception? error)
    {
        if (logger.IsEnabled(LogLevel.Information))
        {
            var (kind, daemonError) = RpcErrorText.Classify(error);
            LogFailed(logger, method, kind, daemonError?.Code.Value ?? 0);
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "{Method} failed: {Kind} {Code}")]
    private static partial void LogFailed(ILogger logger, string method, RpcErrorText.FailureKind kind, int code);
}
