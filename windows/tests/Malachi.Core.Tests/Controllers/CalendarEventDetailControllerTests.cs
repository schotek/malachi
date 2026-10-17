// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarEventDetailControllerTests.swift
// (every test); GTK: none yet. The event detail popover's controller
// (EventDetailController) over the in-memory samples: its rows, the answer
// with and without the series question and Decline Without Reply, the
// remove questions of an attendee and an organiser and the confirm-delete
// preference, Edit's scope, the conflict reload and the hand-offs. Swift's
// calWaitUntil polls are IdleAsync. Added: Find Another Time as the macOS
// popover runs it (Edit with the series question and FindFreeSlot).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Text.Json;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Transport;
using Xunit;
using X = Malachi.Core.Tests.Controllers.EventControllersFixture;

namespace Malachi.Core.Tests.Controllers;

public sealed class CalendarEventDetailControllerTests
{
    private static async Task<EventDetailController> DetailAsync(
        CalUi h, InMemoryCalendarSource src, string id, DateOrTime? recurrenceId = null, bool confirmDelete = true)
    {
        var d = await h.Run(() =>
        {
            var c = new EventDetailController(
                src, new CalendarEventId(id), recurrenceId, env: X.Env, confirmDelete: () => confirmDelete, pending: h.Pending);
            c.Start();
            return c;
        });
        await h.IdleAsync();
        Assert.True(d.Event is not null || d.Error.Length > 0);
        return d;
    }

    // The samples with the design review repeating weekly.
    private static InMemoryCalendarSource WithRecurringReview()
    {
        var data = Cal.SampleData(X.Now, X.Zone);
        var events = data.Events.ToList();
        var i = events.FindIndex(e => e.EventId == new CalendarEventId("ev_sample_review"));
        if (i >= 0)
        {
            events[i] = events[i] with
            {
                Recurring = true,
                Recurrence = new Recurrence { Frequency = RecurrenceFrequency.Weekly, Interval = 1, ByDay = ["TU"] },
                RecurrenceId = new DateOrTime(Rfc3339.Format(events[i].Start!.Value)),
            };
        }
        return new InMemoryCalendarSource(data with { Events = events }) { EmulatesWrites = true };
    }

    private static T LastWrite<T>(InMemoryCalendarSource src)
        where T : InMemoryCalendarSource.Write =>
        Assert.IsType<T>(src.Writes[^1]);

    private static DateOrTime? StandupRecurrenceId(InMemoryCalendarSource src) =>
        src.Data.Events.First(e => e.EventId == new CalendarEventId("ev_sample_standup")).RecurrenceId;

    [Fact]
    public async Task RowsOfAnInvitedEvent()
    {
        using var h = new CalUi();
        var d = await DetailAsync(h, X.Samples(), "ev_sample_planning");
        var r = d.Detail!;
        Assert.Equal("Work · Calendar", r.CalendarLine);
        Assert.Equal("Q4 planning", r.Title);
        Assert.True(r.CanRespond && !r.CanEdit && r.ShowInvitation);
        Assert.Equal(Cal.RemoveFromCalendar, r.RemoveTitle);
        Assert.Equal("teams.microsoft.com", r.VideoHost);
        Assert.True(d.ShownAttendees.Count == 4 && d.ShowAllAttendees.Length == 0);
        var links = new List<string>();
        var shown = new List<MessageRef>();
        await h.Run(() =>
        {
            d.OpenLinkRequested += (_, url) => links.Add(url);
            d.ShowInvitationRequested += (_, m) => shown.Add(m);
            d.JoinVideoCall();
            d.OpenLink("https://example.org/q4-slides");
            d.OpenLink("https://evil.example/");
            d.ShowInvitation();
        });
        Assert.Equal(["https://teams.microsoft.com/l/meetup-join/sample", "https://example.org/q4-slides"], links);
        Assert.Equal([new MessageRef { AccountId = Cal.SampleWork, MessageId = new MessageId("msg_sample_planning") }], shown);
    }

