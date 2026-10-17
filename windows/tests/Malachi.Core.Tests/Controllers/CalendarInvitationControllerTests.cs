// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarInvitationControllerTests.swift
// (every test); GTK: none yet. The invitation card's controller
// (InvitationController) over the sample cards: the card mode per state and
// switch, the lines, the target calendar, the answer with the target,
// Decline Without Reply, Propose New Time, a refusal inline, the
// cancellation's Remove from Calendar and Show in Calendar. Added: Replace
// compares the cards by content (Swift's ==), and an answer sent as the
// card goes away still reaches the source.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Transport;
using Xunit;
using X = Malachi.Core.Tests.Controllers.EventControllersFixture;

namespace Malachi.Core.Tests.Controllers;

public sealed class CalendarInvitationControllerTests
{
    private static readonly MessageId Demo = new("msg_sample_demo");

    private static InvitationCard Card(InvitationState state)
    {
        var states = Cal.InvitationStates.ToList();
        return Cal.SampleData(X.Now, X.Zone).Cards[states.IndexOf(state)];
    }

    private static Task<InvitationController> InvitationAsync(CalUi h, InMemoryCalendarSource src, InvitationState state, bool cardOn = true) =>
        h.Run(() => new InvitationController(src, Cal.SampleGmail, Demo, Card(state), cardOn, X.Env, h.Pending));

    private static T LastWrite<T>(InMemoryCalendarSource src)
        where T : InMemoryCalendarSource.Write =>
        Assert.IsType<T>(src.Writes[^1]);

    [Fact]
    public async Task ModesPerState()
    {
        using var h = new CalUi();
        var src = X.Samples();
        Assert.Equal(Cal.CardMode.Full, (await InvitationAsync(h, src, InvitationState.Unanswered)).Mode);
        Assert.Equal(Cal.CardMode.Full, (await InvitationAsync(h, src, InvitationState.Answered)).Mode);
        Assert.Equal(Cal.CardMode.Cancelled, (await InvitationAsync(h, src, InvitationState.Cancelled)).Mode);
        Assert.Equal(Cal.CardMode.Reply, (await InvitationAsync(h, src, InvitationState.Processed)).Mode);
        Assert.Equal(Cal.CardMode.None, (await InvitationAsync(h, src, InvitationState.Invalid)).Mode);
        Assert.Equal(Cal.CardMode.None, (await InvitationAsync(h, src, InvitationState.Unanswered, cardOn: false)).Mode);
    }

    [Fact]
    public async Task LinesOfAnUnansweredCard()
    {
        using var h = new CalUi();
        var c = await InvitationAsync(h, X.Samples(), InvitationState.Unanswered);
        Assert.True(c.Title == "Vendor demo" && c.PillTitle == Cal.PillTitle(Cal.InvitationPill.Invitation));
        Assert.Equal(new Cal.DateTile("Fri", "16", "Oct"), c.DateTile);
        Assert.True(c.ShowsResponses && !c.ShowsStateStrip && !c.ShowsProposeNewTime);
        Assert.StartsWith("Conflicts with Standup", c.ConflictLine, StringComparison.Ordinal);
        Assert.True(c.Target == Cal.SamplePersonal && c.TargetIsPopup);
        Assert.Equal(["Gmail · Personal", "Gmail · Family"], c.Targets.Select(t => t.Title));
        Assert.Contains("The reply leaves from account Gmail.", c.ReplyNote);
        Assert.Empty(c.StateLine);
    }

