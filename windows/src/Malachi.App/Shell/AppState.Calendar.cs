// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Integration+Calendar.swift
// (AppCalendar, AppState.calendarSource(for:), calendarAlerts) and
// App/Integration+CalDAV.swift (startedCalendarSource(for:),
// showAddCalDAVCalendar, showCalendarPreferences); GTK: the calendar's
// window code follows (docs/calendar.md §6.4).
//
// The application's calendar (docs/calendar.md §5.2, §5.3, §5.9):
//
// - one calendar source per window that shows the calendar
//   (CalendarSourceFor: the main window, the Preferences, a CalDAV sheet's
//   owner), made on first use over the application's client, or the
//   invented samples with MALACHI_CALENDAR_SAMPLES=1 (CalendarSamples, read
//   once at start: every window's source is then the in-memory one, §5.10);
//   a daemon's source starts once (StartedCalendarSourceFor; the main
//   window's on its first entry into the Calendar) and is stopped and
//   disposed with its window;
// - the application's CalendarAlertsController (CalendarAlerts), presented
//   by CalendarAlertNotifier as toasts, enabled while Preferences →
//   Notifications shows desktop notifications, as for mail; it writes
//   Snooze and the dismissals through a source of its own that is never
//   started (macOS does the same; the C# controller only calls
//   event.snoozeAlert and event.dismissAlert on it, which need no start);
//   a refused write is a log line;
// - the routing of the daemon's notifications: the hub hands each of the
//   calendar's four (notify.calendarChanged, notify.calendarState,
//   notify.eventAlert, notify.invitationsChanged) to every window's source
//   and then, once for the application, to the alerts; notify.accountsChanged
//   and the connection reach the daemon's sources (AccountsChanged,
//   ConnectionChanged), as for the board. A source not started yet ignores
//   them;
// - the entry points the calendar's views and the other windows call (an
//   invitation's Show in Calendar, an alert, Preferences → Accounts): the
//   event's detail flyout over its anchor (OpenEventDetail →
//   EventDetailFlyout), the New Event and edit windows over the main
//   window's source (OpenNewEvent, OpenEditEvent → EventEditors), the Add
//   CalDAV Calendar dialog over the asking window or else the main window
//   (ShowAddCalDAVCalendar → CalDAVWizardWindow), and the Preferences on
//   their Calendar page (ShowCalendarPreferences).

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using Malachi.App.Platform;
using Malachi.App.Preferences;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Platform.Windows.Notifications;
using Malachi.Platform.Windows.Sound;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;

namespace Malachi.App.Shell;

/// <summary>The calendar's application-wide state.</summary>
public sealed partial class AppState
{
    // Every window's calendar source, by window (reference).
    private readonly Dictionary<Window, CalendarSourceEntry> calendarSources = new(ReferenceEqualityComparer.Instance);
    private readonly List<IDisposable> calendarTokens = [];
    private ICalendarDataSource? calendarAlertsSource;
    private CalendarAlertNotifier? calendarNotifier;
    private ILogger calendarLogger = Microsoft.Extensions.Logging.Abstractions.NullLogger.Instance;

    /// <summary>
    /// The calendar runs on the invented samples (MALACHI_CALENDAR_SAMPLES=1,
    /// read once at start): every window's source is the in-memory one.
    /// </summary>
    public bool CalendarSamples { get; private set; }

    /// <summary>The events' alerts for the whole application (macOS <c>calendarAlerts</c>).</summary>
    public CalendarAlertsController CalendarAlerts { get; private set; } = null!;

    /// <summary>
    /// The calendar's zone and date formats: the local zone and the user's
    /// regional format (the controllers' default, made explicit so that the
    /// windows and the alerts read them the same way).
    /// </summary>
    public static Cal.Env CalendarEnv() => Cal.Env.ForCulture(TimeZoneInfo.Local, CultureInfo.CurrentCulture);

    /// <summary>
    /// The calendar source of <paramref name="window"/>, made on first ask and
    /// not started (macOS <c>calendarSource(for:)</c>): the daemon's over the
    /// application's client, or the samples. It is stopped and disposed when
    /// the window closes.
    /// </summary>
    public ICalendarDataSource CalendarSourceFor(Window window)
    {
        ArgumentNullException.ThrowIfNull(window);
        return EntryFor(window).Source;
    }

