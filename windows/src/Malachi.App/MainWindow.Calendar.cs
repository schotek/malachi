// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MainWindow/MainWindowController+Calendar.swift
// (calendarController, startCalendar, showCalendar, restoreCalendarFocus,
// newEvent, calendarSignIn's hand-off, showEventDetail, the window's title
// in the Calendar) and of the search field of Calendar/CalendarToolbar.swift;
// GTK: the calendar's window code follows (docs/calendar.md §6.4).
//
// The Calendar mode's part of the main window (docs/calendar.md §5.3, D27):
// the page (Calendars/CalendarPage, in place of the panes under the same
// title bar and status line), its entry and exit, and the title bar's
// search box while the calendar shows. The box is one for both modes: the
// mail's text is kept aside while the calendar shows and comes back in
// Mail, the calendar's query (CalendarPage.SearchText) comes back in the
// Calendar, and the placeholder says what the box searches ("Search Mail",
// "Search Events"). The Board hides the box as before. The search in the
// user's own words is the mail's (MainWindow.OwnWords.cs).
//
// The window's CalendarController (CalendarController) is made over the
// window's calendar source (AppState.CalendarSourceFor: the daemon's, or
// the samples with MALACHI_CALENDAR_SAMPLES=1) the first time it is needed,
// the first entry into the Calendar or New Event in Mail, whose defaults
// come from the calendar's preferences; the first entry starts the source
// and the controller (StartCalendar), which run from then on. Its toasts go
// to the window's toast overlay (over the page while the calendar shows),
// Sign In… to the re-sign-in with the calendar scopes
// (Integration.CalendarSignIn), New Event and a block's detail to the
// application's editor and detail (AppState.OpenNewEvent, OpenEventDetail;
// the detail only while the Calendar shows, anchored on the occurrence's
// view, CalendarPage.AnchorFor, or the page). Its Changed is the page's:
// the views redraw from it, and call ApplyCalendarTitle for the caption and
// the page's title (macOS calendarDidChange).
//
// The page shows its empty state until the views come (No Calendars, Open
// Preferences: AppState.ShowCalendarPreferences). The calendar's content
// attaches here: CalendarPage's BarContent (Today, the arrows, the views
// and the title), PageContent (the sidebar and the views), ShowEmpty and
// ShowTitle, the box's query (CalendarSearchChanged,
// CalendarSearchSubmitted, CalendarSearchEnded), New Event
// (Commands.NewEvent, gated by MainWindow.Mode.cs) and the panel of the
// assistant (D28, the page's own slot).

using System;
using System.Globalization;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Microsoft.UI.Xaml;

namespace Malachi.App;

/// <summary>The main window's Calendar mode.</summary>
public sealed partial class MainWindow
{
    // The mail's text of the search box, kept while the calendar shows.
    private string mailSearchText = "";

    // The calendar was shown once (StartCalendar).
    private bool calendarStarted;

    // The window's calendar, made on first use (EnsureCalendarController).
    private CalendarController? calendarController;

    /// <summary>
    /// The window's calendar (macOS <c>calendarController</c>): its view,
    /// range, occurrences, selection, search and sidebar; null until the
    /// first entry into the Calendar or New Event. Its Changed is the
    /// page's to redraw from.
    /// </summary>
    public CalendarController? CalendarController => calendarController;

    // InitializeMode's part: the page's menu and its Open Preferences, New Event.
    private void InitializeCalendar()
    {
        CalendarView.Attach(Commands);
        CalendarView.OpenPreferencesRequested += (_, _) => OpenCalendarPreferences();
        CalendarView.ShowMode(mode);
        // File ▸ New Event, the calendar's "+" and Ctrl+Shift+N, ShortcutMap's
        // key in every mode (macOS newEvent, ⌥⌘N): Mail and the Calendar (the
        // gate of MainWindow.Mode.cs).
        Commands.NewEvent.Handler = NewEvent;
        Closed += (_, _) => calendarController?.Dispose();
    }

    // SetMode's part for the calendar: the page in place of the panes, the
    // box with the calendar's query, the toasts over the page, the keyboard
    // to the page (macOS showCalendar).
    private void EnterCalendar()
    {
        StartCalendar();
        AssistantSplit.Visibility = Visibility.Collapsed;
        BoardView.Visibility = Visibility.Collapsed;
        CalendarView.Visibility = Visibility.Visible;
        // From Mail the box holds the mail's text; from the Board it still
        // does (the Board hides it and leaves it alone).
        mailSearchText = SearchBox.Text;
        SearchBox.PlaceholderText = Cal.Text.SearchEvents;
        SearchBox.Text = CalendarView.SearchText;
        SearchArea.Visibility = Visibility.Visible;
        AppTitleBar.IsPaneToggleButtonVisible = false;
        AppTitleBar.IsBackButtonVisible = false;
        MoveToasts(CalendarView.ToastLayer);
        ApplyCalendarTitle();
        DispatcherQueue.TryEnqueue(() => CalendarView.FocusContent());
    }

