// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/FormatTests.swift, the counterpart of
// ui/internal/widget/format_test.go. Dates are rendered in the invariant
// culture (Swift: en_US_POSIX) so that the month names are the English ones
// the Go test expects whatever the machine's language, in the machine's
// time zone as the Go and Swift tests do. Windows-only: a name composed with
// other text is isolated (DisplayText, docs/security.md §4), so the long
// form and a list of participants carry U+2068 … U+2069 around each name
// where Go and Swift have the bare name.

using System;
using System.Collections.Generic;
using System.Globalization;
using Malachi.Core.Api;
using Malachi.Core.Text;
using Xunit;

namespace Malachi.Core.Tests.Text;

public sealed class FormatTests
{
    private static readonly CultureInfo Posix = CultureInfo.InvariantCulture;

    [Fact]
    public void DisplayNameTest()
    {
        (Address Input, string Want)[] cases =
        [
            (new Address { Name = "Alice Example", Email = "alice@example.invalid" }, "Alice Example"),
            (new Address { Email = "bob@example.invalid" }, "bob@example.invalid"),
            (new Address { Name = "   ", Email = " carol@example.invalid " }, "carol@example.invalid"),
            (new Address { Name = "<b>bold</b>", Email = "x@y" }, "<b>bold</b>"), // passed through, never markup
            (new Address { Email = "" }, ""),
        ];
        foreach (var (input, want) in cases)
        {
            Assert.Equal(want, Format.DisplayName(input));
        }
    }

    [Fact]
    public void FormatAddressTest()
    {
        (Address Input, string Want)[] cases =
        [
            (new Address { Name = "Alice", Email = "alice@example.invalid" }, "\u2068Alice\u2069 <alice@example.invalid>"),
            (new Address { Email = "bob@example.invalid" }, "bob@example.invalid"),
            (new Address { Name = "Nameless", Email = "" }, "Nameless"),
            (new Address { Email = "" }, ""),
        ];
        foreach (var (input, want) in cases)
        {
            Assert.Equal(want, Format.FormatAddress(input));
        }
    }

    [Theory]
    [InlineData(5, "5 B")]
    [InlineData(2048, "2 KiB")]
    [InlineData(3 << 20, "3.0 MiB")]
    public void FormatSizeTest(long input, string want)
    {
        Assert.Equal(want, Format.FormatSize(input));
    }

    [Fact]
    public void FormatDateTest()
    {
        var zone = TimeZoneInfo.Local;
        var now = At(zone, new DateTime(2026, 9, 2, 15, 30, 0));
        (DateTimeOffset Input, string Want)[] cases =
        [
            (DateTimeOffset.MinValue, ""),
            (now.AddHours(-2), "13:30"),
            (At(zone, new DateTime(2026, 9, 1, 15, 30, 0)), "1 Sep"),
            (At(zone, new DateTime(2026, 6, 2, 15, 30, 0)), "2 Jun"),
            (At(zone, new DateTime(2025, 9, 2, 15, 30, 0)), "2025-09-02"),
            (At(zone, new DateTime(2026, 1, 1, 0, 0, 1)), "1 Jan"),
        ];
        foreach (var (input, want) in cases)
        {
            Assert.Equal(want, Format.FormatDate(input, now, Posix, zone));
        }
        Assert.Equal("Wed, 2 Sep 2026 at 15:30", Format.FormatDateTime(now, Posix, zone));
        Assert.Equal("15:30", Format.FormatTime(now, Posix, zone));
    }

    [Fact]
    public void FormatParticipantsTest()
    {
        List<Address> list =
        [
            new Address { Name = "Bob", Email = "bob@example.invalid" },
            new Address { Email = "BOB@example.invalid" }, // the same person
            new Address { Name = "Alice", Email = "alice@example.invalid" },
            new Address { Name = "Carol", Email = "" }, // name only
            new Address { Email = "" }, // nothing
            new Address { Email = "dave@example.invalid" },
            new Address { Name = "<b>x</b>", Email = "x@example.invalid" }, // markup is text
        ];
        Assert.Equal(
            "\u2068Bob\u2069, \u2068Alice\u2069, \u2068Carol\u2069, \u2068dave@example.invalid\u2069, \u2068<b>x</b>\u2069",
            Format.FormatParticipants(list));
        Assert.Equal("", Format.FormatParticipants([]));
        // One name is not composed with anything: as it is.
        Assert.Equal("Bob", Format.FormatParticipants([list[0]]));
    }

