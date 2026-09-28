// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows addition to the date tests of APICodingTests.swift: the edges of
// Rfc3339 (Coding.cs) where a DateTimeOffset differs from Swift's Date, a
// double of seconds: it ends at years 1 and 9999 and holds 100 ns ticks.

using System;
using System.Globalization;
using System.Text.Json;
using Malachi.Core.Api;
using Xunit;

namespace Malachi.Core.Tests.Api;

public sealed class Rfc3339Tests
{
    /// <summary>
    /// Go's year 0, and year 1 moved below it by an offset, are before what a
    /// DateTimeOffset holds: they clamp to Go's zero time instead of failing
    /// the result that carries them.
    /// </summary>
    [Theory]
    [InlineData("0000-01-01T00:00:00Z")]
    [InlineData("0000-12-31T23:59:59.9999999Z")]
    [InlineData("0001-01-01T00:00:00+00:01")]
    public void TimesBeforeYearOneClampToGoZero(string s)
    {
        var date = Rfc3339.Parse(s);
        Assert.Equal(DateTimeOffset.MinValue, date);
        Assert.True(date!.Value.IsGoZero);
        Assert.Equal(DateTimeOffset.GoZero, ApiJson.Decode<KnownSender>($$$"""{"address":"a","source":"sent","addedAt":"{{{s}}}"}""").AddedAt);
    }

    [Theory]
    [InlineData("9999-12-31T23:59:59-00:01")]
    [InlineData("9999-12-31T23:59:59.99999999Z")]
    public void TimesAfterYear9999ClampToMaxValue(string s)
    {
        Assert.Equal(DateTimeOffset.MaxValue, Rfc3339.Parse(s));
    }

    [Fact]
    public void ResultsAreUtc()
    {
        var date = Rfc3339.Parse("2026-09-02T12:00:00+02:00")!.Value;
        Assert.Equal(TimeSpan.Zero, date.Offset);
        Assert.Equal(new DateTimeOffset(2026, 9, 2, 10, 0, 0, TimeSpan.Zero), date);
    }

    /// <summary>As Swift and Go's arithmetic: 23:59:60 is the next day's midnight.</summary>
    [Fact]
    public void LeapSecondRollsOver()
    {
        Assert.Equal("2017-01-01T00:00:00Z", Rfc3339.Format(Rfc3339.Parse("2016-12-31T23:59:60Z")!.Value));
        Assert.Null(Rfc3339.Parse("2016-12-31T23:59:61Z"));
    }

    /// <summary>As Swift: the day of the month is checked against 31 only.</summary>
    [Fact]
    public void DayOfMonthIsCheckedAgainstThirtyOneOnly()
    {
        Assert.Equal("2026-03-03T00:00:00Z", Rfc3339.Format(Rfc3339.Parse("2026-02-31T00:00:00Z")!.Value));
        Assert.Null(Rfc3339.Parse("2026-02-32T00:00:00Z"));
        Assert.Null(Rfc3339.Parse("2026-02-00T00:00:00Z"));
        Assert.Null(Rfc3339.Parse("2026-00-01T00:00:00Z"));
    }

    [Fact]
    public void FractionDigitsBeyondTheSeventhAreCut()
    {
        var second = Rfc3339.Parse("2026-09-02T10:00:00Z")!.Value;
        Assert.Equal(second.AddTicks(1_234_567), Rfc3339.Parse("2026-09-02T10:00:00.12345678999Z"));
        Assert.Equal(second.AddTicks(1), Rfc3339.Parse("2026-09-02T10:00:00.0000001Z"));
        Assert.Equal(second, Rfc3339.Parse("2026-09-02T10:00:00.00000009Z"));
        Assert.Equal(second.AddTicks(9_999_999), Rfc3339.Parse("2026-09-02T10:00:00.999999999999Z"));
    }

    [Theory]
    [InlineData("2026-09-02T10:00:00+23:59", true)]
    [InlineData("2026-09-02T10:00:00-23:59", true)]
    [InlineData("2026-09-02T10:00:00+00:00", true)]
    [InlineData("2026-09-02T10:00:00-00:00", true)]
    [InlineData("2026-09-02T10:00:00+24:00", false)]
    [InlineData("2026-09-02T10:00:00-00:60", false)]
    [InlineData("2026-09-02T10:00:00+0200", false)]
    [InlineData("2026-09-02T10:00:00+02:0", false)]
    [InlineData("2026-09-02T10:00:00+2:00", false)]
    [InlineData("2026-09-02T10:00:00Z ", false)]
    [InlineData(" 2026-09-02T10:00:00Z", false)]
    [InlineData("2026-09-02T10:00:00UTC", false)]
    [InlineData("2026-09-02T24:00:00Z", false)]
    [InlineData("2026-09-02T10:60:00Z", false)]
    [InlineData("26-09-02T10:00:00Z", false)]
    [InlineData("2026-9-2T10:00:00Z", false)]
    [InlineData("2026-09-02T10:00Z", false)]
    [InlineData("+2026-09-02T10:00:00Z", false)]
    [InlineData("-0001-09-02T10:00:00Z", false)]
    public void ZonesAndFieldsAreStrict(string s, bool valid)
    {
        Assert.Equal(valid, Rfc3339.Parse(s) is not null);
    }

