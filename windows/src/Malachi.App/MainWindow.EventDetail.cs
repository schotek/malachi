// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the MainWindowController extension of
// macos/Sources/MalachiMail/Calendar/EventDetailPopover.swift
// (askAssistant(about:)), of EventEditorWindowController.swift
// (askAssistantAboutEvent's part in the main window) and of what the page
// reads of EventDetailPopover (isShown, close, for Escape's Cal.escapeFor);
// GTK: the calendar's window code follows (docs/calendar.md §6.4).
//
// The event detail's and the event editor's ways into the main window:
// - the detail flyout tells the page it is open (CalendarPage.DetailOpen,
//   Escape closes it first) and the page's Escape closes it
//   (CalendarPage.CloseDetailRequested);
// - Ask about This Event unfolds the assistant panel in the Calendar page
//   (D28, MainWindow.CalendarPage.cs) with a question naming the event's id
//   (and occurrence) in its field, for the user to finish and send; it
//   exists only while the panel does (the Assistant shown, In App chosen);
// - the editor's Ask the Assistant… only opens the panel: the Calendar's
//   while the window shows the Calendar, else Mail's (macOS the same).

using Malachi.App.Calendars;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Boards;

namespace Malachi.App;

/// <summary>The event detail's and the editor's part of the main window.</summary>
public sealed partial class MainWindow
{
    /// <summary>The event detail flyout opened or closed over the window.</summary>
    internal void EventDetailShown(bool open)
    {
        if (CalendarView.DetailOpen == open)
        {
            return;
        }
        CalendarView.DetailOpen = open;
        if (open)
        {
            CalendarView.CloseDetailRequested += OnCloseDetailRequested;
        }
        else
        {
            CalendarView.CloseDetailRequested -= OnCloseDetailRequested;
        }
    }

    /// <summary>
    /// Ask about This Event (macOS <c>askAssistant(about:)</c>): the assistant
    /// panel with the question naming <paramref name="e"/> in its field.
    /// </summary>
    internal void AskAssistantAboutEvent(CalendarEvent e)
    {
        if (!state.Assistant.PanelShown || e?.EventId is not { } id)
        {
            return;
        }
        OpenAssistantPanel();
        AssistantPanelView.Prefill(Assistant.AskAboutEventPrompt(id.Value, e.RecurrenceId?.Value ?? ""));
    }

    /// <summary>
    /// The assistant panel unfolds where the window shows it: in the
    /// Calendar page while the Calendar shows, else in Mail (the editor's
    /// Ask the Assistant…).
    /// </summary>
    internal void OpenAssistantPanel()
    {
        if (!state.Assistant.PanelShown)
        {
            return;
        }
        if (mode != Board.Mode.Calendar)
        {
            RevealAssistant();
            return;
        }
        state.ShowMainWindow();
        if (!CalendarView.AssistantShown)
        {
            ToggleCalendarAssistant();
        }
    }

    private void OnCloseDetailRequested(object? sender, System.EventArgs e) => EventDetailFlyout.Close();
}
