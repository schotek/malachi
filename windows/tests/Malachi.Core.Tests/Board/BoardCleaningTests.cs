// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/BoardModelTests.swift
// (BoardCleaningTests); GTK: ui/internal/board/model_test.go (TestCleanLine,
// TestCleanLineCaps, TestCleanLineBigInput,
// TestCleaningIsBoundedForDroppedInput, TestTheCutNeverBreaksACharacter,
// TestCleanBlock, TestCleanBlockBigInput). A lone surrogate is the C#
// counterpart of Go's invalid UTF-8.

using System;
using System.Diagnostics;
using System.Linq;
using System.Text;
using Xunit;
using static Malachi.Core.Board.Board;
using F = Malachi.Core.Tests.Board.BoardFixture;

namespace Malachi.Core.Tests.Board;

public sealed class BoardCleaningTests
{
    private static int Utf8(string s) => Encoding.UTF8.GetByteCount(s);

    [Theory]
    [InlineData("plain", "plain")]
    [InlineData("  a   b  ", "a b")]
    [InlineData("a\tb", "a b")]
    [InlineData("a\r\nb", "a b")]
    [InlineData("a\nb\rc", "a b c")]
    [InlineData("a\u2028b\u2029c", "a b c")]
    [InlineData("a\u0085b", "a b")]
    [InlineData("a\0b", "ab")]
    [InlineData("a\u202Eb", "ab")] // right-to-left override
    [InlineData("\u202Ea\u202C", "a")]
    [InlineData("a\u200Bb\u200Dc\uFEFFd", "abcd")] // zero width, joiner, BOM
    [InlineData("a\u2066b\u2069", "ab")] // isolates
    [InlineData("a\u0007b\u001Bc", "abc")] // bell, escape
    [InlineData("\u202E\u200B\0\uFEFF", "")] // only format characters
    [InlineData("", "")]
    [InlineData("   \n\t ", "")]
    [InlineData("čeština ✓ 🙂", "čeština ✓ 🙂")]
    [InlineData("a\uFFFDb", "ab")] // the replacement character
    public void CleanLineTable(string input, string want) => Assert.Equal(want, CleanLine(input, 100));

    [Fact]
    public void CleanLineOfNothingAndOfInvalidText()
    {
        Assert.Equal("", CleanLine("abc", 0));
        Assert.Equal("", CleanLine("abc", -1));
        Assert.Equal("", CleanLine(null, 100));
        Assert.Equal("", CleanBlock(null, 100));
        // Lone surrogates: Go's invalid UTF-8 (built here: a theory's data must serialise).
        var high = ((char)0xD800).ToString();
        var low = ((char)0xDC00).ToString();
        Assert.Equal("abc", CleanLine("a" + high + "b" + low + "c", 100));
        Assert.Equal("a\nb", CleanBlock("a" + low + "\nb" + high, 100));
    }

    [Fact]
    public void CleanLineCaps()
    {
        Assert.Equal("abc", CleanLine("abcdef", 3));
        Assert.Equal("ab", CleanLine("ab cd", 3)); // the space at the cut goes
        Assert.Equal("é", CleanLine("éé", 3)); // a scalar is never cut
        Assert.Equal("€€", CleanLine("€€€", 8));
        Assert.Equal("🙂", CleanLine("🙂🙂", 7));
        Assert.Equal("", CleanLine("🙂", 3));
    }

    [Fact]
    public void CleanLineBigInput()
    {
        var mb = new string('é', 500_000); // 1 MB
        foreach (var max in new[] { 1, 2, 99, 100, 300 })
        {
            var output = CleanLine(mb, max);
            Assert.Equal(max - (max % 2), Utf8(output));
            Assert.All(output, c => Assert.Equal('é', c));
        }
        var three = new string('€', 400_000);
        Assert.Equal(99, Utf8(CleanLine(three, 100))); // 33 scalars of 3 bytes, cut on the boundary
        // A megabyte of nothing but format characters and whitespace.
        var nothing = string.Concat(Enumerable.Repeat("\u202E \u200B\n", 250_000));
        Assert.Equal("", CleanLine(nothing, 300));
        Assert.Equal("", CleanBlock(nothing, 300));
        // A megabyte of spaces between two words: the scan gives up long
        // before the second word, so the text ends with the first.
        var gap = "a" + new string(' ', 1_000_000) + "b";
        Assert.Equal("a", CleanLine(gap, 300));
        Assert.Equal("a", CleanBlock(gap, 300));
        // A shorter gap is read through.
        var shortGap = "a" + new string(' ', 1000) + "b";
        Assert.Equal("a b", CleanLine(shortGap, 300));
    }

