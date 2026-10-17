// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The rules of macos/Sources/MalachiCore/Calendar/CalendarSource.swift
// (Cal.Snapshot, Cal.errorMessage, rangeKey, eventKey, rangesOverlap) and
// DaemonCalendarSource.swift (CalendarObservers), which Swift reaches only
// through its controller and source tests (CalendarControllerTests,
// DaemonCalendarSourceTests, ported with the controllers). Windows-only
// suite: the snapshot's lookups over the samples, the sentence of a failed
// call, the cache keys and the observers' order.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Transport;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Calendars;

public sealed class CalendarSourceTests
{
    private static readonly Cal.Samples Data = Cal.SampleData(F.At(2026, 10, 14, 12), F.Prague);

    private static Cal.Snapshot Disabled(Cal.Snapshot s, CalendarId id) => s with
    {
        CalendarAccounts =
        [
            .. s.CalendarAccounts.Select(a => a with
            {
                Calendars = [.. a.Calendars.Select(c => c.Id == id ? c with { Disabled = true } : c)],
            }),
        ],
    };

    [Fact]
    public void EmptySnapshot()
    {
        var s = Cal.Snapshot.Empty;
        Assert.Equal(Cal.Phase.Loading, s.Phase);
        Assert.Empty(s.Accounts);
        Assert.Empty(s.CalendarAccounts);
        Assert.Equal(0, s.Unanswered);
        Assert.Same(Cal.DefaultPreferences, s.Preferences);
        Assert.Null(s.FindAccount(Cal.SampleWork));
        Assert.Equal("", s.AccountName(Cal.SampleWork));
        Assert.Equal("", s.MyName(Cal.SampleWork));
        Assert.Empty(s.VisibleCalendars);
        Assert.Empty(s.WritableCalendars);
    }

    [Fact]
    public void SnapshotLookups()
    {
        var s = Cal.SampleSnapshot(Data);
        Assert.Equal("Nextcloud", s.AccountName(Cal.SampleNextcloud));
        Assert.Equal(Cal.SampleGmail, s.FindCalendarAccount(Cal.SampleGmail)?.AccountId);
        Assert.Equal("Family", s.CalendarName(Cal.SampleFamily));
        Assert.Equal("me@example.org", s.MyAddress(Cal.SampleWork));
        // Without a display name, the address.
        Assert.Equal("me@example.org", s.MyName(Cal.SampleWork));
        var named = s with
        {
            Accounts = [.. s.Accounts.Select(a => a.Id == Cal.SampleWork ? a with { Config = a.Config with { DisplayName = "Jana‮" } } : a)],
        };
        Assert.Equal("Jana", named.MyName(Cal.SampleWork));
        Assert.Equal(3, s.States.Count);
        Assert.Equal(6, s.VisibleCalendars.Count);
        // Each account's default first, accounts in order; never birthdays,
        // holidays or a read-only calendar.
        Assert.Equal(
            [Cal.SampleWorkCalendar, Cal.SamplePersonal, Cal.SampleFamily, Cal.SampleSport],
            s.WritableCalendars.Select(c => c.Id));
    }

    [Fact]
    public void DisabledCalendarsShowOnlyInPreferences()
    {
        var s = Disabled(Cal.SampleSnapshot(Data), Cal.SamplePersonal);
        Assert.Null(s.FindCalendar(Cal.SamplePersonal));
        Assert.Equal("", s.CalendarName(Cal.SamplePersonal));
        Assert.DoesNotContain(Cal.SamplePersonal, s.VisibleCalendars);
        Assert.DoesNotContain(s.WritableCalendars, c => c.Id == Cal.SamplePersonal);
        Assert.DoesNotContain(s.ActiveCalendarAccounts.SelectMany(a => a.Calendars), c => c.Id == Cal.SamplePersonal);
        // Preferences keeps it.
        Assert.Contains(s.FindCalendarAccount(Cal.SampleGmail)!.Calendars, c => c.Id == Cal.SamplePersonal);
        // A hidden calendar is active, not visible.
        var hidden = s with
        {
            CalendarAccounts =
            [
                .. s.CalendarAccounts.Select(a => a with
                {
                    Calendars = [.. a.Calendars.Select(c => c.Id == Cal.SampleSport ? c with { Visible = false } : c)],
                }),
            ],
        };
        Assert.NotNull(hidden.FindCalendar(Cal.SampleSport));
        Assert.DoesNotContain(Cal.SampleSport, hidden.VisibleCalendars);
    }

    [Fact]
    public void SnapshotsCompareByContent()
    {
        var a = Cal.SampleSnapshot(Data);
        Assert.Equal(a, a with { Accounts = [.. a.Accounts], CalendarAccounts = [.. a.CalendarAccounts], Invitations = [.. a.Invitations] });
        Assert.NotEqual(a, a with { Unanswered = 2 });
        Assert.NotEqual(a, a with { Phase = Cal.Phase.Failed });
        Assert.NotEqual(a, a with { Invitations = [.. a.Invitations.Skip(1)] });
    }