    /// <summary>Only ASCII digits are digits: a character of another script never is.</summary>
    [Fact]
    public void OnlyAsciiDigitsCount()
    {
        Assert.Null(Rfc3339.Parse("\u0662\u0660\u0662\u0666-09-02T10:00:00Z"));
        Assert.Null(Rfc3339.Parse("2026-09-02T10:00:00.\uFF15Z"));
        Assert.Null(Rfc3339.Parse("2026-09-02T10:00:00\uFF3A"));
    }

    [Fact]
    public void FormatWritesUtcAndKeepsTicks()
    {
        Assert.Equal("2026-09-02T10:00:00Z", Rfc3339.Format(new DateTimeOffset(2026, 9, 2, 12, 0, 0, TimeSpan.FromHours(2))));
        Assert.Equal("0001-01-01T00:00:00Z", Rfc3339.Format(DateTimeOffset.MinValue));
        Assert.Equal("9999-12-31T23:59:59.9999999Z", Rfc3339.Format(DateTimeOffset.MaxValue));
        Assert.Equal("1970-01-01T00:00:00.0000001Z", Rfc3339.Format(DateTimeOffset.UnixEpoch.AddTicks(1)));
        Assert.Equal("1969-12-31T23:59:59.9999999Z", Rfc3339.Format(DateTimeOffset.UnixEpoch.AddTicks(-1)));
    }

    /// <summary>
    /// The civil-calendar arithmetic (days_from_civil, civil_from_days) agrees
    /// with .NET's proleptic Gregorian calendar over the whole range, and
    /// every instant survives Format and Parse.
    /// </summary>
    [Fact]
    public void CalendarAgreesWithDotNetOverTheWholeRange()
    {
        var step = TimeSpan.FromDays(3) + TimeSpan.FromSeconds(3_677) + TimeSpan.FromTicks(1_234_567);
        var count = 0;
        for (var t = DateTimeOffset.MinValue; t < DateTimeOffset.MaxValue - step; t += step)
        {
            var utc = t.UtcDateTime;
            var fraction = (utc.Ticks % TimeSpan.TicksPerSecond).ToString("D7", CultureInfo.InvariantCulture).TrimEnd('0');
            var expected = utc.ToString("yyyy'-'MM'-'dd'T'HH':'mm':'ss", CultureInfo.InvariantCulture)
                + (fraction.Length > 0 ? "." + fraction : "") + "Z";
            var text = Rfc3339.Format(t);
            if (text != expected || Rfc3339.Parse(text) != t)
            {
                Assert.Fail($"{t:O}: formatted {text}, expected {expected}, parsed back {Rfc3339.Parse(text):O}");
            }
            count++;
        }
        Assert.True(count > 1_000_000, $"only {count} instants checked");
    }

    [Fact]
    public void GoZeroIsAnyInstantOfYearOne()
    {
        Assert.Equal(DateTimeOffset.MinValue, DateTimeOffset.GoZero);
        Assert.True(new DateTimeOffset(1, 12, 31, 23, 59, 59, TimeSpan.Zero).AddTicks(9_999_999).IsGoZero);
        Assert.False(new DateTimeOffset(2, 1, 1, 0, 0, 0, TimeSpan.Zero).IsGoZero);
        // An instant, whatever its offset: 0002-01-01T00:30+01:00 is still year 1 in UTC.
        Assert.True(new DateTimeOffset(2, 1, 1, 0, 30, 0, TimeSpan.FromHours(1)).IsGoZero);
    }

    /// <summary>A required date that is null fails the decoding; an optional one that is null is absent.</summary>
    [Fact]
    public void NullDates()
    {
        Assert.Throws<JsonException>(() => ApiJson.Decode<KnownSender>("""{"address":"a","source":"sent","addedAt":null}"""));
        Assert.Throws<JsonException>(() => ApiJson.Decode<KnownSender>("""{"address":"a","source":"sent","addedAt":1788343200}"""));
        Assert.Null(ApiJson.Decode<OutboxInfo>("""{"state":"queued","attempts":0,"nextAttemptAt":null}""").NextAttemptAt);
        Assert.Null(ApiJson.Decode<OutboxInfo>("""{"state":"queued","attempts":0}""").NextAttemptAt);
    }
}