    [Fact]
    public async Task AnswerOfASingleEvent()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var d = await DetailAsync(h, src, "ev_sample_planning");
        var asked = 0;
        await h.Run(() =>
        {
            d.AskScope = _ =>
            {
                asked++;
                return Task.FromResult<EditScope?>(EditScope.This);
            };
            d.Respond(EventResponse.Tentative);
        });
        await h.IdleAsync();
        Assert.Equal(0, asked);
        var p = LastWrite<InMemoryCalendarSource.Write.Respond>(src).Parameters;
        Assert.True(p.Scope == EditScope.All && p.Response == EventResponse.Tentative && p.ETag == "\"1\"" && p.SendResponse is null);
        Assert.Equal("Tentative: Q4 planning", p.Mail?.Subject);
        Assert.Equal(EventResponse.Tentative, d.Event?.MyResponse);
        // Decline Without Reply.
        await h.Run(() => d.Respond(EventResponse.Declined, withoutReply: true));
        await h.IdleAsync();
        var q = LastWrite<InMemoryCalendarSource.Write.Respond>(src).Parameters;
        Assert.True(q.SendResponse == false && q.Mail is null && q.Response == EventResponse.Declined);
    }

    [Fact]
    public async Task AnswerOfASeriesAsksTheScope()
    {
        using var h = new CalUi();
        var src = WithRecurringReview();
        var d = await DetailAsync(h, src, "ev_sample_review");
        var questions = new List<Cal.ScopeQuestion>();
        await h.Run(() =>
        {
            d.AskScope = q =>
            {
                questions.Add(q);
                return Task.FromResult<EditScope?>(null);
            };
            d.Respond(EventResponse.Accepted);
        });
        await h.IdleAsync();
        Assert.Equal([EditScope.This, EditScope.All], questions[0].Choices.Select(c => c.Scope));
        Assert.Empty(src.Writes);
        await h.Run(() =>
        {
            d.AskScope = _ => Task.FromResult<EditScope?>(EditScope.All);
            d.Respond(EventResponse.Accepted);
        });
        await h.IdleAsync();
        Assert.Equal(EditScope.All, LastWrite<InMemoryCalendarSource.Write.Respond>(src).Parameters.Scope);
    }

    [Fact]
    public async Task AttendeeRemoveAsksAboutDeclining()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var d = await DetailAsync(h, src, "ev_sample_planning");
        var questions = new List<Cal.Question>();
        var answer = Cal.Answer.Cancel;
        var closed = 0;
        await h.Run(() =>
        {
            d.AskQuestion = q =>
            {
                questions.Add(q);
                return Task.FromResult(answer);
            };
            d.CloseRequested += (_, _) => closed++;
            d.Remove();
        });
        await h.IdleAsync();
        Assert.Equal("Also tell the organiser you won’t attend?", questions[0].Heading);
        Assert.True(src.Writes.Count == 0 && closed == 0);
        answer = Cal.Answer.Alternative;
        await h.Run(d.Remove);
        await h.IdleAsync();
        var p = LastWrite<InMemoryCalendarSource.Write.Delete>(src).Parameters;
        Assert.True(p.Notify == false && p.Mail is null && p.Scope == EditScope.All);
        Assert.Equal(1, closed);
    }

    [Fact]
    public async Task AttendeeRemoveAndDecline()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var d = await DetailAsync(h, src, "ev_sample_planning");
        await h.Run(() =>
        {
            d.AskQuestion = _ => Task.FromResult(Cal.Answer.Confirm);
            d.Remove();
        });
        await h.IdleAsync();
        var p = LastWrite<InMemoryCalendarSource.Write.Delete>(src).Parameters;
        Assert.True(p.Notify == true && p.Mail?.Subject == "Declined: Q4 planning");
    }

    [Fact]
    public async Task OrganiserDeleteFollowsConfirmDelete()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var questions = new List<Cal.Question>();
        var d = await DetailAsync(h, src, "ev_sample_offsite");
        await h.Run(() =>
        {
            d.AskQuestion = q =>
            {
                questions.Add(q);
                return Task.FromResult(Cal.Answer.Confirm);
            };
            d.Remove();
        });
        await h.IdleAsync();
        Assert.Equal(["Remove “Team offsite”?"], questions.Select(q => q.Heading));
        Assert.Null(LastWrite<InMemoryCalendarSource.Write.Delete>(src).Parameters.Notify);
        // Without the preference nothing is asked.
        var src2 = X.Samples();
        var quiet = await DetailAsync(h, src2, "ev_sample_offsite", confirmDelete: false);
        var asked = 0;
        await h.Run(() =>
        {
            quiet.AskQuestion = _ =>
            {
                asked++;
                return Task.FromResult(Cal.Answer.Cancel);
            };
            quiet.Remove();
        });
        await h.IdleAsync();
        Assert.True(asked == 0 && src2.Writes.Count == 1);
    }

    [Fact]
    public async Task SeriesDeleteAsksTheScope()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var rid = StandupRecurrenceId(src);
        var d = await DetailAsync(h, src, "ev_sample_standup", recurrenceId: rid, confirmDelete: false);
        var scopes = new List<Cal.ScopeQuestion>();
        await h.Run(() =>
        {
            d.AskScope = q =>
            {
                scopes.Add(q);
                return Task.FromResult<EditScope?>(EditScope.This);
            };
            d.Remove();
        });
        await h.IdleAsync();
        Assert.Equal([EditScope.This, EditScope.ThisAndFuture, EditScope.All], scopes[0].Choices.Select(c => c.Scope));
        var p = LastWrite<InMemoryCalendarSource.Write.Delete>(src).Parameters;
        Assert.True(p.Scope == EditScope.This && p.RecurrenceId == rid);
    }

    [Fact]
    public async Task EditHandsTheScopeOver()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var rid = StandupRecurrenceId(src);
        var d = await DetailAsync(h, src, "ev_sample_standup", recurrenceId: rid);
        var edits = new List<EditScope>();
        await h.Run(() =>
        {
            d.AskScope = _ => Task.FromResult<EditScope?>(EditScope.ThisAndFuture);
            d.EditRequested += (_, x) => edits.Add(x.Scope);
            d.Edit();
        });
        await h.IdleAsync();
        Assert.Equal([EditScope.ThisAndFuture], edits);
        // A single event goes to the editor without a question.
        var single = await DetailAsync(h, src, "ev_sample_offsite");
        var asked = 0;
        await h.Run(() =>
        {
            single.AskScope = _ =>
            {
                asked++;
                return Task.FromResult<EditScope?>(null);
            };
            single.EditRequested += (_, x) => edits.Add(x.Scope);
            single.Edit();
        });
        await h.IdleAsync();
        Assert.Equal(2, edits.Count);
        Assert.True(edits[^1] == EditScope.All && asked == 0);
    }

    [Fact]
    public async Task FindAnotherTimeEditsWithTheFreeSlot()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var rid = StandupRecurrenceId(src);
        var d = await DetailAsync(h, src, "ev_sample_standup", recurrenceId: rid);
        var edits = new List<(EditScope Scope, bool FindFreeSlot)>();
        var asked = 0;
        await h.Run(() =>
        {
            d.AskScope = _ =>
            {
                asked++;
                return Task.FromResult<EditScope?>(EditScope.This);
            };
            d.EditRequested += (_, x) => edits.Add((x.Scope, x.FindFreeSlot));
            d.FindAnotherTime();
        });
        await h.IdleAsync();
        Assert.Equal(1, asked); // the series question first
        Assert.Equal([(EditScope.This, true)], edits);
        await h.Run(d.Edit);
        await h.IdleAsync();
        Assert.Equal((EditScope.This, false), edits[^1]);
        // An event the user cannot change offers neither.
        var planning = await DetailAsync(h, src, "ev_sample_planning");
        await h.Run(() =>
        {
            planning.EditRequested += (_, x) => edits.Add((x.Scope, x.FindFreeSlot));
            planning.FindAnotherTime();
        });
        await h.IdleAsync();
        Assert.Equal(2, edits.Count);
    }

    [Fact]
    public async Task ConflictReloadsTheEvent()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var newer = src.Data.Events.First(e => e.EventId == new CalendarEventId("ev_sample_planning")) with
        {
            ETag = "\"9\"",
            Title = "Q4 planning (moved)",
        };
        var data = JsonSerializer.SerializeToElement(new ConflictEventData { Event = newer }, ApiJsonContext.Wire.ConflictEventData);
        src.Failures[API.EventRespond.Name] = new RpcException(new RpcError { Code = ErrorCode.Conflict, Message = "etag", Data = data });
        var d = await DetailAsync(h, src, "ev_sample_planning");
        await h.Run(() => d.Respond(EventResponse.Accepted));
        await h.IdleAsync();
        Assert.Equal(Cal.Text.ErrorText(ErrorCode.Conflict), d.Error);
        Assert.True(d.Event?.ETag == "\"9\"" && d.Detail?.Title == "Q4 planning (moved)");
    }

    [Fact]
    public async Task UnknownEventSaysSo()
    {
        using var h = new CalUi();
        var d = await DetailAsync(h, X.Samples(), "ev_nothing");
        Assert.True(d.Event is null && d.Error == Cal.Text.ErrorText(ErrorCode.EventNotFound));
        var invitation = Cal.SampleData(X.Now, X.Zone).Occurrences[^1];
        Assert.Null(await h.Run(() => EventDetailController.ForOccurrence(X.Samples(), invitation, X.Env, pending: h.Pending)));
    }
}