    /// <summary>
    /// A megabyte of what the cleaners drop costs no more than a short
    /// input: they read a bounded number of characters (8 × the cap).
    /// </summary>
    [Fact]
    public void CleaningIsBoundedForDroppedInput()
    {
        var zw = new string('\u200B', 1_000_000);
        var spaces = new string(' ', 1_000_000);
        var clock = Stopwatch.StartNew();
        for (var i = 0; i < 20; i++)
        {
            Assert.Equal("", CleanLine(zw, 300));
            Assert.Equal("", CleanBlock(zw, 4000));
            Assert.Equal("", CleanLine(spaces, 300));
            Assert.Equal("", CleanBlock(spaces, 4000));
        }
        // Eighty full scans of a megabyte would take seconds even optimised.
        Assert.True(clock.Elapsed.TotalSeconds < 2, clock.Elapsed.ToString());
        // Past the budget nothing more is read, even what would be kept.
        Assert.Equal("", CleanLine(new string('\u200B', 24) + "x", 3));
        Assert.Equal("x", CleanLine(new string('\u200B', 23) + "x", 3));
        Assert.Equal("", CleanBlock(new string('\u200B', 24) + "x", 3));

        // A whole view over such a case.
        var c = F.Mk("c1", subject: zw, snippet: spaces, messages: [new CaseMessage { From = zw, Date = F.Ago(1), Text = spaces }]);
        var v = F.View([c, F.Mk("c2", hours: 2)]);
        var r = v.Sections[0].Rows[0];
        Assert.True(r.Id == F.Id("c1") && r.Title == "(No subject)" && r.Snippet == "" && r.Person == "P c1");
        var d = v.Detail!;
        Assert.True(d.Id == F.Id("c1") && d.Title == "(No subject)" && d.Subject == "");
        Assert.Equal([""], d.Messages.Select(m => m.From));
        Assert.Equal([""], d.Messages.Select(m => m.Text));
        Assert.Equal(["c1", "c2"], F.Ids(v.Sections[0].Rows));
    }

    [Fact]
    public void TheCutNeverBreaksACharacter()
    {
        const string e = "e\u0301"; // é, decomposed: 3 bytes
        Assert.Equal("ab", CleanLine("ab" + e, 3)); // the e would lose its accent
        Assert.Equal("ab", CleanLine("ab" + e, 4));
        Assert.Equal("ab" + e, CleanLine("ab" + e, 5)); // it fits
        Assert.Equal("ab", CleanLine("ab " + e + "cd", 4)); // the space before goes too
        Assert.Equal("ab", CleanBlock("ab\n" + e, 4));
        Assert.Equal("ab", CleanBlock("ab" + e, 3));
        // Several marks on one letter: the cluster goes whole.
        Assert.Equal("a", CleanLine("a" + "e\u0301\u0302\u0303", 5));
        var cz = char.ConvertFromUtf32(0x1F1E8) + char.ConvertFromUtf32(0x1F1FF); // a flag: two regional indicators, 8 bytes
        Assert.Equal(cz, CleanLine(cz + cz, 12)); // not a lone indicator
        Assert.Equal(cz + cz, CleanLine(cz + cz, 16));
        Assert.Equal("", CleanLine(cz, 4));
        Assert.Equal(cz, CleanBlock(cz + cz, 15));
        // Not cut at all: a trailing cluster stays as it is.
        var lone = "x" + char.ConvertFromUtf32(0x1F1E8);
        Assert.Equal(lone, CleanLine(lone, 100));
        // Format characters (ZWJ, ZWNJ) are still dropped.
        Assert.Equal("abc", CleanLine("a\u200Db\u200Cc", 100));
        // The rest of Go's rules: an emoji modifier, a Hangul syllable of
        // conjoining jamo, the Thai AM after its letter.
        var thumb = char.ConvertFromUtf32(0x1F44D) + char.ConvertFromUtf32(0x1F3FD);
        Assert.Equal("a", CleanLine("a" + thumb, 8));
        Assert.Equal("a", CleanLine("a\u1100\u1161", 4));
        Assert.Equal("a", CleanLine("aกำ", 6));
    }

    [Theory]
    [InlineData("plain", "plain")]
    [InlineData("a\nb", "a\nb")]
    [InlineData("a\r\nb", "a\nb")]
    [InlineData("a\rb", "a\nb")]
    [InlineData("a\r\rb", "a\n\nb")]
    [InlineData("a\n\rb", "a\n\nb")]
    [InlineData("a\u2028b", "a\nb")]
    [InlineData("a\u2029b", "a\nb")]
    [InlineData("a\u0085b\u000Bc\u000Cd", "a\nb\nc\nd")]
    [InlineData("a\n\n\n\n\nb", "a\n\nb")] // at most one empty line
    [InlineData("a  \nb", "a\nb")] // no spaces at the end of a line
    [InlineData("a\n  b", "a\n  b")] // indentation stays
    [InlineData("a\tb", "a b")]
    [InlineData("\n\n a\n\n", "a")]
    [InlineData("a\0b", "ab")]
    [InlineData("a\u202Eb\u200Bc", "abc")]
    [InlineData("\u202E\u200B\0", "")]
    [InlineData("", "")]
    public void CleanBlockTable(string input, string want) => Assert.Equal(want, CleanBlock(input, 100));

    [Fact]
    public void CleanBlockCaps()
    {
        Assert.Equal("", CleanBlock("abc", 0));
        Assert.Equal("ab", CleanBlock("ab\n\ncd", 3));
        Assert.Equal("é", CleanBlock("éé", 3));
    }

    [Fact]
    public void CleanBlockBigInput()
    {
        var mb = new string('é', 500_000);
        Assert.Equal(4000, Utf8(CleanBlock(mb, 4000)));
        var lines = string.Concat(Enumerable.Repeat("line\r\n", 200_000));
        var blk = CleanBlock(lines, 4000);
        Assert.True(Utf8(blk) <= 4000);
        Assert.DoesNotContain('\r', blk);
        Assert.StartsWith("line\nline", blk, StringComparison.Ordinal);
        Assert.False(blk.EndsWith('\n'));
    }
}
