// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/CalendarAlertsControllerTests.swift:
// the alerts (Controllers/CalendarAlertsController.cs): due presents,
// dismissed from elsewhere withdraws, Snooze and a dismissal write and
// withdraw, the notification preference; then Windows-only cases (a refused
// snooze, an all-day alert's day, the toast's tag, a due alert again
// replacing the first). Swift's onPresent and onWithdraw are a recording
// ICalendarAlertNotifier.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Presentation;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Transport;
using Microsoft.Extensions.Time.Testing;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;
using Write = Malachi.Core.Calendars.InMemoryCalendarSource.Write;

namespace Malachi.Core.Tests.Controllers;

public sealed class CalendarAlertsControllerTests
{
    /// <summary>Wednesday 14 October 2026, 10:17 in Prague (Swift <c>calNow</c>).</summary>
    private static readonly DateTimeOffset Now = F.At(2026, 10, 14, 10, 17);

    private static readonly CalendarAlertId Al1 = new("al_1");

    private static EventAlert Alert(string state, bool video = true) => new()
    {
        AlertId = Al1,
        State = state,
        EventId = new CalendarEventId("ev_sample_planning"),
        AccountId = Cal.SampleWork,
        CalendarId = Cal.SampleWorkCalendar,
        Title = "Q4 planning",
        Start = Now.AddSeconds(600),
        End = Now.AddSeconds(3600),
        AllDay = false,
        Location = "Room A",
        VideoCall = video,
        MinutesBefore = 10,
    };

    private static InMemoryCalendarSource Samples() => new(Cal.SampleData(Now, F.Prague)) { EmulatesWrites = true };

    [Fact]
    public async Task DueDismissAndSnooze()
    {
        await using var h = new Harness();
        var src = Samples();
        var a = await h.Ui.RunAsync(() => h.Controller(src));
        await h.Ui.RunAsync(() =>
        {
            a.Handle(new DaemonNotification.EventAlert(new EventAlertNotification { Alert = Alert(EventAlertState.Due) }));
            Assert.True(h.Notifier.Presented.Count == 1 && h.Notifier.Presented[0].Join && h.Notifier.Presented[0].Title == "Q4 planning");
            Assert.Equal(Cal.AlertNotification(Alert(EventAlertState.Due), Now, F.Env()).Body, h.Notifier.Presented[0].Body);
            Assert.True(a.Shown.ContainsKey(Al1));
            // Another client dismissed it.
            a.Handle(Alert(EventAlertState.Dismissed));
            Assert.True(h.Notifier.Withdrawn.SequenceEqual([Al1]) && a.Shown.Count == 0);
            // A dismissal of an alert not shown withdraws nothing.
            a.Handle(Alert(EventAlertState.Dismissed));
            Assert.Single(h.Notifier.Withdrawn);
            a.Handle(Alert(EventAlertState.Due));
            a.Snooze(Al1);
            Assert.Equal(2, h.Notifier.Withdrawn.Count);
        });
        await h.IdleAsync();
        Assert.Equal([new Write.Snooze(new EventSnoozeAlertParams { AlertId = Al1, Minutes = 5 })], await h.Ui.RunAsync(() => src.Writes.ToList()));
        await h.Ui.RunAsync(() =>
        {
            a.Handle(Alert(EventAlertState.Due));
            a.Dismiss(Al1);
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(2, src.Writes.Count);
            Assert.Equal(new Write.Dismiss(new EventDismissAlertParams { AlertId = Al1 }), src.Writes[^1]);
            // Other notifications are not this controller's.
            a.Handle(new DaemonNotification.InvitationsChanged(new InvitationsChangedNotification()));
            Assert.Equal(3, h.Notifier.Presented.Count);
        });
    }

    [Fact]
    public async Task NotificationsOffPresentNothing()
    {
        await using var h = new Harness();
        var a = await h.Ui.RunAsync(() => h.Controller(Samples(), enabled: () => false));
        await h.Ui.RunAsync(() =>
        {
            a.Handle(Alert(EventAlertState.Due, video: false));
            Assert.True(h.Notifier.Presented.Count == 0 && a.Shown.Count == 0);
        });
    }

    // Windows-only cases.

