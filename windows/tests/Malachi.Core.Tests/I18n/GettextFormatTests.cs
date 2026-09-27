// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/GettextFormatTests.swift. macOS
// converts the gettext patterns to Foundation's syntax; Windows formats them
// as the GTK UI's fmt.Sprintf does, so the Swift cases are kept with the
// result of formatting instead of the converted pattern. The expected
// strings of the Go cases were produced by fmt.Sprintf of Go 1.27; the
// POSIX %N$ form is the one deliberate difference (Go does not know it).

using System;
using System.Globalization;
using Malachi.Core.I18n;
using Xunit;

namespace Malachi.Core.Tests.I18n;

public sealed class GettextFormatTests
{
    // The Swift cases, each formatted with arguments that fit it.
    [Theory]
    [InlineData("%s", new object[] { "x" }, "x")]
    [InlineData("%d", new object[] { 42 }, "42")]
    [InlineData("%1$s and %2$d", new object[] { "a", 2 }, "a and 2")]
    [InlineData("Connected to malachid %s (pid %d)", new object[] { "1.2", 42 }, "Connected to malachid 1.2 (pid 42)")]
    [InlineData("Protocol mismatch: UI %d, backend %d", new object[] { 1, 2 }, "Protocol mismatch: UI 1, backend 2")]
    [InlineData("%.1f MiB", new object[] { 1.5 }, "1.5 MiB")]
    [InlineData("%.0f KiB", new object[] { 12.6 }, "13 KiB")]
    [InlineData("%f", new object[] { 1.5 }, "1.500000")]
    [InlineData("100%%", new object[0], "100%")]
    [InlineData("%@", new object[] { "x" }, "%!@(string=x)")]
    [InlineData("%ld", new object[] { 5 }, "%!l(int=5)d")]
    [InlineData("%5d|%-3s", new object[] { 42, "x" }, "   42|x  ")]
    [InlineData("no directives", new object[0], "no directives")]
    [InlineData("trailing %", new object[0], "trailing %!(NOVERB)")]
    [InlineData("", new object[0], "")]
    public void Converts(string src, object?[] args, string want)
    {
        Assert.Equal(want, GettextFormat.Format(src, args));
    }

    // Swift: converting twice is harmless. Windows never converts: a pattern
    // is formatted once, the directive check a catalogue applies depends on
    // the pattern alone (so a string always passes against itself), and
    // formatting the Swift mix of directives gives the same text every time.
    [Fact]
    public void IsIdempotent()
    {
        const string Pattern = "%s %d %1$s %2$d %.1f %% %@ %ld";
        Assert.True(GettextFormat.SameDirectives(Pattern, Pattern));
        var once = GettextFormat.Format(Pattern, "x", 7, 1.5);
        Assert.Equal("x 7 x 7 1.5 % %!@(MISSING) %!l(MISSING)d", once);
        Assert.Equal(once, GettextFormat.Format(Pattern, "x", 7, 1.5));
        Assert.Equal(["1:s", "2:d", "1:s", "2:d", "3:f", "4:@", "5:l"], GettextFormat.Directives(Pattern));
    }

    [Fact]
    public void ConvertedPatternsFormat()
    {
        Assert.Equal("Connected to malachid 1.2 (pid 42)", GettextFormat.Format("Connected to malachid %s (pid %d)", "1.2", 42));
        Assert.Equal("x then 7", GettextFormat.Format("%2$s then %1$d", 7, "x"));
    }

