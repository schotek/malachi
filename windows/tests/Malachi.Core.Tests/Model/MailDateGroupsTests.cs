// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/maildate/groups_test.go and
// macos/Tests/MalachiCoreTests/MailDateGroupsTests.swift.

using System;
using System.Globalization;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Xunit;
using static Malachi.Core.Tests.Model.ThreadModelTests;

namespace Malachi.Core.Tests.Model;

public sealed class MailDateGroupsTests
{
    private static readonly DateTimeOffset Now = new(2026, 10, 15, 12, 0, 0, TimeSpan.Zero);

    [Theory]
    [InlineData("2026-10-16", MailDateGroupKind.Today)]
    [InlineData("2026-10-15", MailDateGroupKind.Today)]
    [InlineData("2026-10-14", MailDateGroupKind.Yesterday)]
    [InlineData("2026-10-12", MailDateGroupKind.ThisWeek)]
    [InlineData("2026-10-11", MailDateGroupKind.LastWeek)]
    [InlineData("2026-10-05", MailDateGroupKind.LastWeek)]
    [InlineData("2026-10-04", MailDateGroupKind.ThisMonth)]
    [InlineData("2026-09-01", MailDateGroupKind.LastMonth)]
    [InlineData("2026-08-31", MailDateGroupKind.ThisYear)]
    [InlineData("2025-12-31", MailDateGroupKind.OlderYear)]
    public void CalendarBoundaries(string date, MailDateGroupKind expected)
    {
        var instant = DateTimeOffset.Parse(date + "T12:00:00Z", CultureInfo.InvariantCulture);
        var group = MailDateGroups.Containing(instant, Now, TimeZoneInfo.Utc, DayOfWeek.Monday);
        Assert.Equal(expected, group.Kind);
        Assert.Equal(expected == MailDateGroupKind.OlderYear ? 2025 : 0, group.Year);
    }

    [Fact]
    public void WeekStartFollowsLocaleAndCrossesYearBoundary()
    {
        var now = new DateTimeOffset(2026, 1, 7, 12, 0, 0, TimeSpan.Zero);
        var sunday = now.AddDays(-3);
        Assert.Equal(MailDateGroupKind.ThisWeek, MailDateGroups.Containing(sunday, now, TimeZoneInfo.Utc, DayOfWeek.Sunday).Kind);
        Assert.Equal(MailDateGroupKind.LastWeek, MailDateGroups.Containing(sunday, now, TimeZoneInfo.Utc, DayOfWeek.Monday).Kind);
        Assert.Equal(MailDateGroupKind.LastWeek, MailDateGroups.Containing(now.AddDays(-7), now, TimeZoneInfo.Utc, DayOfWeek.Monday).Kind);
    }

    [Fact]
    public void LocalMidnightAndDstUseCalendarDays()
    {
        var zone = TimeZoneInfo.FindSystemTimeZoneById("Europe/Prague");
        var now = new DateTimeOffset(2026, 3, 30, 0, 30, 0, TimeSpan.FromHours(2));
        var yesterday = new DateTimeOffset(2026, 3, 29, 0, 30, 0, TimeSpan.FromHours(1));
        Assert.Equal(MailDateGroupKind.Yesterday, MailDateGroups.Containing(yesterday, now, zone, DayOfWeek.Monday).Kind);
        Assert.Equal(MailDateGroupKind.Today, MailDateGroups.Containing(now.AddMinutes(-15), now, zone, DayOfWeek.Monday).Kind);
        var autumn = new DateTimeOffset(2026, 10, 26, 0, 30, 0, TimeSpan.FromHours(1));
        var previous = new DateTimeOffset(2026, 10, 25, 0, 30, 0, TimeSpan.FromHours(2));
        Assert.Equal(MailDateGroupKind.Yesterday, MailDateGroups.Containing(previous, autumn, zone, DayOfWeek.Monday).Kind);
    }

    [Fact]
    public void FlaggedFirstAndExpandedMembersStayWithParent()
    {
        var today = Row("today", Now);
        var old = Row("old", Now.AddYears(-2), Flag.Flagged);
        var parent = Row("parent", Now.AddDays(-1));
        var child = Row("child", Now.AddYears(-1), Flag.Flagged) with { Member = true };
        var sections = MailDateGroups.Sections([today, parent, child, old], Now, TimeZoneInfo.Utc, DayOfWeek.Monday);
        Assert.Equal([MailDateGroupKind.Flagged, MailDateGroupKind.Today, MailDateGroupKind.Yesterday], sections.Select(s => s.Group.Kind));
        Assert.Equal([parent.Key, child.Key], sections[2].Rows.Select(r => r.Key));
        Assert.Equal(old.Key, Assert.Single(sections[0].Rows).Key);
        Assert.Equal(4, sections.Sum(s => s.Rows.Count));
    }

    [Fact]
    public void ConversationUsesAggregateFlagAndLatestDate()
    {
        var message = Member("latest", "thread", 0, "Sender");
        var summary = Thr("thread", 2, 0, message, Flag.Flagged) with { LatestDate = Now };
        var row = new ListRow { Key = new ListKey(Thread: summary.Id), Message = message, Summary = summary, Thread = true };
        var group = Assert.Single(MailDateGroups.Sections([row], Now, TimeZoneInfo.Utc, DayOfWeek.Monday)).Group;
        Assert.Equal(MailDateGroupKind.Flagged, group.Kind);
        row = row with { Summary = summary with { Flags = [] } };
        group = Assert.Single(MailDateGroups.Sections([row], Now, TimeZoneInfo.Utc, DayOfWeek.Monday)).Group;
        Assert.Equal(MailDateGroupKind.Today, group.Kind);
    }

    [Fact]
    public void EmptyListHasNoHeaders() => Assert.Empty(MailDateGroups.Sections([], Now, TimeZoneInfo.Utc, DayOfWeek.Monday));

    internal static ListRow Row(string id, DateTimeOffset date, params Flag[] flags)
    {
        var message = Member(id, null, 0, "Sender", flags) with { Date = date };
        return new ListRow { Key = new ListKey(Message: message.Id), Message = message };
    }
}