    /// <summary>A refused snooze is reported (a log line), the notification stays withdrawn.</summary>
    [Fact]
    public async Task ARefusedSnoozeIsReported()
    {
        await using var h = new Harness();
        var src = Samples();
        src.Failures[API.EventSnoozeAlert.Name] = new RpcException(new RpcError { Code = ErrorCode.CalendarNotFound, Message = "gone" });
        var failures = new List<string>();
        var a = await h.Ui.RunAsync(() =>
        {
            var made = h.Controller(src);
            made.Failed += (_, e) => failures.Add(e);
            made.Handle(Alert(EventAlertState.Due));
            made.Snooze(Al1);
            return made;
        });
        await h.IdleAsync();
        Assert.Equal([Cal.Text.ErrorText(ErrorCode.CalendarNotFound)!], failures);
        Assert.Empty(await h.Ui.RunAsync(() => a.Shown.Keys.ToList()));
        Assert.Equal([Al1], h.Notifier.Withdrawn);
    }

    /// <summary>
    /// The request carries the day a click opens (an all-day alert's date in
    /// the zone), Join only with a video call, and a toast tag that fits.
    /// </summary>
    [Fact]
    public async Task TheRequestCarriesTheDayAndTheTag()
    {
        await using var h = new Harness();
        var a = await h.Ui.RunAsync(() => h.Controller(Samples()));
        var longId = new CalendarAlertId(new string('x', 100));
        await h.Ui.RunAsync(() =>
        {
            a.Handle(Alert(EventAlertState.Due, video: false) with
            {
                AlertId = longId,
                AllDay = true,
                Start = null,
                End = null,
                StartDate = new CalendarDate("2026-10-15"),
                VideoCall = null,
            });
            var r = h.Notifier.Presented[^1];
            Assert.False(r.Join);
            Assert.Equal(F.At(2026, 10, 15), r.Day);
            Assert.True(r.Tag.Length <= DesktopNotification.MaxIdentifierLength);
            Assert.StartsWith("alert-", r.Tag, StringComparison.Ordinal);
            Assert.Equal(CalendarAlertsController.TagOf(longId), r.Tag);
            Assert.Equal("alert-al_1", CalendarAlertsController.TagOf(Al1));
            // The timed alert's day is its start.
            a.Handle(Alert(EventAlertState.Due));
            Assert.Equal(Now.AddSeconds(600), h.Notifier.Presented[^1].Day);
            Assert.Equal(new CalendarEventId("ev_sample_planning"), h.Notifier.Presented[^1].EventId);
        });
    }

    /// <summary>Due again (after a snooze) replaces the request shown; one dismissal withdraws it once.</summary>
    [Fact]
    public async Task DueAgainReplacesTheRequest()
    {
        await using var h = new Harness();
        var a = await h.Ui.RunAsync(() => h.Controller(Samples()));
        await h.Ui.RunAsync(() =>
        {
            a.Handle(Alert(EventAlertState.Due));
            a.Handle(Alert(EventAlertState.Due) with { Title = "Q4 planning (moved)" });
            Assert.Equal(2, h.Notifier.Presented.Count);
            Assert.Single(a.Shown);
            Assert.Equal("Q4 planning (moved)", a.Shown[Al1].Title);
            a.Handle(Alert(EventAlertState.Dismissed));
            a.Handle(Alert(EventAlertState.Dismissed));
            Assert.Single(h.Notifier.Withdrawn);
        });
    }

    /// <summary>Records what the controller presents and withdraws (Swift's onPresent and onWithdraw).</summary>
    private sealed class RecordingNotifier : ICalendarAlertNotifier
    {
        public List<CalendarAlertsController.Request> Presented { get; } = [];

        public List<CalendarAlertId> Withdrawn { get; } = [];

        public void Present(CalendarAlertsController.Request request) => Presented.Add(request);

        public void Withdraw(CalendarAlertId alertId) => Withdrawn.Add(alertId);
    }

    private sealed class Harness : IAsyncDisposable
    {
        private readonly List<CalendarAlertsController> controllers = [];

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public FakeTimeProvider Clock { get; } = new(Now);

        public RecordingNotifier Notifier { get; } = new();

        public CalendarAlertsController Controller(ICalendarDataSource source, Func<bool>? enabled = null)
        {
            var c = new CalendarAlertsController(source, Notifier, F.Env(), Clock, enabled, pending: Pending);
            controllers.Add(c);
            return c;
        }

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending);

        public async ValueTask DisposeAsync()
        {
            await Ui.RunAsync(() =>
            {
                foreach (var c in controllers)
                {
                    c.Dispose();
                }
            });
            Ui.Dispose();
        }
    }
}
