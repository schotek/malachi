// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MainWindow/MainWindowController+Calendar.swift
// (calendarDidChange, updateCalendarStatus and its timer, the sidebar's
// onAddCalDAV and onSyncMicrosoft, toggleCalendarAssistant,
// returnAssistantToMail, calendarAssistantAllowed) and of the calendar
// part of StatusBarViewController.swift; GTK: the calendar's window code
// follows (docs/calendar.md §6.4).
//
// The main window's side of the calendar page (Calendars/CalendarPage):
//
// - the controller's Changed redraws the page and, while the Calendar
//   shows, the caption (ApplyCalendarTitle) and the status line; the title
//   bar's box takes the controller's query back when the controller
//   cleared it (Escape, the Unanswered row) and the box is not being typed
//   into (macOS update());
// - the status line in the Calendar (macOS setCalendar): the calendars'
//   state ("Calendars synced 2 minutes ago", a sign-in needed) in place of
//   the sync line's text, the unanswered count ahead of the triage's note,
//   refreshed each minute while the Calendar shows; the flyout with the
//   accounts and the Outbox stays the sync line's;
// - Delete on the selection (the controller's RemoveRequested) opens the
//   occurrence's detail, whose Remove asks its question (the detail's
//   flow, EventDetailController.Remove; Windows: macOS has no key for it);
// - the sidebar's Add CalDAV Calendar… (its window over this one) and
//   "Sync a Microsoft 365 account's calendars…" (Preferences → Calendar);
// - Ctrl+F and Ctrl+E in the page give the keyboard to the title bar's box;
// - the assistant panel (D28): ✦ in the calendar's bar (shown while the
//   panel exists: the Assistant shown, In App chosen) moves the window's
//   one panel out of the mail's split view into the page's slot and
//   unfolds it with the keyboard in its question field; shown there, ✦
//   folds it. Whenever the window leaves the Calendar the panel goes back
//   to the mail's split view, where it was. Requests that reveal the panel
//   from elsewhere go to Mail as before (Board.ModeFor).

using System;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App;

/// <summary>The main window's side of the calendar page.</summary>
public sealed partial class MainWindow
{
    // Refreshes the status line's "synced … ago" while the Calendar shows.
    private DispatcherQueueTimer? calendarStatusTimer;

    // Where the assistant panel lives in Mail (the pane's grid and its
    // place there), while the calendar's page has it.
    private Panel? mailAssistantParent;
    private int mailAssistantIndex;

    // The window's controller exists (EnsureCalendarController): the page
    // follows it, and the window the page.
    private void AttachCalendarPage(CalendarController c)
    {
        CalendarView.AttachCalendar(c);
        c.Changed += (_, _) => CalendarChanged();
        c.RemoveRequested += (_, o) => ShowEventDetail(o);
        CalendarView.AssistantToggleRequested += (_, _) => ToggleCalendarAssistant();
        CalendarView.AddCalDAVRequested += (_, _) => state.ShowAddCalDAVCalendar(this, null);
        CalendarView.SyncMicrosoftRequested += (_, _) => OpenCalendarPreferences();
        CalendarView.SearchFocusRequested += (_, _) => SearchBox.Focus(FocusState.Keyboard);
        // The status line and the panel follow the mode: the page's
        // visibility is the Calendar's.
        CalendarView.RegisterPropertyChangedCallback(UIElement.VisibilityProperty, (_, _) => CalendarShownChanged());
        if (state.Assistant is { } assistant)
        {
            assistant.Changed += (_, _) => CalendarView.SetAssistantAvailable(assistant.PanelShown);
            CalendarView.SetAssistantAvailable(assistant.PanelShown);
        }
        Closed += (_, _) => calendarStatusTimer?.Stop();
    }

    // After every change of the controller (macOS calendarDidChange).
    private void CalendarChanged()
    {
        CalendarView.Update();
        if (mode != Board.Mode.Calendar || calendarController is not { } c)
        {
            return;
        }
        // The controller cleared its query (Escape, the Unanswered row):
        // the box follows unless it is being typed into.
        if (c.SearchText.Length == 0 && SearchBox.Text.Length > 0 && SearchBox.FocusState == FocusState.Unfocused)
        {
            SearchBox.Text = "";
        }
        CalendarView.SearchText = c.SearchText;
        ApplyCalendarTitle();
        UpdateCalendarStatus();
    }

    // The page came or went (SetMode): the status line's texts, and the
    // panel back to the mail's split view when the Calendar goes.
    private void CalendarShownChanged()
    {
        if (CalendarView.Visibility != Visibility.Visible)
        {
            ReturnAssistantToMail();
        }
        UpdateCalendarStatus();
    }

    // The status line's texts for the mode (macOS updateCalendarStatus):
    // in the Calendar the calendars' state and the unanswered count, the
    // sync line as always otherwise.
    private void UpdateCalendarStatus()
    {
        if (mode != Board.Mode.Calendar || calendarController is not { } c)
        {
            calendarStatusTimer?.Stop();
            StatusLine.SetCalendar(null, "");
            return;
        }
        StatusLine.SetCalendar(c.StatusLine, c.UnansweredLine);
        if (calendarStatusTimer is null)
        {
            var timer = DispatcherQueue.CreateTimer();
            timer.Interval = TimeSpan.FromSeconds(60);
            timer.Tick += (_, _) => UpdateCalendarStatus();
            calendarStatusTimer = timer;
        }
        if (!calendarStatusTimer.IsRunning)
        {
            calendarStatusTimer.Start();
        }
    }

    // ✦ in the calendar's bar (macOS toggleCalendarAssistant): the panel
    // moves into the page and unfolds with the keyboard in its question
    // field; shown there, it folds.
    private void ToggleCalendarAssistant()
    {
        if (mode != Board.Mode.Calendar || !state.Assistant.PanelShown)
        {
            return;
        }
        if (CalendarView.AssistantShown)
        {
            CalendarView.HideAssistant();
            return;
        }
        if (mailAssistantParent is null && AssistantPanelView.Parent is Panel parent)
        {
            mailAssistantParent = parent;
            mailAssistantIndex = parent.Children.IndexOf(AssistantPanelView);
            parent.Children.Remove(AssistantPanelView);
        }
        state.Assistant.Refresh();
        CalendarView.ShowAssistant(AssistantPanelView);
        // After the panel has unfolded.
        DispatcherQueue.TryEnqueue(DispatcherQueuePriority.Low, AssistantPanelView.FocusInput);
    }

    // The panel goes back to the mail's split view (the window leaves the
    // Calendar; macOS returnAssistantToMail).
    private void ReturnAssistantToMail()
    {
        var panel = CalendarView.ReleaseAssistant();
        if (mailAssistantParent is not { } parent)
        {
            return;
        }
        if (panel is not null && !parent.Children.Contains(panel))
        {
            parent.Children.Insert(Math.Clamp(mailAssistantIndex, 0, parent.Children.Count), panel);
        }
        mailAssistantParent = null;
    }
}
