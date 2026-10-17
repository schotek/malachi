// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The rules of macos/Sources/MalachiCore/Calendar/CalendarText.swift; GTK:
// ui/internal/calendar/text.go (StatusLine, ErrorText, EmptyTitle,
// EmptyBody, ViewTitle, the date formats), which Go reaches only through
// po_test.go (every msgid is in the template; here StringsCheckTests).
// Windows-only suite: the texts in English, the order of StatusLine's
// cases, and which codes have a calendar sentence.

using System;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Xunit;
using F = Malachi.Core.Tests.Calendars.CalFixture;

namespace Malachi.Core.Tests.Calendars;

public sealed class CalendarTextTests
{
    private static readonly DateTimeOffset Now = F.UtcAt(2026, 10, 16, 12, 0);

    private static CalendarSyncState State(string status, DateTimeOffset? lastSync = null) => new()
    {
        AccountId = new AccountId("a"),
        Status = new CalendarSyncStatus(status),
        Progress = -1,
        LastSync = lastSync,
    };

    [Fact]
    public void StatusLineOrder()
    {
        var two = Now.AddMinutes(-2);
        var ten = Now.AddMinutes(-10);
        Assert.Equal("", Cal.Text.StatusLine([], Now, F.Utc));
        Assert.Equal("", Cal.Text.StatusLine([State(CalendarSyncStatus.Idle)], Now, F.Utc));
        // The oldest last sync.
        Assert.Equal("Calendars synced 10 minutes ago",
            Cal.Text.StatusLine([State(CalendarSyncStatus.Idle, two), State(CalendarSyncStatus.Error, ten)], Now, F.Utc));
        // All offline, but only all.
        Assert.Equal("Calendars are offline",
            Cal.Text.StatusLine([State(CalendarSyncStatus.Offline, two), State(CalendarSyncStatus.Offline)], Now, F.Utc));
        Assert.Equal("Calendars synced 2 minutes ago",
            Cal.Text.StatusLine([State(CalendarSyncStatus.Offline, two), State(CalendarSyncStatus.Idle)], Now, F.Utc));
        // Syncing beats offline; consent beats everything, wherever it is.
        Assert.Equal("Syncing calendars…",
            Cal.Text.StatusLine([State(CalendarSyncStatus.Offline), State(CalendarSyncStatus.Syncing, two)], Now, F.Utc));
        Assert.Equal("Calendar access needs a new sign-in",
            Cal.Text.StatusLine(
                [State(CalendarSyncStatus.Syncing), State(CalendarSyncStatus.Offline), State(CalendarSyncStatus.ConsentRequired)], Now, F.Utc));
        Assert.Equal("Calendars synced just now", Cal.Text.SyncedLine(Now, Now, F.Utc));
    }

    [Theory]
    [InlineData(ErrorCode.ReadOnly, "This calendar or event cannot be changed.")]
    [InlineData(ErrorCode.EventGone, "The event no longer exists.")]
    [InlineData(ErrorCode.EventNotFound, "The event no longer exists.")]
    [InlineData(ErrorCode.CalendarNotFound, "The calendar no longer exists.")]
    [InlineData(ErrorCode.Conflict, "The event was changed elsewhere. It has been reloaded.")]
    [InlineData(ErrorCode.CalendarConsentRequired, "Calendar access needs a new sign-in")]
    [InlineData(ErrorCode.MalformedCalendar, "The calendar data could not be read.")]
    [InlineData(ErrorCode.Offline, "The calendar cannot be changed while offline.")]
    [InlineData(ErrorCode.AuthFailed, null)]
    [InlineData(ErrorCode.InvalidArgument, null)]
    public void ErrorTexts(int code, string? want) => Assert.Equal(want, Cal.Text.ErrorText(new ErrorCode(code)));

    [Fact]
    public void LongErrorTextsExist()
    {
        Assert.StartsWith("No account can send", Cal.Text.ErrorText(ErrorCode.InvitationsUnavailable), StringComparison.Ordinal);
        Assert.StartsWith("The event repeats", Cal.Text.ErrorText(ErrorCode.RecurrenceUnsupported), StringComparison.Ordinal);
    }

    [Fact]
    public void EmptyStatesAndViews()
    {
        Assert.Equal(["No Calendars", "Calendar Sync Is Off", "Loading Calendars…", "Calendar Unavailable"],
            Array.ConvertAll(Enum.GetValues<Cal.EmptyKind>(), Cal.Text.EmptyTitle));
        Assert.Equal("", Cal.Text.EmptyBody(Cal.EmptyKind.Loading));
        Assert.Equal("The Malachi Mail backend is not running.", Cal.Text.EmptyBody(Cal.EmptyKind.NoBackend));
        Assert.NotEmpty(Cal.Text.EmptyBody(Cal.EmptyKind.NoAccounts));
        Assert.NotEmpty(Cal.Text.EmptyBody(Cal.EmptyKind.SyncOff));
        Assert.Equal(["Day", "Week", "Month", "Agenda"], Array.ConvertAll(Enum.GetValues<Cal.View>(), Cal.Text.ViewTitle));
        Assert.Equal("Calendar", Cal.Text.ModeName);
        Assert.Equal("1 unanswered invitation", Cal.Text.UnansweredCount(1));
        Assert.Equal("3 unanswered invitations", Cal.Text.UnansweredCount(3));
        Assert.Equal("week 42", Cal.WeekLabel(42));
    }

    [Fact]
    public void DateFormats()
    {
        var env = F.Env(F.Prague);
        // 8:05 UTC is 10:05 in Prague.
        var t = F.UtcAt(2026, 10, 13, 8, 5);
        Assert.Equal("10:05", Cal.Text.TimeText(t, env));
        Assert.Equal("Tuesday 13 October 2026", Cal.Text.LongDateText(t, env));
        Assert.Equal("Tuesday 13 October", Cal.Text.WeekdayDateText(t, env));
        Assert.Equal("13 October", Cal.Text.DayMonthText(t, env));
        Assert.Equal("13 October 2026", Cal.Text.DayMonthYearText(t, env));
        Assert.Equal("13", Cal.Text.DayText(t, env));
        Assert.Equal("October 2026", Cal.Text.MonthYearText(t, env));
        Assert.Equal("October", Cal.Text.MonthText(t, env));
        Assert.Equal("Tuesday", Cal.Text.WeekdayText(t, env));
        Assert.Equal("Tue", Cal.Text.ShortWeekdayText(t, env));
        Assert.Equal("Oct", Cal.Text.ShortMonthText(t, env));
        Assert.Equal("Tue 13 Oct 2026 10:05", Cal.Text.DateTimeText(t, env));
        Assert.Equal("10:05–11:35", Cal.Text.TimeRangeText(t, t.AddMinutes(90), env));
        Assert.Equal("12–18 October", Cal.Text.RangeText("12", "18 October"));
        Assert.Equal("Work · Calendar", Cal.Text.Dot("Work", "Calendar"));
    }
}