    // %f rounds the exact binary value half to even, as Go's strconv does
    // (12.5 is 12, 0.35 is 0.34999… and becomes 0.3).
    [Theory]
    [InlineData("%.0f", 12.5, "12")]
    [InlineData("%.0f", 13.5, "14")]
    [InlineData("%.0f", 0.5, "0")]
    [InlineData("%.0f", 1.5, "2")]
    [InlineData("%.0f", -0.5, "-0")]
    [InlineData("%.2f", 0.125, "0.12")]
    [InlineData("%.2f", 0.375, "0.38")]
    [InlineData("%.1f", 0.05, "0.1")]
    [InlineData("%.1f", 0.25, "0.2")]
    [InlineData("%.1f", 0.35, "0.3")]
    [InlineData("%.1f", 0.95, "0.9")]
    [InlineData("%.1f", 0.15, "0.1")]
    [InlineData("%.1f", 1048575.0 / 1048576.0, "1.0")]
    [InlineData("%.1f MiB", 3.0, "3.0 MiB")]
    [InlineData("%.0f KiB", 2.0, "2 KiB")]
    [InlineData("%.0f KiB", 1535.0 / 1024.0, "1 KiB")]
    [InlineData("%.0f KiB", 1536.0 / 1024.0, "2 KiB")]
    [InlineData("%.0f KiB", 2560.0 / 1024.0, "2 KiB")]
    [InlineData("%f", -0.0, "-0.000000")]
    [InlineData("%.1f", -0.0, "-0.0")]
    [InlineData("%f", double.PositiveInfinity, "+Inf")]
    [InlineData("%f", double.NegativeInfinity, "-Inf")]
    [InlineData("%f", double.NaN, "NaN")]
    [InlineData("%+f", double.NaN, "+NaN")]
    [InlineData("% f", double.NaN, " NaN")]
    [InlineData("%08.2f", -3.14159, "-0003.14")]
    [InlineData("%+.2f", 3.14159, "+3.14")]
    [InlineData("% .2f", 3.14159, " 3.14")]
    [InlineData("%-8.2f|", 3.14159, "3.14    |")]
    [InlineData("%8.2f|", 3.14159, "    3.14|")]
    [InlineData("%08f", double.PositiveInfinity, "    +Inf")]
    [InlineData("%#.0f", 3.0, "3.")]
    [InlineData("%.3f", 1e21, "1000000000000000000000.000")]
    [InlineData("%.20f", 0.1, "0.10000000000000000555")]
    [InlineData("%.30f", 1e-10, "0.000000000100000000000000003643")]
    [InlineData("%.0f", 1e22, "10000000000000000000000")]
    [InlineData("%.1f", double.Epsilon, "0.0")]
    [InlineData("%.1f%%", 99.5, "99.5%")]
    public void FloatsRoundAsGoDoes(string pattern, double value, string want)
    {
        Assert.Equal(want, GettextFormat.Format(pattern, value));
    }

    [Fact]
    public void LargestFloatIsExact()
    {
        Assert.Equal(
            "179769313486231570814527423731704356798070567525844996598917476803157260780028538760589558632766878171540458953514382464234321326889464182768467546703537516986049910576551282076245490090389328944075868508455133942304583236903222948165808559332123348274797826204144723168738177180919299881250404026184124858368",
            GettextFormat.Format("%.0f", double.MaxValue));
        Assert.Equal("0.100000", GettextFormat.Format("%f", 0.1f));
        Assert.Equal("2.2", GettextFormat.Format("%.1f", 2.25f));
        Assert.Equal("  2.2|2.25  |-002.2", GettextFormat.Format("%5.1f|%-6.2f|%06.1f", 2.25, 2.25, -2.25));
    }