    /// <summary>
    /// The calendar source of <paramref name="window"/>, started the first
    /// time (macOS <c>startedCalendarSource(for:)</c> and the source's part of
    /// <c>startCalendar</c>): from then on it loads and follows the daemon's
    /// notifications. The samples need no start.
    /// </summary>
    public ICalendarDataSource StartedCalendarSourceFor(Window window)
    {
        ArgumentNullException.ThrowIfNull(window);
        var entry = EntryFor(window);
        if (!entry.Started)
        {
            entry.Started = true;
            (entry.Source as DaemonCalendarSource)?.Start();
        }
        return entry.Source;
    }

    /// <summary>
    /// A click on an event's alert toast or one of its buttons (Program, on
    /// the UI thread): the Calendar with the event, Join or Snooze
    /// (CalendarAlertNotifier.Answer). Nothing past Quit's point of no return.
    /// </summary>
    public void AnswerCalendarAlert(CalendarAlertActivation alert)
    {
        ArgumentNullException.ThrowIfNull(alert);
        if (IsStopping)
        {
            return;
        }
        calendarNotifier?.Answer(alert);
    }

    // Entry points of the calendar's views (the header's last item).

    /// <summary>
    /// The detail flyout of event <paramref name="id"/> (occurrence
    /// <paramref name="recurrenceId"/>) over <paramref name="anchor"/> in
    /// <paramref name="owner"/> (macOS <c>EventDetailPopover.show(eventID:recurrenceID:relativeTo:of:)</c>):
    /// Show in Calendar and an alert's click.
    /// </summary>
    public void OpenEventDetail(MainWindow owner, CalendarEventId id, DateOrTime? recurrenceId, FrameworkElement anchor)
    {
        ArgumentNullException.ThrowIfNull(owner);
        ArgumentNullException.ThrowIfNull(anchor);
        Calendars.EventDetailFlyout.Show(this, owner, id, recurrenceId, anchor);
    }

    /// <summary>
    /// The detail flyout of <paramref name="occurrence"/> over
    /// <paramref name="anchor"/> in <paramref name="owner"/> (the controller's
    /// OpenDetailRequested: a click on a block, Return on the selection).
    /// </summary>
    public void OpenEventDetail(MainWindow owner, Occurrence occurrence, FrameworkElement anchor)
    {
        ArgumentNullException.ThrowIfNull(owner);
        ArgumentNullException.ThrowIfNull(occurrence);
        ArgumentNullException.ThrowIfNull(anchor);
        Calendars.EventDetailFlyout.Show(this, owner, occurrence, anchor);
    }

    /// <summary>
    /// The New Event window with <paramref name="defaults"/> (macOS
    /// <c>EventEditorManager.openNew</c>): New Event of the window's commands,
    /// "+", a double click on empty time.
    /// </summary>
    public void OpenNewEvent(Window? owner, Cal.Defaults defaults)
    {
        ArgumentNullException.ThrowIfNull(defaults);
        // The windows work over the main window's source, whoever asks
        // (macOS EventEditorManager.open); owner is for symmetry with the
        // detail's entry points.
        _ = owner;
        EventEditors.OpenNew(defaults);
    }

    /// <summary>
    /// The editor of event <paramref name="id"/> (occurrence
    /// <paramref name="recurrenceId"/>) for <paramref name="scope"/>, looking
    /// for a free slot first when <paramref name="findFreeSlot"/> (macOS
    /// <c>EventEditorManager.openEdit</c>; the detail's EditRequested).
    /// </summary>
    public void OpenEditEvent(Window? owner, CalendarEventId id, DateOrTime? recurrenceId, EditScope scope, bool findFreeSlot)
    {
        _ = owner;
        EventEditors.OpenEdit(id, recurrenceId, scope, findFreeSlot);
    }

    /// <summary>
    /// The Add CalDAV Calendar dialog over <paramref name="owner"/> (macOS
    /// <c>showAddCalDAVCalendar(from:onDone:)</c>): Preferences → Calendar's
    /// Add CalDAV Calendar…, Preferences → Integrations' CalDAV Calendar
    /// Add…, the … menus' and the No Accounts page's Add CalDAV Calendar…,
    /// the Calendar sidebar's Add Calendar…; <paramref name="onDone"/>
    /// runs after account.add with the new account.
    /// </summary>
    public void ShowAddCalDAVCalendar(Window owner, Action<AccountId>? onDone)
    {
        ArgumentNullException.ThrowIfNull(owner);
        if (IsStopping)
        {
            return;
        }
        // Over the window it was asked from while that one shows, else the
        // main window (shown first); the window's calendar source names the
        // mail accounts, and its snapshot is asked anew after the add.
        var parent = owner.AppWindow?.IsVisible == true ? owner : null;
        if (parent is null)
        {
            ShowMainWindow();
            parent = MainWindow;
        }
        if (parent is null)
        {
            return;
        }
        Malachi.App.Wizard.CalDAVWizardWindow.Show(this, parent, StartedCalendarSourceFor(parent), onDone);
    }