    // Windows-only (DisplayText, docs/security.md §4): the review's From
    // name, whose override drew the address after it backwards, is cleaned
    // and isolated from the address; a right-to-left name stays as written.
    [Fact]
    public void NamesAreCleanedAndIsolatedFromTheAddress()
    {
        var hostile = new Address { Name = DisplayTextTests.HostileName, Email = "admin@evil.example" };
        Assert.Equal(DisplayTextTests.CleanedName, Format.DisplayName(hostile));
        Assert.Equal("\u2068" + DisplayTextTests.CleanedName + "\u2069 <admin@evil.example>", Format.FormatAddress(hostile));

        var hebrew = new Address { Name = "שלום כהן", Email = "shalom@example.org" };
        Assert.Equal("שלום כהן", Format.DisplayName(hebrew));
        Assert.Equal("\u2068שלום כהן\u2069 <shalom@example.org>", Format.FormatAddress(hebrew));

        // A name of nothing but controls is no name; the address is cleaned too.
        var controls = new Address { Name = "\u202E\u0007 ", Email = " bob@example.org\u202E" };
        Assert.Equal("bob@example.org", Format.DisplayName(controls));
        Assert.Equal("bob@example.org", Format.FormatAddress(controls));
        Assert.Equal("", Format.DisplayName(new Address { Name = "\u2066", Email = "\r\n" }));

        // So is a name of nothing but characters that draw nothing (the
        // marks, ZWSP, WJ, BOM): the address shows in its place.
        var invisible = new Address { Name = "\u200F\u200E\u061C \u200B\u2060\uFEFF", Email = "eve@example.org" };
        Assert.Equal("eve@example.org", Format.DisplayName(invisible));
        Assert.Equal("eve@example.org", Format.FormatAddress(invisible));
        Assert.Equal("", Format.DisplayName(new Address { Name = "\u200B", Email = "\uFEFF" }));
        // A mark beside letters stays.
        Assert.Equal("\u200Fמשה", Format.DisplayName(new Address { Name = "\u200Fמשה", Email = "m@example.org" }));

        // An Arabic name first cannot turn the list of participants around.
        List<Address> list =
        [
            new Address { Name = "محمد", Email = "m@example.org" },
            new Address { Name = "Bob\u202E", Email = "bob@example.org" },
        ];
        Assert.Equal("\u2068محمد\u2069, \u2068Bob\u2069", Format.FormatParticipants(list));
    }

    [Theory]
    [InlineData(0, "")]
    [InlineData(1, "")]
    [InlineData(2, "2")]
    [InlineData(17, "17")]
    public void ThreadCountTextTest(int n, string want)
    {
        Assert.Equal(want, Format.ThreadCountText(n));
    }

    // Windows: "today" is the day in the zone asked for, and the date is
    // shown there (an evening in New York is the next day in Prague).
    [Fact]
    public void TodayIsTheDayInTheTimeZone()
    {
        var prague = TimeZoneInfo.FindSystemTimeZoneById("Europe/Prague");
        var newYork = TimeZoneInfo.FindSystemTimeZoneById("America/New_York");
        var now = new DateTimeOffset(2026, 9, 2, 23, 30, 0, TimeSpan.FromHours(2)); // Prague, 17:30 in New York
        var morning = new DateTimeOffset(2026, 9, 2, 8, 0, 0, TimeSpan.FromHours(2)); // 02:00 in New York
        var yesterdayInNewYork = new DateTimeOffset(2026, 9, 2, 5, 0, 0, TimeSpan.FromHours(2)); // 23:00 on 1 Sep there
        Assert.Equal("08:00", Format.FormatDate(morning, now, Posix, prague));
        Assert.Equal("02:00", Format.FormatDate(morning, now, Posix, newYork));
        Assert.Equal("05:00", Format.FormatDate(yesterdayInNewYork, now, Posix, prague));
        Assert.Equal("1 Sep", Format.FormatDate(yesterdayInNewYork, now, Posix, newYork));
    }

    // Numbers are Go's in every culture (3.0, not 3,0), names follow the
    // culture given, and Go's zero time never throws, whatever the zone.
    [Fact]
    public void CultureAndEdges()
    {
        var saved = CultureInfo.CurrentCulture;
        try
        {
            CultureInfo.CurrentCulture = CultureInfo.GetCultureInfo("cs-CZ");
            Assert.Equal("3.0 MiB", Format.FormatSize(3 << 20));
            Assert.Equal("1.5 MiB", Format.FormatSize(3 << 19));
        }
        finally
        {
            CultureInfo.CurrentCulture = saved;
        }
        Assert.Equal("1023 B", Format.FormatSize(1023));
        Assert.Equal("1 KiB", Format.FormatSize(1024));
        Assert.Equal("1024 KiB", Format.FormatSize((1 << 20) - 1));
        Assert.Equal("1.0 MiB", Format.FormatSize(1 << 20));
        Assert.Equal("8796093022208.0 MiB", Format.FormatSize(long.MaxValue));
        Assert.Equal("-5 B", Format.FormatSize(-5));

        var prague = TimeZoneInfo.FindSystemTimeZoneById("Europe/Prague");
        var fixedTime = new DateTimeOffset(2026, 9, 2, 15, 4, 0, TimeSpan.FromHours(2));
        Assert.Equal("st 2. 9. 2026 v 15:04", Malachi.Core.I18n.Strftime.Format(fixedTime, "%a %-d. %-m. %Y v %H:%M", CultureInfo.GetCultureInfo("cs-CZ"), prague));
        Assert.Equal("Wed, 2 Sep 2026 at 15:04", Format.FormatDateTime(fixedTime, CultureInfo.GetCultureInfo("en-US"), prague));
        Assert.Equal("st, 2 zář 2026 at 15:04", Format.FormatDateTime(fixedTime, CultureInfo.GetCultureInfo("cs-CZ"), prague));

        var west = TimeZoneInfo.FindSystemTimeZoneById("America/New_York");
        Assert.Equal("", Format.FormatDate(DateTimeOffset.MinValue, fixedTime, Posix, west));
        Assert.Equal("00:00", Format.FormatTime(DateTimeOffset.MinValue, Posix, west));
        Assert.Equal("", Format.FormatDate(new DateTimeOffset(1, 1, 1, 23, 59, 59, TimeSpan.Zero), fixedTime, Posix, prague));
    }

    private static DateTimeOffset At(TimeZoneInfo zone, DateTime wallClock) =>
        new(wallClock, zone.GetUtcOffset(wallClock));
}