    [Theory]
    [InlineData("%d", new object[] { -42 }, "-42")]
    [InlineData("%+d", new object[] { 42 }, "+42")]
    [InlineData("% d", new object[] { 42 }, " 42")]
    [InlineData("%05d", new object[] { -42 }, "-0042")]
    [InlineData("%-5d|", new object[] { -42 }, "-42  |")]
    [InlineData("%.3d", new object[] { 7 }, "007")]
    [InlineData("%.3d", new object[] { -7 }, "-007")]
    [InlineData("%8.3d|", new object[] { 7 }, "     007|")]
    [InlineData("%08.3d|", new object[] { 7 }, "     007|")]
    [InlineData("%.0d|", new object[] { 0 }, "|")]
    [InlineData("%5.0d|", new object[] { 0 }, "     |")]
    [InlineData("%d", new object[] { long.MinValue }, "-9223372036854775808")]
    [InlineData("%d", new object[] { ulong.MaxValue }, "18446744073709551615")]
    [InlineData("%d", new object[] { (sbyte)-5 }, "-5")]
    [InlineData("%d", new object[] { (byte)200 }, "200")]
    [InlineData("%d", new object[] { (short)-300 }, "-300")]
    [InlineData("%d", new object[] { (ushort)60000 }, "60000")]
    [InlineData("%d", new object[] { 4000000000u }, "4000000000")]
    [InlineData("%d", new object[] { 123456789012L }, "123456789012")]
    [InlineData("%-05d|", new object[] { 42 }, "42   |")]
    [InlineData("%0-5d|", new object[] { 42 }, "42   |")]
    [InlineData("%+05d|", new object[] { 42 }, "+0042|")]
    [InlineData("% 05d|", new object[] { 42 }, " 0042|")]
    [InlineData("%+ d|", new object[] { 42 }, "+42|")]
    [InlineData("%d", new object[] { 'a' }, "97")]
    public void IntegersAsGoDoes(string pattern, object?[] args, string want)
    {
        Assert.Equal(want, GettextFormat.Format(pattern, args));
    }

    // A verb that does not fit its argument, and verbs other than s, d, f,
    // v and %, are reported in place as Go reports a bad verb.
    [Theory]
    [InlineData("%s", new object[] { 5 }, "%!s(int=5)")]
    [InlineData("%s", new object[] { 1.5 }, "%!s(float64=1.5)")]
    [InlineData("%s", new object[] { true }, "%!s(bool=true)")]
    [InlineData("%s", new object?[] { null }, "%!s(<nil>)")]
    [InlineData("%s", new object[] { 5L }, "%!s(int64=5)")]
    [InlineData("%s", new object[] { (byte)5 }, "%!s(uint8=5)")]
    [InlineData("%s", new object[] { 1.5f }, "%!s(float32=1.5)")]
    [InlineData("%s", new object[] { 'a' }, "%!s(int32=97)")]
    [InlineData("%d", new object[] { "abc" }, "%!d(string=abc)")]
    [InlineData("%d", new object[] { 1.5 }, "%!d(float64=1.5)")]
    [InlineData("%d", new object[] { true }, "%!d(bool=true)")]
    [InlineData("%d", new object?[] { null }, "%!d(<nil>)")]
    [InlineData("%d", new object[] { 1.5f }, "%!d(float32=1.5)")]
    [InlineData("%d", new object[] { 1e21 }, "%!d(float64=1e+21)")]
    [InlineData("%d", new object[] { 1e20 }, "%!d(float64=1e+20)")]
    [InlineData("%d", new object[] { 123456.0 }, "%!d(float64=123456)")]
    [InlineData("%d", new object[] { 1234567.0 }, "%!d(float64=1.234567e+06)")]
    [InlineData("%d", new object[] { 0.0001 }, "%!d(float64=0.0001)")]
    [InlineData("%d", new object[] { 0.00001 }, "%!d(float64=1e-05)")]
    [InlineData("%d", new object[] { 0.1 }, "%!d(float64=0.1)")]
    [InlineData("%d", new object[] { 100.0 }, "%!d(float64=100)")]
    [InlineData("%d", new object[] { 0.1f }, "%!d(float32=0.1)")]
    [InlineData("%d", new object[] { 16777216f }, "%!d(float32=1.6777216e+07)")]
    [InlineData("%d", new object[] { double.PositiveInfinity }, "%!d(float64=+Inf)")]
    [InlineData("%d", new object[] { double.NaN }, "%!d(float64=NaN)")]
    [InlineData("%d", new object[] { -0.0 }, "%!d(float64=-0)")]
    [InlineData("%f", new object[] { 5 }, "%!f(int=5)")]
    [InlineData("%f", new object[] { "x" }, "%!f(string=x)")]
    [InlineData("%f", new object?[] { null }, "%!f(<nil>)")]
    [InlineData("%x", new object[] { 255 }, "%!x(int=255)")]
    [InlineData("%q", new object[] { "x" }, "%!q(string=x)")]
    [InlineData("%v", new object?[] { null }, "<nil>")]
    [InlineData("%v", new object[] { 5 }, "5")]
    [InlineData("%v", new object[] { "s" }, "s")]
    [InlineData("%v", new object[] { 1.5 }, "1.5")]
    [InlineData("%v", new object[] { true }, "true")]
    [InlineData("%v", new object[] { 1e21 }, "1e+21")]
    [InlineData("%v", new object[] { 1.0 }, "1")]
    [InlineData("%v", new object[] { -2.5e-7 }, "-2.5e-07")]
    [InlineData("%v", new object[] { 123456789.0 }, "1.23456789e+08")]
    public void BadVerbsAsGoReportsThem(string pattern, object?[] args, string want)
    {
        Assert.Equal(want, GettextFormat.Format(pattern, args));
    }

