// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/EventEditor/EventEditorViewController.swift
// (buildFields' attendee field and RecipientSuggestionsController,
// attendeesEdited, render's availability); GTK: the event editor follows
// (docs/calendar.md §6.4), the rows are ui/internal/calendar/availability.go
// through EventEditorController.
//
// The attendees of the event editor (docs/calendar.md §5.6): the compose
// window's RecipientTokenBox with its suggestions (contact.search for the
// chosen calendar's account, the compose window's popup), unchanged. The
// box owns its RecipientTokens; every change of its value goes to the
// controller as the box resolves it (SetAttendees: what a commit would make
// of the typed text), which counts the attendees, the primary button's
// label and the availability. The availability rows, the summary line, Next
// Free Slot and its line show only when the calendar's account answers
// free/busy and there are attendees.

using System.Diagnostics.CodeAnalysis;
using System.Linq;
using Malachi.App.Compose;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Compose;
using Malachi.Core.Controllers;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Logging;

namespace Malachi.App.Calendars;

/// <summary>The attendees and the availability of the event editor's form.</summary>
[SuppressMessage("Design", "CA1001", Justification = "The suggestions go with the window: Close disposes of them, as the compose pane's.")]
public sealed partial class EventEditorPane
{
    private RecipientSuggestions? suggestions;

    // The box with the event's attendees, its suggestions, and the
    // controller told of every change.
    private void WireAttendees(AppState appState, EventEditorController editor)
    {
        AttendeesBox.Text = editor.Attendees.Text;
        var completion = new SuggestionsController(
            appState.Client,
            () => CurrentAccount ?? new AccountId(""),
            // The typed text after the last badge, and its end.
            () => (AttendeesBox.Pending, Suggest.ScalarOffset(AttendeesBox.Pending.Length, AttendeesBox.Pending)),
            logger: appState.Logs.CreateLogger<SuggestionsController>());
        suggestions = new RecipientSuggestions(AttendeesBox, completion);
        AttendeesBox.Changed += (_, _) => SyncAttendees();
        AttendeesBox.PendingChanged += (_, _) =>
        {
            if (!closed)
            {
                completion.TextChanged();
            }
        };
    }

    // attendeesEdited: the box's badges and typed text into the controller.
    private void SyncAttendees()
    {
        if (closed || controller is not { } c)
        {
            return;
        }
        var r = AttendeesBox.Resolved();
        c.SetAttendees(r.Addresses, r.Invalid);
    }

    // render's availability: the rows over the start's day, the summary,
    // Next Free Slot and its line.
    private void RenderAvailability(EventEditorController c)
    {
        var shows = c.ShowsAvailability;
        AvailabilityLabel.Visibility = Show(shows);
        AvailabilityPanel.Visibility = Show(shows);
        if (shows)
        {
            var day = c.AvailabilityDay;
            Availability.Show(c.AvailabilityRows, day.From, day.To, c.Input.Start, c.Input.End, c.Env);
            var summary = string.Join("\n", c.AvailabilitySummary.Select(CalendarDisplay.Clean));
            SummaryText.Text = summary;
            SummaryText.Visibility = Show(summary.Length > 0);
        }
        FreeSlotText.Text = CalendarDisplay.Clean(c.FreeSlotText);
        NextSlotButton.IsEnabled = !c.Input.AllDay;
    }
}
