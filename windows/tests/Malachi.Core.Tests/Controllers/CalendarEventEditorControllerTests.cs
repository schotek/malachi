// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarEventEditorControllerTests.swift
// (every test); GTK: none yet. The event editor's controller
// (EventEditorController) over the in-memory samples: the defaults of a new
// event, the calendars, the validation, the attendees through
// RecipientTokens, the primary label and the footer, the availability's
// debounce and its account rule, Next Free Slot, Attach, the save paths
// with their params and the close question. Swift's now closure is a
// FakeTimeProvider at calNow, its manual clock ManualSleep, its
// calWaitUntil polls IdleAsync. Added: the Windows-only members moved out
// of the macOS views (ShowsReloadEvent, ReloadEventAsync, Start's
// findFreeSlot, SetAttendees), and the complex rule that Save keeps unless
// the user replaced it (Swift's eventInput, untested there).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;
using Mode = Malachi.Core.Controllers.EventEditorController.Mode;
using X = Malachi.Core.Tests.Controllers.EventControllersFixture;

namespace Malachi.Core.Tests.Controllers;

public sealed class CalendarEventEditorControllerTests
{
    private static readonly TimeZoneInfo NewYork = TimeZoneInfo.FindSystemTimeZoneById("America/New_York");

    private static Task<EventEditorController> EditorAsync(
        CalUi h, InMemoryCalendarSource? src = null, Mode? mode = null, CalendarId? calendarId = null, ManualSleep? clock = null,
        bool findFreeSlot = false)
    {
        src ??= X.Samples();
        return h.Run(() =>
        {
            var d = Cal.NewEventDefaults(X.Now, Cal.Origin.Now, src.Snapshot.Preferences, X.Zone);
            var e = new EventEditorController(src, mode, d, calendarId, X.Env, new FakeTimeProvider(X.Now), clock?.Sleep, h.Pending);
            e.Start(findFreeSlot);
            return e;
        });
    }

    private static CalendarEvent Event(InMemoryCalendarSource src, string id) =>
        src.Data.Events.First(e => e.EventId == new CalendarEventId(id));

    private static T LastWrite<T>(InMemoryCalendarSource src)
        where T : InMemoryCalendarSource.Write =>
        Assert.IsType<T>(src.Writes[^1]);

    [Fact]
    public async Task NewEventDefaults()
    {
        using var h = new CalUi();
        var e = await EditorAsync(h);
        Assert.Equal(Cal.Text.NewEvent, e.WindowTitle);
        Assert.Equal(Cal.Date(2026, 10, 14, 10, 30, X.Zone), e.Input.Start);
        Assert.Equal(Cal.Date(2026, 10, 14, 11, 30, X.Zone), e.Input.End);
        Assert.True(e.Alert == 15 && e.Input.Alerts!.SequenceEqual([15]));
        Assert.Equal(Cal.SampleWorkCalendar, e.Input.CalendarId);
        // Writable calendars only: no birthdays or holidays.
        Assert.Equal([Cal.SampleWorkCalendar, Cal.SamplePersonal, Cal.SampleFamily, Cal.SampleSport], e.Calendars.Select(c => c.Id));
        Assert.Equal("Work · Calendar", e.Calendars[0].Title);
        Assert.True(e.PrimaryLabel == "Save" && e.Footer.Count == 0 && !e.Dirty);
        Assert.True(e.SaveKeyAllowed);
    }

    [Fact]
    public async Task AllDayRemapsTheAlert()
    {
        using var h = new CalUi();
        var e = await EditorAsync(h);
        Assert.True(e.Alert == 15 && e.AlertChoices.SequenceEqual(Cal.AlertChoices));
        await h.Run(() => e.SetAllDay(true));
        // 15 minutes → 1 day before (9:00), the day-based choices.
        Assert.True(e.Alert == Cal.AllDayAlertDefault && e.Input.Alerts!.SequenceEqual([Cal.AllDayAlertDefault]));
        Assert.Equal(Cal.AllDayAlertChoices, e.AlertChoices);
        Assert.Equal(Cal.AlertTitleFor(Cal.AllDayAlertDefault, allDay: true), e.AlertTitle(e.Alert));
        // Off: back to the preference's default.
        await h.Run(() => e.SetAllDay(false));
        Assert.True(e.Alert == 15 && e.Input.Alerts!.SequenceEqual([15]));
        Assert.Equal(Cal.AlertChoices, e.AlertChoices);
        // None stays None both ways.
        await h.Run(() =>
        {
            e.SetAlert(-1);
            e.SetAllDay(true);
        });
        Assert.True(e.Alert == -1 && e.Input.Alerts!.Count == 0);
        await h.Run(() => e.SetAllDay(false));
        Assert.True(e.Alert == -1 && e.Input.Alerts!.Count == 0);
    }