    [Fact]
    public void ErrorMessages()
    {
        Assert.Equal("The preview cannot do that.", Cal.ErrorMessage(Cal.SourceError.Preview));
        Assert.Equal(Cal.SourceErrorKind.Preview, Cal.SourceError.Preview.Kind);
        Assert.Equal(
            "The event no longer exists.",
            Cal.ErrorMessage(new RpcException(new RpcError { Code = ErrorCode.EventGone, Message = "gone" })));
        Assert.Equal(
            "The event was changed elsewhere. It has been reloaded.",
            Cal.ErrorMessage(new RpcException(new RpcError { Code = ErrorCode.Conflict, Message = "x" }), "Saving"));
        // A code without a calendar sentence: the generic one, with what.
        Assert.Equal(
            "Saving failed: unknown account",
            Cal.ErrorMessage(new RpcException(new RpcError { Code = ErrorCode.AccountNotFound, Message = "x" }), "Saving"));
        Assert.Equal(
            "Calendar failed: unknown account",
            Cal.ErrorMessage(new RpcException(new RpcError { Code = ErrorCode.AccountNotFound, Message = "x" })));
        Assert.Equal("Calendar failed", Cal.ErrorMessage(new InvalidOperationException("x")));
        Assert.Equal("Calendar failed", Cal.ErrorMessage(null));
    }

    [Fact]
    public void CacheKeys()
    {
        var r = Cal.ViewRange(Cal.View.Week, F.At(2026, 10, 14, 12), Cal.Weekday.Monday, F.Prague);
        Assert.Equal(Cal.RangeKey(r), Cal.RangeKey(r with { }));
        Assert.Equal(Cal.RangeKey(r), Cal.RangeKey(new Cal.Range(r.From.ToUniversalTime(), r.To.ToUniversalTime(), F.Prague)));
        Assert.NotEqual(Cal.RangeKey(r), Cal.RangeKey(r with { Zone = F.Utc }));
        Assert.NotEqual(Cal.RangeKey(r), Cal.RangeKey(r with { To = r.To.AddDays(1) }));
        Assert.Equal("e_1|", Cal.EventKey(new CalendarEventId("e_1"), null));
        Assert.Equal("e_1|2026-10-16", Cal.EventKey(new CalendarEventId("e_1"), new DateOrTime("2026-10-16")));
        Assert.Equal("|", Cal.EventKey(default, null));
    }

    [Theory]
    [InlineData(10, 12, null, null, true)]
    [InlineData(10, 12, 12, null, false)]
    [InlineData(10, 12, 11, null, true)]
    [InlineData(10, 12, null, 10, false)]
    [InlineData(10, 12, null, 11, true)]
    [InlineData(10, 12, 8, 10, false)]
    [InlineData(10, 12, 12, 14, false)]
    [InlineData(10, 12, 9, 13, true)]
    public void RangesOverlap(int a, int b, int? from, int? to, bool want)
    {
        static DateTimeOffset H(int h) => F.UtcAt(2026, 10, 16, h, 0);
        Assert.Equal(want, Cal.RangesOverlap(H(a), H(b), from is { } f ? H(f) : null, to is { } t ? H(t) : null));
    }

    [Fact]
    public void ObserversInOrder()
    {
        var o = new CalendarObservers();
        var calls = new List<string>();
        var first = o.Add(c => calls.Add("1:" + c.GetType().Name));
        o.Add(c => calls.Add("2:" + c.GetType().Name));
        Assert.Equal(2, o.Count);
        o.Notify(new Cal.SourceChange.SnapshotChanged());
        Assert.Equal(["1:SnapshotChanged", "2:SnapshotChanged"], calls);
        calls.Clear();
        first.Cancel();
        first.Cancel();
        o.Notify(new Cal.SourceChange.EventChanged(new CalendarEventId("e")));
        Assert.Equal(["2:EventChanged"], calls);
        Assert.Equal(1, o.Count);
        // A handler that cancels the one after it: that one is not called.
        var o2 = new CalendarObservers();
        var seen = new List<int>();
        Malachi.Core.Boards.BoardObserverToken? second = null;
        o2.Add(_ =>
        {
            seen.Add(1);
            second?.Cancel();
        });
        second = o2.Add(_ => seen.Add(2));
        o2.Notify(new Cal.SourceChange.OccurrencesChanged(null, null));
        Assert.Equal([1], seen);
    }

    [Fact]
    public void SourceChangesCompareByValue()
    {
        var t = F.UtcAt(2026, 10, 16, 8, 0);
        Assert.Equal(new Cal.SourceChange.SnapshotChanged(), new Cal.SourceChange.SnapshotChanged());
        Assert.Equal(new Cal.SourceChange.OccurrencesChanged(t, null), new Cal.SourceChange.OccurrencesChanged(t, null));
        Assert.NotEqual<Cal.SourceChange>(new Cal.SourceChange.OccurrencesChanged(t, null), new Cal.SourceChange.OccurrencesChanged(null, t));
        Assert.Equal(new Cal.SourceChange.Failed("x"), new Cal.SourceChange.Failed("x"));
    }
}