    [Theory]
    [InlineData("%d", new object[0], "%!d(MISSING)")]
    [InlineData("%s %s", new object[] { "a" }, "a %!s(MISSING)")]
    [InlineData("%d%s", new object[] { 1 }, "1%!s(MISSING)")]
    [InlineData("%s", new object[] { "a", "b" }, "a%!(EXTRA string=b)")]
    [InlineData("%s", new object?[] { "a", 1, 2.5, true, null }, "a%!(EXTRA int=1, float64=2.5, bool=true, <nil>)")]
    [InlineData("x", new object[] { 1 }, "x%!(EXTRA int=1)")]
    [InlineData("%%", new object[] { 1 }, "%%!(EXTRA int=1)")]
    [InlineData("%d %d", new object[] { 1, 2, 3 }, "1 2%!(EXTRA int=3)")]
    [InlineData("%5%|", new object[0], "%|")]
    [InlineData("%-5%|", new object[0], "%|")]
    public void MissingAndExtraArguments(string pattern, object?[] args, string want)
    {
        Assert.Equal(want, GettextFormat.Format(pattern, args));
    }

    // Go's explicit indexes, and the POSIX form %N$ gettext translators write.
    [Theory]
    [InlineData("%[2]s %[1]s", new object[] { "a", "b" }, "b a")]
    [InlineData("%[2]s %s", new object[] { "a", "b", "c" }, "b c")]
    [InlineData("%[3]s", new object[] { "a" }, "%!s(BADINDEX)")]
    [InlineData("%[0]s", new object[] { "a" }, "%!s(BADINDEX)")]
    [InlineData("%[x]s", new object[] { "a" }, "%!s(BADINDEX)")]
    [InlineData("%[1]s", new object[] { "a", "b" }, "a")]
    [InlineData("%[2]d %d", new object[] { 1, 2 }, "2 %!d(MISSING)")]
    [InlineData("%[1]*d|", new object[] { 5, 42 }, "   42|")]
    [InlineData("%[2]*[1]d|", new object[] { 42, 6 }, "    42|")]
    [InlineData("%[1]d %[1]d", new object[] { 3 }, "3 3")]
    [InlineData("%[2]d", new object[] { 1, 2 }, "2")]
    [InlineData("%[1]s %s %s", new object[] { "a", "b" }, "a b %!s(MISSING)")]
    [InlineData("%[3]*.[2]*[1]f|", new object[] { 12.0, 2, 6 }, " 12.00|")]
    [InlineData("%*d|", new object[] { 5, 42 }, "   42|")]
    [InlineData("%-*d|", new object[] { 5, 42 }, "42   |")]
    [InlineData("%*d|", new object[] { -5, 42 }, "42   |")]
    [InlineData("%.*f", new object[] { 2, 3.14159 }, "3.14")]
    [InlineData("%.*f", new object[] { -2, 3.14159 }, "%!(BADPREC)3.141590")]
    [InlineData("%*d", new object[] { "x", 42 }, "%!(BADWIDTH)42")]
    [InlineData("%.*d", new object[] { "x", 42 }, "%!(BADPREC)42")]
    [InlineData("%1$s", new object[] { "a" }, "a")]
    [InlineData("%2$d then %1$s", new object[] { "x", 7 }, "7 then x")]
    [InlineData("%2$s %1$s", new object[] { "a", "b" }, "b a")]
    [InlineData("%2$s", new object[] { "a", "b" }, "b")]
    [InlineData("%3$s", new object[] { "a" }, "%!s(BADINDEX)")]
    [InlineData("%0$s", new object[] { "a" }, "%!s(BADINDEX)")]
    [InlineData("%1$5d|", new object[] { 42 }, "   42|")]
    [InlineData("%1$-5s|", new object[] { "x" }, "x    |")]
    public void ArgumentIndexes(string pattern, object?[] args, string want)
    {
        Assert.Equal(want, GettextFormat.Format(pattern, args));
    }