    // SetMode's part when the calendar goes: the box gets the mail's text
    // and placeholder back (in Mail it shows them; in the Board it hides).
    private void LeaveCalendar()
    {
        CalendarView.SearchText = SearchBox.Text;
        SearchBox.PlaceholderText = L10n.T("Search Mail");
        SearchBox.Text = mailSearchText;
        mailSearchText = "";
    }

    /// <summary>
    /// Starts the calendar the first time it shows (macOS
    /// <c>startCalendar</c>): the window's source, then its controller. They
    /// keep running from then on (the hub's notifications reach a started
    /// source only). Show in Calendar and an alert's Join start it too.
    /// </summary>
    internal void StartCalendar()
    {
        if (calendarStarted)
        {
            return;
        }
        calendarStarted = true;
        state.StartedCalendarSourceFor(this);
        EnsureCalendarController().Start();
    }

    // The window's calendar, made on first use and not started (macOS
    // calendarController): the toasts, the sign-in and the hand-offs wired.
    private CalendarController EnsureCalendarController()
    {
        if (calendarController is { } existing)
        {
            return existing;
        }
        var c = new CalendarController(
            state.CalendarSourceFor(this),
            state.Settings,
            Shell.AppState.CalendarEnv(),
            TimeProvider.System,
            Cal.LocaleFirstDay(CultureInfo.CurrentCulture));
        calendarController = c;
        c.ToastRequested += (_, text) => Toasts.Show(text);
        c.SignInRequested += (_, account) => state.Integration?.CalendarSignIn(account);
        c.NewEventRequested += (_, defaults) => state.OpenNewEvent(this, defaults);
        c.OpenDetailRequested += (_, o) => ShowEventDetail(o);
        // The page's bar, sidebar and views over it, its Changed
        // (MainWindow.CalendarPage.cs).
        AttachCalendarPage(c);
        return c;
    }

    // New Event (macOS newEvent): the editor with the calendar's defaults
    // for the next half hour; in Mail too, over a controller not started yet.
    private void NewEvent() => state.OpenNewEvent(this, EnsureCalendarController().NewEventDefaults(null, Cal.Origin.Now));

    // The controller asks for an occurrence's detail (macOS showEventDetail):
    // only while the Calendar shows and for an event (an invitation not yet
    // in the calendar has none), over the occurrence's view or the page.
    private void ShowEventDetail(Occurrence o)
    {
        if (mode != Core.Boards.Board.Mode.Calendar || o.EventId is null)
        {
            return;
        }
        var anchor = CalendarView.AnchorFor(Cal.OccurrenceKey(o)) ?? CalendarView;
        state.OpenEventDetail(this, o, anchor);
    }

    // The window's caption and the page's title in the Calendar (macOS
    // applyTitle: the controller's title and subtitle once it exists; the
    // mode's name until then). The caption has no subtitle, as the board's.
    private void ApplyCalendarTitle()
    {
        var title = calendarController?.Title ?? Cal.Text.ModeName;
        var subtitle = calendarController?.Subtitle ?? "";
        CalendarView.ShowTitle(title, subtitle);
        if (mode == Core.Boards.Board.Mode.Calendar)
        {
            Title = new ListHeading(title, "").Caption;
        }
    }

    // The empty state's Open Preferences (macOS showCalendarPreferences).
    private void OpenCalendarPreferences() => state.ShowCalendarPreferences();

    // The box's typing while the calendar shows: the calendar's query (its
    // search runs with the calendar's content), never the mail's search.
    private void CalendarSearchChanged(string text)
    {
        CalendarView.SearchText = text;
        calendarController?.SetSearch(text);
    }

    // Return in the box while the calendar shows.
    private void CalendarSearchSubmitted(string text)
    {
        CalendarView.SearchText = text;
        calendarController?.SetSearch(text);
    }

    // Escape in the box while the calendar shows: the query empties and the
    // page takes the keyboard (Cal.EscapeFor's ClearSearch).
    private void CalendarSearchEnded()
    {
        SearchBox.Text = "";
        CalendarView.SearchText = "";
        calendarController?.ClearSearch();
        CalendarView.FocusContent();
    }

    // Whether the box is the calendar's now.
    private bool SearchIsCalendars => mode == Core.Boards.Board.Mode.Calendar;
}