    [Fact]
    public async Task ValidationBlocksSave()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var e = await EditorAsync(h, src);
        await h.Run(() => e.SetEnd(e.Input.Start - TimeSpan.FromSeconds(60)));
        Assert.True(e.Dirty);
        Assert.False(await h.Invoke(e.SaveAsync));
        Assert.Equal([Cal.Problem.EndBeforeStart], e.Problems);
        Assert.Single(e.ProblemTexts);
        Assert.Empty(src.Writes);
        // A change clears the problems.
        await h.Run(() => e.SetTitle("Lunch"));
        Assert.Empty(e.Problems);
    }

    [Fact]
    public async Task StartMovesTheEnd()
    {
        using var h = new CalUi();
        var e = await EditorAsync(h);
        await h.Run(() => e.SetStart(Cal.Date(2026, 10, 15, 14, 0, X.Zone)));
        Assert.Equal(Cal.Date(2026, 10, 15, 15, 0, X.Zone), e.Input.End);
        await h.Run(() => e.SetAllDay(true));
        Assert.Equal(Cal.Date(2026, 10, 15, 0, 0, X.Zone), e.Input.Start);
        Assert.Equal(Cal.Date(2026, 10, 16, 0, 0, X.Zone), e.Input.End);
        await h.Run(() => e.SetZone(NewYork));
        Assert.Equal("Time zone: America/New_York", e.TimeZoneLink);
    }

    [Fact]
    public async Task AttendeesLabelFooterAndInvalid()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var e = await EditorAsync(h, src);
        await h.Run(() => e.EditAttendees(t => t.Paste("Jana <jana@example.org>, nonsense")));
        Assert.Equal(["jana@example.org"], e.Input.Attendees.Select(a => a.Address));
        Assert.Equal("Save and Send Invitations", e.PrimaryLabel);
        Assert.Equal(["The server sends the invitations."], e.Footer);
        Assert.False(e.SaveKeyAllowed);
        Assert.False(await h.Invoke(e.SaveAsync));
        Assert.Contains(Cal.Problem.InvalidAttendee, e.Problems);
        // The CalDAV calendar sends by mail through the Gmail account.
        await h.Run(() => e.SetCalendar(Cal.SampleSport));
        Assert.Equal(["Invitations leave by e-mail from account Gmail (me@example.com) through the Outbox."], e.Footer);
    }

    [Fact]
    public async Task AvailabilityIsDebouncedAndOnlyWithFreeBusy()
    {
        using var h = new CalUi();
        var clock = new ManualSleep();
        var src = X.Samples();
        var e = await EditorAsync(h, src, clock: clock);
        Assert.True(!e.ShowsAvailability && !e.AvailabilityPending);
        await h.Run(() =>
        {
            e.EditAttendees(t => t.Paste("jana@example.org, petr@example.org,"));
            e.EditAttendees(t => t.Paste("eva@example.org,"));
        });
        Assert.True(e.ShowsAvailability);
        await h.IdleAsync();
        Assert.Equal(2, clock.Pending.Count);
        Assert.All(clock.Pending, d => Assert.Equal(TimeSpan.FromMilliseconds(500), d));
        await h.Run(clock.Fire);
        await h.IdleAsync();
        Assert.False(e.AvailabilityPending);
        Assert.Single(src.Reads, r => r == API.CalendarFreeBusy.Name);
        Assert.Equal(["You", "jana@example.org", "petr@example.org", "eva@example.org"], e.AvailabilityRows.Select(r => r.Label));
        Assert.All(e.AvailabilityRows.Skip(1), r => Assert.False(r.Known));
        Assert.NotEmpty(e.AvailabilitySummary);
        // Gmail's account cannot answer for others: no request.
        await h.Run(() => e.SetCalendar(Cal.SamplePersonal));
        Assert.True(!e.ShowsAvailability && !e.AvailabilityPending);
        Assert.Empty(e.AvailabilityRows);
    }

    [Fact]
    public async Task NextFreeSlotMovesTheEvent()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var slot = new TimeSlot { Start = Cal.Date(2026, 10, 15, 13, 0, X.Zone), End = Cal.Date(2026, 10, 15, 14, 0, X.Zone) };
        src.FindFreeTimeResult = new CalendarFindFreeTimeResult { Slots = [slot] };
        var e = await EditorAsync(h, src);
        await h.Run(e.NextFreeSlot);
        await h.IdleAsync();
        Assert.True(e.Input.Start == slot.Start && e.Input.End == slot.End);
        Assert.StartsWith("Next free slot: ", e.FreeSlotText, StringComparison.Ordinal);
        src.FindFreeTimeResult = new CalendarFindFreeTimeResult();
        var none = await EditorAsync(h, src);
        await h.Run(none.NextFreeSlot);
        await h.IdleAsync();
        Assert.Equal(Cal.NoFreeSlot, none.FreeSlotText);
    }

    [Fact]
    public async Task SaveCreatesWithInvitations()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var e = await EditorAsync(h, src);
        var saved = new List<CalendarEvent>();
        await h.Run(() =>
        {
            e.Saved += (_, x) => saved.Add(x);
            e.SetTitle("  Roadmap  ");
            e.SetLocation("Room D");
            e.SetRepeat(Cal.Repeat.Weekly);
            e.SetAlert(-1);
            e.EditAttendees(t => t.Paste("jana@example.org,"));
            e.Attach("/tmp/agenda.pdf");
        });
        await h.IdleAsync();
        Assert.NotEmpty(e.Attachments);
        Assert.Contains("Attachment: agenda.pdf", e.Footer);
        Assert.True(await h.Invoke(e.SaveAsync));
        var p = LastWrite<InMemoryCalendarSource.Write.Create>(src).Parameters;
        Assert.True(p.CalendarId == Cal.SampleWorkCalendar && p.SendInvitations == true);
        Assert.True(p.Event.Title == "Roadmap" && p.Event.Location == "Room D" && p.Event.Alerts!.Count == 0);
        Assert.True(p.Event.Start == new LocalDateTime("2026-10-14T10:30:00") && p.Event.TimeZone == new TimeZoneName("Europe/Prague"));
        var r = p.Event.Recurrence!;
        Assert.True(r.Frequency == RecurrenceFrequency.Weekly && r.Interval == 1 && r.ByDay!.SequenceEqual(["WE"]));
        Assert.Equal([e.Attachments[0].Id], p.Event.Attachments!);
        Assert.Equal("Invitation: Roadmap", p.Mail?.Subject);
        Assert.True(saved.Count == 1 && !e.Dirty);
        var a = Assert.IsType<InMemoryCalendarSource.Write.ImportAttachment>(src.Writes[0]).Parameters;
        Assert.True(a.AccountId == Cal.SampleWork && a.Path == "/tmp/agenda.pdf");
    }

    [Fact]
    public async Task EditUpdatesWithTheScope()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var offsite = Event(src, "ev_sample_offsite");
        var e = await EditorAsync(h, src, mode: new Mode.EditEvent(offsite, EditScope.All));
        Assert.Equal(Cal.Text.EditEvent, e.WindowTitle);
        Assert.True(e.Input.AllDay && e.Input.Title == "Team offsite");
        Assert.Equal([Cal.SampleWorkCalendar], e.Calendars.Select(c => c.Id));
        await h.Run(() => e.SetTitle("Team offsite 2026"));
        Assert.True(await h.Invoke(e.SaveAsync));
        var p = LastWrite<InMemoryCalendarSource.Write.Update>(src).Parameters;
        Assert.True(p.Scope == EditScope.All && p.ETag == "\"1\"" && p.RecurrenceId is null && p.Mail is null);
        Assert.True(p.Event.AllDay && p.Event.StartDate == offsite.StartDate && p.Event.EndDate == offsite.EndDate);
        // One occurrence of the standup: no rule in the exception.
        var standup = Event(src, "ev_sample_standup");
        var one = await EditorAsync(h, src, mode: new Mode.EditEvent(standup, EditScope.This));
        Assert.True(one.RepeatChoice is Cal.Repeat.Custom or Cal.Repeat.Weekly);
        await h.Run(() => one.SetTitle("Standup (moved)"));
        Assert.True(await h.Invoke(one.SaveAsync));
        var q = LastWrite<InMemoryCalendarSource.Write.Update>(src).Parameters;
        Assert.True(q.Scope == EditScope.This && q.RecurrenceId == standup.RecurrenceId && q.Event.Recurrence is null);
    }

    [Fact]
    public async Task DeleteRunsTheDetailsFlow()
    {
        using var h = new CalUi();
        // New Event has no Delete.
        Assert.Null((await EditorAsync(h)).DeleteTitle);
        // An invitation: Cancel, Remove Only, Remove and Decline.
        var src = X.Samples();
        var planning = Event(src, "ev_sample_planning");
        var e = await EditorAsync(h, src, mode: new Mode.EditEvent(planning, EditScope.All));
        Assert.Equal(Cal.RemoveTitle(planning), e.DeleteTitle);
        var answer = Cal.Answer.Cancel;
        var asked = new List<Cal.Question>();
        var saveQuestions = 0;
        var closed = 0;
        await h.Run(() =>
        {
            e.AskQuestion = _ =>
            {
                saveQuestions++;
                return Task.FromResult(Cal.Answer.Cancel);
            };
            e.AskDeleteQuestion = q =>
            {
                asked.Add(q);
                return Task.FromResult(answer);
            };
            e.CloseRequested += (_, _) => closed++;
            e.SetTitle("changed");
        });
        Assert.False(await h.Invoke(e.DeleteAsync));
        Assert.Equal("Also tell the organiser you won’t attend?", asked[0].Heading);
        Assert.True(src.Writes.Count == 0 && closed == 0);
        answer = Cal.Answer.Alternative;
        Assert.True(await h.Invoke(e.DeleteAsync));
        var p = LastWrite<InMemoryCalendarSource.Write.Delete>(src).Parameters;
        Assert.True(p.EventId == planning.EventId && p.Scope == EditScope.All && p.Notify == false && p.Mail is null);
        // Closes without the unsaved-changes question.
        Assert.True(closed == 1 && saveQuestions == 0 && !e.Dirty);

        var src2 = X.Samples();
        var d = await EditorAsync(h, src2, mode: new Mode.EditEvent(Event(src2, "ev_sample_planning"), EditScope.All));
        await h.Run(() => d.AskDeleteQuestion = _ => Task.FromResult(Cal.Answer.Confirm));
        Assert.True(await h.Invoke(d.DeleteAsync));
        var q = LastWrite<InMemoryCalendarSource.Write.Delete>(src2).Parameters;
        Assert.True(q.Notify == true && q.Mail?.Subject == "Declined: Q4 planning");

        // The organiser's event: the delete question follows the preference.
        var src3 = X.Samples();
        var offsite = Event(src3, "ev_sample_offsite");
        var o = await EditorAsync(h, src3, mode: new Mode.EditEvent(offsite, EditScope.All));
        var headings = new List<string>();
        await h.Run(() => o.AskDeleteQuestion = q =>
        {
            headings.Add(q.Heading);
            return Task.FromResult(Cal.Answer.Cancel);
        });
        Assert.False(await h.Invoke(o.DeleteAsync));
        Assert.True(headings.SequenceEqual(["Remove “Team offsite”?"]) && src3.Writes.Count == 0);
        await h.Run(() => o.ConfirmDelete = () => false);
        Assert.True(await h.Invoke(o.DeleteAsync));
        var r = LastWrite<InMemoryCalendarSource.Write.Delete>(src3).Parameters;
        Assert.True(r.Notify is null && r.ETag == offsite.ETag && headings.Count == 1);
    }

    [Fact]
    public async Task CloseAsksWithChanges()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var e = await EditorAsync(h, src);
        var closed = 0;
        await h.Run(() => e.CloseRequested += (_, _) => closed++);
        Assert.True(await h.Invoke(e.CloseAsync));
        Assert.Equal(1, closed);
        var answer = Cal.Answer.Cancel;
        var questions = new List<Cal.Question>();
        await h.Run(() =>
        {
            e.SetTitle("Draft");
            e.AskQuestion = q =>
            {
                questions.Add(q);
                return Task.FromResult(answer);
            };
        });
        Assert.False(await h.Invoke(e.CloseAsync));
        Assert.Equal("Save the Changes to This Event?", questions[0].Heading);
        answer = Cal.Answer.Alternative;
        Assert.True(await h.Invoke(e.CloseAsync));
        Assert.True(closed == 2 && src.Writes.Count == 0);
        var s = await EditorAsync(h, src);
        await h.Run(() =>
        {
            s.SetTitle("Kept");
            s.AskQuestion = _ => Task.FromResult(Cal.Answer.Confirm);
        });
        Assert.True(await h.Invoke(s.CloseAsync));
        LastWrite<InMemoryCalendarSource.Write.Create>(src);
    }

    [Fact]
    public async Task RefusedSaveStaysOpenWithTheError()
    {
        using var h = new CalUi();
        var src = X.Samples();
        src.Failures[API.EventCreate.Name] = new RpcException(new RpcError { Code = ErrorCode.ReadOnly, Message = "no" });
        var e = await EditorAsync(h, src);
        await h.Run(() => e.SetTitle("X"));
        Assert.False(await h.Invoke(e.SaveAsync));
        Assert.True(e.Error == Cal.Text.ErrorText(ErrorCode.ReadOnly) && e.Dirty);
        Assert.False(e.ShowsReloadEvent); // not a conflict, and not Edit
    }

    [Fact]
    public async Task VideoProvidersFollowTheCalendar()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var e = await EditorAsync(h, src);
        Assert.Equal(["teamsForBusiness"], e.VideoProviders);
        var teams = new VideoCallInput { Mode = VideoCallMode.Create, Provider = "teamsForBusiness" };
        Assert.True(await h.Run(() => e.SetVideoCall(teams)));
        Assert.True(e.Dirty && e.EventInput.VideoCall == teams);
        Assert.False(await h.Run(() => e.SetVideoCall(new VideoCallInput { Mode = VideoCallMode.Create, Provider = "zoom" })));
        // Gmail's account cannot create meetings: none, and the meeting goes.
        await h.Run(() => e.SetCalendar(Cal.SamplePersonal));
        Assert.True(e.VideoProviders.Count == 0 && e.VideoCall is null);
    }

    [Fact]
    public async Task VideoLinkMustBeHttps()
    {
        using var h = new CalUi();
        var e = await EditorAsync(h);
        Assert.False(await h.Run(() => e.SetVideoCall(new VideoCallInput { Mode = VideoCallMode.Link, Url = "http://meet.example.org/x" })));
        Assert.False(await h.Run(() => e.SetVideoCall(new VideoCallInput { Mode = VideoCallMode.Link, Url = "javascript:alert(1)" })));
        Assert.False(await h.Run(() => e.SetVideoCall(new VideoCallInput { Mode = VideoCallMode.Link, Url = "https://" })));
        Assert.True(e.VideoCall is null && !e.Dirty);
        Assert.True(await h.Run(() => e.SetVideoCall(new VideoCallInput { Mode = VideoCallMode.Link, Url = " https://meet.example.org/abc " })));
        Assert.Equal(new VideoCallInput { Mode = VideoCallMode.Link, Url = "https://meet.example.org/abc" }, e.VideoCall);
        Assert.True(await h.Run(() => e.SetVideoCall(new VideoCallInput { Mode = VideoCallMode.None })));
        Assert.True(e.VideoCall is null && e.EventInput.VideoCall is null);
    }

    [Fact]
    public async Task EditStartsWithTheEventsLink()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var e = await EditorAsync(h, src, mode: new Mode.EditEvent(Event(src, "ev_sample_planning"), EditScope.All));
        Assert.Equal(new VideoCallInput { Mode = VideoCallMode.Link, Url = "https://teams.microsoft.com/l/meetup-join/sample" }, e.VideoCall);
        Assert.True(e.EventInput.VideoCall == e.VideoCall && !e.Dirty);
    }

    // Windows-only: what moved out of the macOS views.

    [Fact]
    public async Task ConflictOffersReloadEvent()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var offsite = Event(src, "ev_sample_offsite");
        src.Failures[API.EventUpdate.Name] = new RpcException(new RpcError { Code = ErrorCode.Conflict, Message = "etag" });
        var e = await EditorAsync(h, src, mode: new Mode.EditEvent(offsite, EditScope.All));
        await h.Run(() => e.SetTitle("Team offsite 2026"));
        Assert.False(await h.Invoke(e.SaveAsync));
        Assert.True(e.ShowsReloadEvent && e.Error == Cal.Text.ErrorText(ErrorCode.Conflict));
        var fresh = await h.Invoke(e.ReloadEventAsync);
        Assert.Equal(offsite.EventId, fresh?.EventId);
        Assert.Equal(API.EventGet.Name, src.Reads[^1]);
        // A change clears the error and the offer.
        await h.Run(() => e.SetTitle("Team offsite 2027"));
        Assert.False(e.ShowsReloadEvent);
        // New Event has nothing to reload.
        Assert.Null(await h.Invoke((await EditorAsync(h, src)).ReloadEventAsync));
    }

    [Fact]
    public async Task FindFreeSlotRunsAsTheWindowOpens()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var slot = new TimeSlot { Start = Cal.Date(2026, 10, 15, 13, 0, X.Zone), End = Cal.Date(2026, 10, 15, 14, 0, X.Zone) };
        src.FindFreeTimeResult = new CalendarFindFreeTimeResult { Slots = [slot] };
        var e = await EditorAsync(h, src, mode: new Mode.EditEvent(Event(src, "ev_sample_planning"), EditScope.All), findFreeSlot: true);
        await h.IdleAsync();
        Assert.True(e.Input.Start == slot.Start && e.Input.End == slot.End && e.Dirty);
        Assert.Single(src.Reads, r => r == API.CalendarFindFreeTime.Name);
    }

    [Fact]
    public async Task AComplexRuleIsKeptUnlessReplaced()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var complex = new Recurrence { Frequency = RecurrenceFrequency.Weekly, Interval = 1, ByDay = ["TU"], Complex = true };
        var series = Event(src, "ev_sample_offsite") with { Recurring = true, Recurrence = complex };
        var e = await EditorAsync(h, src, mode: new Mode.EditEvent(series, EditScope.All));
        Assert.Equal(Cal.Repeat.Custom, e.RepeatChoice);
        await h.Run(() => e.SetTitle("Team offsite 2026"));
        Assert.True(e.EventInput.KeepRecurrence == true && e.EventInput.Recurrence is null);
        // The same rule, a copy of it, is still the event's own.
        await h.Run(() => e.SetRecurrence(complex with { ByDay = ["TU"] }));
        Assert.True(e.EventInput.KeepRecurrence == true);
        // A rule the user chose replaces it.
        await h.Run(() => e.SetRepeat(Cal.Repeat.Weekly));
        Assert.True(e.EventInput.KeepRecurrence is null && e.EventInput.Recurrence?.Frequency == RecurrenceFrequency.Weekly);
        // One occurrence never carries a rule.
        var one = await EditorAsync(h, src, mode: new Mode.EditEvent(series, EditScope.This));
        Assert.True(one.EventInput.Recurrence is null && one.EventInput.KeepRecurrence is null);
    }

    [Fact]
    public async Task SetAttendeesTakesTheFieldsResolvedValue()
    {
        using var h = new CalUi();
        var e = await EditorAsync(h);
        await h.Run(() => e.SetAttendees([new Address { Name = "Jana", Email = "jana@example.org" }], ["nonsense"]));
        Assert.Equal(["jana@example.org"], e.Input.Attendees.Select(a => a.Address));
        Assert.Equal("Jana", e.Input.Attendees[0].Name);
        Assert.True(e.Attendees.HasInvalid && e.Dirty);
        // The same value again is no change.
        var changes = 0;
        await h.Run(() =>
        {
            e.Changed += (_, _) => changes++;
            e.SetAttendees([new Address { Name = "Jana", Email = "jana@example.org" }], ["nonsense"]);
        });
        Assert.Equal(0, changes);
    }
}