    // Width and precision count code points, as Go counts runes.
    [Theory]
    [InlineData("%.s|", "abc", "|")]
    [InlineData("%.2s|", "abc", "ab|")]
    [InlineData("%.2s|", "čšžý", "čš|")]
    [InlineData("%5s|", "čš", "   čš|")]
    [InlineData("%-5s|", "čš", "čš   |")]
    [InlineData("%05s|", "ab", "000ab|")]
    [InlineData("%5s|", "😀", "    😀|")]
    [InlineData("%.1s|", "😀x", "😀|")]
    public void StringsPadByCodePoints(string pattern, string value, string want)
    {
        Assert.Equal(want, GettextFormat.Format(pattern, value));
    }

    [Theory]
    [InlineData("%!", new object[0], "%!!(MISSING)")]
    [InlineData("%-", new object[0], "%!(NOVERB)")]
    [InlineData("%5", new object[] { 1 }, "%!(NOVERB)%!(EXTRA int=1)")]
    [InlineData("%.", new object[] { 1 }, "%!.(int=1)")]
    [InlineData("%.5", new object[] { 1 }, "%!(NOVERB)%!(EXTRA int=1)")]
    [InlineData("%[1]", new object[] { 1 }, "%!(NOVERB)")]
    [InlineData("%é", new object[] { 1 }, "%!é(int=1)")]
    [InlineData("%s%", new object[] { "a" }, "a%!(NOVERB)")]
    public void MalformedPatterns(string pattern, object?[] args, string want)
    {
        Assert.Equal(want, GettextFormat.Format(pattern, args));
    }

    // Go and Swift print 1.5 in every locale; so does Windows.
    [Fact]
    public void NumbersIgnoreTheCulture()
    {
        var saved = CultureInfo.CurrentCulture;
        try
        {
            CultureInfo.CurrentCulture = CultureInfo.GetCultureInfo("cs-CZ");
            Assert.Equal("1.5 MiB", GettextFormat.Format("%.1f MiB", 1.5));
            Assert.Equal("1234567", GettextFormat.Format("%d", 1234567));
            Assert.Equal("%!d(float64=1.5)", GettextFormat.Format("%d", 1.5));
        }
        finally
        {
            CultureInfo.CurrentCulture = saved;
        }
    }