    [Fact]
    public async Task AnswerWithTheTarget()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var c = await InvitationAsync(h, src, InvitationState.Unanswered);
        await h.Run(() => c.SetTarget(Cal.SampleFamily));
        Assert.Equal(Cal.SampleFamily, c.Target);
        await h.Run(() => c.SetTarget(new CalendarId("cal_unknown")));
        Assert.Equal(Cal.SampleFamily, c.Target);
        Assert.True(await h.Run(() =>
        {
            c.Respond(EventResponse.Accepted);
            return c.Busy;
        }));
        await h.IdleAsync();
        Assert.False(c.Busy);
        var p = LastWrite<InMemoryCalendarSource.Write.InvitationRespond>(src).Parameters;
        Assert.True(p.AccountId == Cal.SampleGmail && p.MessageId == Demo);
        Assert.True(p.Response == EventResponse.Accepted && p.CalendarId == Cal.SampleFamily && p.SendResponse is null);
        Assert.Equal("Accepted: Vendor demo", p.Mail?.Subject);
        Assert.True(c.Card.State == InvitationState.Answered && c.ShowsStateStrip);
        Assert.Equal("Accepted · in calendar Gmail · Family", c.StateStrip);
    }

    [Fact]
    public async Task DeclineWithoutReplySendsNothing()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var c = await InvitationAsync(h, src, InvitationState.Unanswered);
        await h.Run(() => c.Respond(EventResponse.Declined, withoutReply: true));
        await h.IdleAsync();
        var p = LastWrite<InMemoryCalendarSource.Write.InvitationRespond>(src).Parameters;
        Assert.True(p.CalendarId is null && p.SendResponse == false && p.Mail is null);
    }

    [Fact]
    public async Task ProposeNewTimeNeedsTheFlag()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var data = Card(InvitationState.Unanswered);
        var start = Cal.Date(2026, 10, 16, 13, 0, X.Zone);
        var c = await InvitationAsync(h, src, InvitationState.Unanswered);
        await h.Run(() => c.ProposeNewTime(start, start.AddHours(1)));
        await h.IdleAsync();
        Assert.Empty(src.Writes);
        data = data with { CanProposeNewTime = true };
        var p = await h.Run(() => new InvitationController(src, Cal.SampleGmail, new MessageId("m"), data, env: X.Env, pending: h.Pending));
        Assert.True(p.ShowsProposeNewTime);
        await h.Run(() => p.ProposeNewTime(start, start.AddHours(1)));
        await h.IdleAsync();
        var q = LastWrite<InMemoryCalendarSource.Write.InvitationRespond>(src).Parameters;
        Assert.True(q.Response == EventResponse.Tentative && q.ProposedStart == start && q.ProposedEnd == start.AddHours(1));
    }

    [Fact]
    public async Task RefusalShowsInline()
    {
        using var h = new CalUi();
        var src = X.Samples();
        src.Failures[API.InvitationRespond.Name] = new RpcException(new RpcError { Code = ErrorCode.CalendarConsentRequired, Message = "consent" });
        var c = await InvitationAsync(h, src, InvitationState.Unanswered);
        await h.Run(() => c.Respond(EventResponse.Accepted));
        await h.IdleAsync();
        Assert.True(c.Error == Cal.Text.ConsentLine && c.Card.State == InvitationState.Unanswered);
        var preview = await InvitationAsync(h, X.Samples(writes: false), InvitationState.Unanswered);
        await h.Run(() => preview.Respond(EventResponse.Accepted));
        await h.IdleAsync();
        Assert.Equal(Cal.Text.PreviewOnly, preview.Error);
    }

    [Fact]
    public async Task CancellationAndShowInCalendar()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var c = await InvitationAsync(h, src, InvitationState.Cancelled);
        await h.Run(c.ApplyCancel);
        await h.IdleAsync();
        Assert.True(c.CancelApplied && !c.Busy);
        Assert.Equal(
            [new InMemoryCalendarSource.Write.ApplyCancel(new InvitationApplyCancelParams { AccountId = Cal.SampleGmail, MessageId = Demo })],
            src.Writes);
        var shown = new List<(CalendarEventId? EventId, DateTimeOffset? Day)>();
        await h.Run(() =>
        {
            c.ShowInCalendarRequested += (_, x) => shown.Add(x);
            c.ShowInCalendar();
        });
        Assert.True(shown[0].EventId == new CalendarEventId("ev_sample_demo") && shown[0].Day == c.Card.Start);
        // A reply's one line.
        Assert.Equal("Jana Nováková accepted (applied to your event)", (await InvitationAsync(h, src, InvitationState.Processed)).ReplyCard);
    }

    // Windows-only.

    [Fact]
    public async Task ReplaceComparesTheCardsByContent()
    {
        using var h = new CalUi();
        var c = await InvitationAsync(h, X.Samples(), InvitationState.Unanswered);
        var changes = 0;
        await h.Run(() =>
        {
            c.Changed += (_, _) => changes++;
            c.SetTarget(Cal.SampleFamily);
        });
        Assert.Equal(1, changes);
        // The same card from a newer message.get (new lists, same content) keeps the choice.
        await h.Run(() => c.Replace(Card(InvitationState.Unanswered)));
        Assert.True(changes == 1 && c.Target == Cal.SampleFamily);
        // Another card takes its default target.
        await h.Run(() => c.Replace(Card(InvitationState.Unanswered) with { Title = "Vendor demo (moved)" }));
        Assert.True(changes == 2 && c.Target == Cal.SamplePersonal && c.Title == "Vendor demo (moved)");
    }

    [Fact]
    public async Task AnAnswerAsTheCardGoesAwayIsSent()
    {
        using var h = new CalUi();
        var src = X.Samples();
        var c = await InvitationAsync(h, src, InvitationState.Unanswered);
        await h.Run(() =>
        {
            c.Respond(EventResponse.Accepted);
            c.Close();
        });
        await h.IdleAsync();
        Assert.IsType<InMemoryCalendarSource.Write.InvitationRespond>(Assert.Single(src.Writes));
        Assert.True(c.Busy && c.Card.State == InvitationState.Unanswered); // the answer reached nobody
    }
}
