// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/StrftimeTests.swift. The Swift
// cases map to ICU patterns, these to .NET custom formats: the same fields
// and quoted literals, a quote inside a literal escaped with a backslash
// (ICU doubles it; .NET would drop it), and a single field marked with %.
// The rendering cases are Swift's unchanged, plus the traps .NET adds.

using System;
using System.Globalization;
using Malachi.Core.I18n;
using Xunit;

namespace Malachi.Core.Tests.I18n;

public sealed class StrftimeTests
{
    // 2026-09-02 15:04 in Prague, a Wednesday.
    private static readonly TimeZoneInfo Prague = TimeZoneInfo.FindSystemTimeZoneById("Europe/Prague");
    private static readonly DateTimeOffset Fixed = new(2026, 9, 2, 15, 4, 0, TimeSpan.FromHours(2));

    [Theory]
    [InlineData("%H:%M", "HH':'mm")]
    [InlineData("%-d %b", "d' 'MMM")]
    [InlineData("%Y-%m-%d", "yyyy'-'MM'-'dd")]
    [InlineData("%a, %-d %b %Y at %H:%M", "ddd', 'd' 'MMM' 'yyyy' at 'HH':'mm")]
    [InlineData("%a %-d. %-m. %Y v %H:%M", "ddd' 'd'. 'M'. 'yyyy' v 'HH':'mm")]
    [InlineData("%A %B %e %I:%M %p", "dddd' 'MMMM' 'd' 'hh':'mm' 'tt")]
    [InlineData("%-I%p", "htt")]
    [InlineData("100%% at %-H", "'100% at 'H")]
    [InlineData("o'clock %H", @"'o\'clock 'HH")]
    [InlineData("%y %S %-S %-M", "yy' 'ss' 's' 'm")]
    [InlineData("%Q", "'%Q'")]
    [InlineData("plain", "'plain'")]
    [InlineData("", "")]
    // .NET: one letter alone would be a standard format.
    [InlineData("%-d", "%d")]
    [InlineData("%-H", "%H")]
    [InlineData("%e", "%d")]
    [InlineData("%-m", "%M")]
    [InlineData("%y", "yy")]
    [InlineData(@"back\slash %H", @"'back\\slash 'HH")]
    [InlineData("%", "'%'")]
    [InlineData("%-", "'%-'")]
    [InlineData("%Z %z", "zzz' 'zzz")]
    public void ToDateFormat(string strftime, string want)
    {
        Assert.Equal(want, Strftime.ToDateFormat(strftime));
    }

    // The four msgids of ui/internal/widget/format.go, in English.
    [Fact]
    public void EnglishMsgids()
    {
        Assert.Equal("15:04", Render("%H:%M", "en-US"));
        Assert.Equal("2 Sep", Render("%-d %b", "en-US"));
        Assert.Equal("2026-09-02", Render("%Y-%m-%d", "en-US"));
        Assert.Equal("Wed, 2 Sep 2026 at 15:04", Render("%a, %-d %b %Y at %H:%M", "en-US"));
    }

    // The Czech translations of po/cs.po.
    [Fact]
    public void CzechMsgids()
    {
        Assert.Equal("15:04", Render("%H:%M", "cs-CZ"));
        Assert.Equal("2. 9.", Render("%-d. %-m.", "cs-CZ"));
        Assert.Equal("2. 9. 2026", Render("%-d. %-m. %Y", "cs-CZ"));
        Assert.Equal("st 2. 9. 2026 v 15:04", Render("%a %-d. %-m. %Y v %H:%M", "cs-CZ"));
    }

    [Fact]
    public void LiteralsAreNeverFields()
    {
        // "at" and "v" must not be read as pattern letters.
        Assert.Equal("at 15", Render("at %H", "en-US"));
        Assert.Equal("v 15", Render("v %H", "cs-CZ"));
        Assert.Equal("100%", Render("100%%", "en-US"));
    }

    [Fact]
    public void TwelveHourClock()
    {
        Assert.Equal("03:04 PM", Render("%I:%M %p", "en-US"));
        Assert.Equal("3 PM", Render("%-I %p", "en-US"));
        Assert.Equal("02/09/26", Render("%d/%m/%y", "en-US"));
    }

    // .NET reads a one-letter format as a standard one ("d" is the short
    // date, "H" throws); the % keeps it a field.
    [Fact]
    public void SingleFieldIsNotAStandardFormat()
    {
        Assert.Equal("2", Render("%-d", "en-US"));
        Assert.Equal("15", Render("%-H", "en-US"));
        Assert.Equal("9", Render("%-m", "en-US"));
        Assert.Equal("4", Render("%-M", "en-US"));
        Assert.Equal("26", Render("%y", "en-US"));
    }

    // The ICU form '' would end the quoted run in .NET and lose the quote.
    [Fact]
    public void QuotesAndBackslashesSurvive()
    {
        Assert.Equal("o'clock 15", Render("o'clock %H", "en-US"));
        Assert.Equal("15 o'clock, it's", Render("%H o'clock, it's", "en-US"));
        Assert.Equal(@"back\slash 15", Render(@"back\slash %H", "en-US"));
        Assert.Equal("\"15\"", Render("\"%H\"", "en-US"));
    }

    // .NET renders an empty format as the "G" standard format.
    [Fact]
    public void EmptyFormatRendersNothing()
    {
        Assert.Equal("", Render("", "en-US"));
    }

    // ':' and '/' are literal, never the culture's separators.
    [Fact]
    public void SeparatorsAreLiteral()
    {
        Assert.Equal("15:04", Render("%H:%M", "fi-FI"));
        Assert.Equal("02/09/26", Render("%d/%m/%y", "de-DE"));
    }

    // %b and %B are GLib's month names "with day": the genitive in Czech,
    // with or without a day number in the same format.
    [Fact]
    public void MonthNamesAreTheFormWithADay()
    {
        var january = new DateTimeOffset(2026, 1, 2, 15, 4, 0, TimeSpan.FromHours(1));
        var cs = CultureInfo.GetCultureInfo("cs-CZ");
        Assert.Equal("2. ledna", Strftime.Format(january, "%-d. %B", cs, Prague));
        Assert.Equal("ledna 2026", Strftime.Format(january, "%B %Y", cs, Prague));
        Assert.Equal(cs.DateTimeFormat.AbbreviatedMonthGenitiveNames[0], Strftime.Format(january, "%b", cs, Prague));
        Assert.Equal("září", Strftime.Format(Fixed, "%B", cs, Prague));
        Assert.Equal("January 2026", Strftime.Format(january, "%B %Y", CultureInfo.GetCultureInfo("en-US"), Prague));
        Assert.Equal("stycznia 2026", Strftime.Format(january, "%B %Y", CultureInfo.GetCultureInfo("pl-PL"), Prague));
    }

    // The instant is shown in the time zone asked for.
    [Fact]
    public void RendersInTheTimeZone()
    {
        Assert.Equal("13:04", Strftime.Format(Fixed, "%H:%M", CultureInfo.InvariantCulture, TimeZoneInfo.Utc));
        Assert.Equal("+02:00", Strftime.Format(Fixed, "%z", CultureInfo.InvariantCulture, Prague));
    }

    private static string Render(string strftime, string locale) =>
        Strftime.Format(Fixed, strftime, CultureInfo.GetCultureInfo(locale), Prague);
}