    // A value's own text through %s; an exception from ToString is reported
    // in place, as Go reports a panicking String method.
    [Fact]
    public void ObjectsFormatThroughTheirText()
    {
        Assert.Equal("Dark", GettextFormat.Format("%s", Scheme.Dark));
        Assert.Equal("2", GettextFormat.Format("%d", Scheme.Dark));
        Assert.Equal("[id]", GettextFormat.Format("%s", new Named("[id]")));
        Assert.Equal("%!d(Named=[id])", GettextFormat.Format("%d", new Named("[id]")));
        Assert.Equal("%!s(PANIC=String method: boom)", GettextFormat.Format("%s", new Throwing()));
    }

    // Formatting is total: whatever the pattern and arguments, it returns.
    [Fact]
    public void NeverThrows()
    {
        const string Alphabet = "%%%%sdfvx0123456789.*[]$#+- éabc";
        object?[] values = [null, 0, -1, 7, long.MinValue, ulong.MaxValue, 1.5, double.NaN, 2.5f, "s", "", true, 'c', new Throwing()];
        var random = new Random(20260927);
        for (var round = 0; round < 20_000; round++)
        {
            var chars = new char[random.Next(0, 12)];
            for (var i = 0; i < chars.Length; i++)
            {
                chars[i] = Alphabet[random.Next(Alphabet.Length)];
            }
            var args = new object?[random.Next(0, 4)];
            for (var i = 0; i < args.Length; i++)
            {
                args[i] = values[random.Next(values.Length)];
            }
            var pattern = new string(chars);
            _ = GettextFormat.Format(pattern, args);
            _ = GettextFormat.Directives(pattern);
        }
        Assert.Equal("%!s(<nil>)", GettextFormat.Format("%s", [null]));
        Assert.Equal("x", GettextFormat.Format("x", null));
    }

    [Fact]
    public void DirectivesListWhatAPatternConsumes()
    {
        Assert.Equal(["1:s", "2:d"], GettextFormat.Directives("%s of %d"));
        Assert.Equal(["2:d", "1:s"], GettextFormat.Directives("%2$d z %1$s"));
        Assert.Equal(["2:s", "3:s"], GettextFormat.Directives("%[2]s %s"));
        Assert.Equal(["1:*", "2:d"], GettextFormat.Directives("%*d"));
        Assert.Equal(["1:f"], GettextFormat.Directives("%.1f MiB"));
        Assert.Empty(GettextFormat.Directives("100%%"));
        Assert.Empty(GettextFormat.Directives("no directives"));
        Assert.Equal(["NOVERB"], GettextFormat.Directives("trailing %"));
        Assert.Equal(["BADINDEX:s"], GettextFormat.Directives("%[0]s"));
        Assert.Equal(["BADINDEX:s"], GettextFormat.Directives("%0$s"));
    }

    // What the catalogue compares a translation with its msgid by.
    [Theory]
    [InlineData("%s of %d", "%2$d z %1$s", true)]
    [InlineData("%s of %d", "%[2]d z %[1]s", true)]
    [InlineData("%s of %d", "%d z %s", false)]
    [InlineData("%s of %d", "%s z", false)]
    [InlineData("%.1f MiB", "%.2f MiB", true)]
    [InlineData("%5d", "%d", true)]
    [InlineData("100%%", "100 %%", true)]
    [InlineData("Syncing %s… %d %%", "Synchronizuje se %s… %d %%", true)]
    [InlineData("%s", "%s %s", false)]
    [InlineData("%[1]s %[1]s", "%s", true)]
    [InlineData("%d", "%s", false)]
    [InlineData("Zoom", "100 %", false)]
    public void SameDirectives(string msgid, string translation, bool same)
    {
        Assert.Equal(same, GettextFormat.SameDirectives(msgid, translation));
    }

    private enum Scheme
    {
        Auto,
        Light,
        Dark,
    }

    private sealed class Named(string text)
    {
        public override string ToString() => text;
    }

    private sealed class Throwing
    {
        public override string ToString() => throw new InvalidOperationException("boom");
    }
}