    /// <summary>
    /// The Preferences on their Calendar page (macOS
    /// <c>showCalendarPreferences</c>): the calendar page's empty state, the
    /// sidebar's Sync a Microsoft 365 Account's Calendars….
    /// </summary>
    public void ShowCalendarPreferences() => PreferencesWindow.Show(this)?.ShowCalendar();

    // After the board: the samples' flag, the alerts and the notifications'
    // routing (AppCalendar.init).
    private void InitializeCalendar()
    {
        calendarLogger = Logs.CreateLogger("Malachi.App.Calendar");
        CalendarSamples = InMemoryCalendarSource.SamplesRequested;
        calendarAlertsSource = NewCalendarSource();
        calendarNotifier = new CalendarAlertNotifier(this, new NewMailSound(calendarLogger));
        var settings = Settings;
        CalendarAlerts = new CalendarAlertsController(
            calendarAlertsSource,
            calendarNotifier,
            CalendarEnv(),
            TimeProvider.System,
            enabled: () => settings.DesktopNotifications,
            logger: Logs.CreateLogger<CalendarAlertsController>());
        // A refused snooze or dismissal: a log line, not a toast (the
        // controller logs the method and the error class itself).
        CalendarAlerts.Failed += (_, _) => LogCalendarAlertFailed(calendarLogger);
        calendarTokens.Add(Notifications.AddCalendar(HandleCalendar));
        calendarTokens.Add(Notifications.AddAccountsChanged(() =>
        {
            foreach (var s in DaemonCalendarSources())
            {
                s.AccountsChanged();
            }
        }));
        calendarTokens.Add(Notifications.AddConnectionState(state =>
        {
            var connected = state is ConnectionState.Connected or ConnectionState.InfoFailed;
            foreach (var s in DaemonCalendarSources())
            {
                s.ConnectionChanged(connected);
            }
        }));
    }

    // Quit's Release: the routing goes, then every source and the alerts.
    private void CloseCalendar()
    {
        foreach (var t in calendarTokens)
        {
            t.Dispose();
        }
        calendarTokens.Clear();
        foreach (var entry in calendarSources.Values.ToArray())
        {
            CloseSource(entry.Source);
        }
        calendarSources.Clear();
        CalendarAlerts?.Dispose();
        if (calendarAlertsSource is { } own)
        {
            CloseSource(own);
            calendarAlertsSource = null;
        }
    }

    // A decoded calendar notification (AppCalendar.handle): every window's
    // source, then the alerts once for the application.
    private void HandleCalendar(DaemonNotification n)
    {
        foreach (var entry in calendarSources.Values.ToArray())
        {
            entry.Source.Handle(n);
        }
        CalendarAlerts.Handle(n);
    }

    private DaemonCalendarSource[] DaemonCalendarSources() =>
        calendarSources.Values.Select(e => e.Source).OfType<DaemonCalendarSource>().ToArray();

    private CalendarSourceEntry EntryFor(Window window)
    {
        if (calendarSources.TryGetValue(window, out var entry))
        {
            return entry;
        }
        entry = new CalendarSourceEntry(NewCalendarSource());
        calendarSources[window] = entry;
        window.Closed += (_, _) => ForgetCalendarSource(window);
        return entry;
    }

    // The window closed: its source stops for good.
    private void ForgetCalendarSource(Window window)
    {
        if (calendarSources.Remove(window, out var entry))
        {
            CloseSource(entry.Source);
        }
    }

    private ICalendarDataSource NewCalendarSource() => CalendarSamples
        ? InMemoryCalendarSource.Samples(TimeProvider.System.GetUtcNow(), TimeZoneInfo.Local)
        : new DaemonCalendarSource(Client, TimeZoneInfo.Local, Logs.CreateLogger<DaemonCalendarSource>());

    private static void CloseSource(ICalendarDataSource source) => (source as IDisposable)?.Dispose();

    [LoggerMessage(Level = LogLevel.Information, Message = "calendar: an alert's snooze or dismissal was refused")]
    private static partial void LogCalendarAlertFailed(ILogger logger);

    // A window's source and whether it was started.
    private sealed class CalendarSourceEntry(ICalendarDataSource source)
    {
        public ICalendarDataSource Source { get; } = source;

        public bool Started { get; set; }
    }
}
