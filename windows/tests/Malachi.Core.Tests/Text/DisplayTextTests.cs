// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only tests (GTK and macOS have no such rule yet): DisplayText,
// what the app does to mail text before its chrome shows it
// (docs/security.md §4). The hostile values are the From name and the
// subject of the security review's message with hostile headers; the
// right-to-left names are ordinary ones, which must come out as written.

using System.Text;
using System.Xml;
using Malachi.Core.Text;
using Xunit;

namespace Malachi.Core.Tests.Text;

public sealed class DisplayTextTests
{
    /// <summary>The review's From name: markup-looking text and an override before a reversed "gnp.exe".</summary>
    internal const string HostileName = "<Bold>Admin</Bold> {Binding} \u202Eexe.gnp";

    /// <summary>What <see cref="HostileName"/> shows: the override gone.</summary>
    internal const string CleanedName = "<Bold>Admin</Bold> {Binding} exe.gnp";

    /// <summary>The review's subject: markup, an override that drew "gnp.exe" as "exe.png", and a bell.</summary>
    internal const string HostileSubject = "AUDITHDR <Run Text='x'/> {x:Bind} & <script>alert(1)</script> \u202Egnp.exe \u0007bell";

    /// <summary>What <see cref="HostileSubject"/> shows: the override gone, the bell a space, the markup text.</summary>
    internal const string CleanedSubject = "AUDITHDR <Run Text='x'/> {x:Bind} & <script>alert(1)</script> gnp.exe  bell";

    [Fact]
    public void TheReviewsHeadersLoseTheirOverrideAndTheirBell()
    {
        Assert.Equal(CleanedName, DisplayText.Clean(HostileName));
        Assert.Equal(CleanedSubject, DisplayText.Clean(HostileSubject));
    }

    [Fact]
    public void EveryExplicitBidiControlIsRemoved()
    {
        for (var c = 0x202A; c <= 0x202E; c++)
        {
            Assert.Equal("ab", DisplayText.Clean("a" + (char)c + "b"));
        }
        for (var c = 0x2066; c <= 0x2069; c++)
        {
            Assert.Equal("ab", DisplayText.Clean("a" + (char)c + "b"));
        }
    }

    [Fact]
    public void ControlCharactersAndLineBreaksBecomeSpaces()
    {
        for (var c = 0; c < 0x20; c++)
        {
            Assert.Equal("a b", DisplayText.Clean("a" + (char)c + "b"));
        }
        for (var c = 0x7F; c <= 0x9F; c++)
        {
            Assert.Equal("a b", DisplayText.Clean("a" + (char)c + "b"));
        }
        Assert.Equal("a b c", DisplayText.Clean("a\u2028b\u2029c"));
    }

    [Fact]
    public void BrokenTextAndNonCharactersBecomeTheReplacementCharacter()
    {
        Assert.Equal("a\uFFFDb", DisplayText.Clean("a\uD800b"));
        Assert.Equal("a\uFFFD", DisplayText.Clean("a\uDC00"));
        Assert.Equal("\uFFFD\uFFFD", DisplayText.Clean("\uFFFE\uFFFF"));
        // A pair is one character and stays.
        Assert.Equal("a😀b", DisplayText.Clean("a😀b"));
    }

    [Theory]
    [InlineData("שלום כהן")] // Hebrew
    [InlineData("محمد عبد الله")] // Arabic
    [InlineData("می\u200Cخواهم")] // Persian, with the zero-width non-joiner it needs
    [InlineData("\u200Fמשה (Moshe)")] // a right-to-left mark stays
    [InlineData("\u061Cأحمد 2\u200E")] // so do the Arabic letter mark and a left-to-right mark
    [InlineData("👩\u200D💻 Zoë Ångström")] // an emoji sequence joined by a zero-width joiner
    [InlineData("<b>bold</b> & {Binding}")] // markup is text
    [InlineData("")]
    public void OrdinaryTextComesBackAsItIs(string s)
    {
        // The same instance: the common case costs no allocation.
        Assert.Same(s, DisplayText.Clean(s));
    }

    [Fact]
    public void CleaningTwiceChangesNothing()
    {
        foreach (var s in new[] { HostileName, HostileSubject, "a\uD800\u2028\u0085\u2067b\uFFFE" })
        {
            var once = DisplayText.Clean(s);
            Assert.Equal(once, DisplayText.Clean(once));
        }
        Assert.Equal("", DisplayText.Clean(null));
    }

    [Fact]
    public void WhatCleaningLeavesIsXmlCharacterData()
    {
        // A toast is an XML document: the review's bell in a subject made it
        // invalid, and Windows dropped the notification.
        Assert.Throws<XmlException>(() => XmlConvert.VerifyXmlChars(HostileSubject));
        XmlConvert.VerifyXmlChars(DisplayText.Clean(HostileSubject));
        var every = new StringBuilder(0x10000);
        for (var c = 0; c <= 0xFFFF; c++)
        {
            every.Append((char)c);
        }
        XmlConvert.VerifyXmlChars(DisplayText.Clean(every.ToString()));
    }

    [Theory]
    [InlineData("")]
    [InlineData(null)]
    [InlineData("\u200F")] // RLM
    [InlineData("\u200E")] // LRM
    [InlineData("\u061C")] // ALM
    [InlineData("\u200B")] // ZERO WIDTH SPACE
    [InlineData("\u2060")] // WORD JOINER
    [InlineData("\uFEFF")] // the BOM, ZERO WIDTH NO-BREAK SPACE
    [InlineData("\u200C\u200D\u00AD")] // the joiners, a soft hyphen
    [InlineData("\u034F\uFE0F\u3164\u115F\uFFA0")] // CGJ, a variation selector, the Hangul fillers
    [InlineData("\U000E0041\U000E007F")] // tag characters
    public void TextOfNothingButDefaultIgnorablesIsInvisible(string? s)
    {
        Assert.True(DisplayText.IsInvisible(s));
        Assert.Equal("", DisplayText.CleanTrimmed(s));
    }

    [Theory]
    [InlineData("a")]
    [InlineData("\u200Fמשה")] // a mark beside letters
    [InlineData("\u0600")] // ARABIC NUMBER SIGN: a format character that draws a sign
    [InlineData("\u06DD")] // ARABIC END OF AYAH, likewise
    [InlineData("\uFFFD")] // what broken text becomes
    [InlineData(" ")] // white space is a name to GTK
    [InlineData("\u200B \u200B")]
    public void TextThatDrawsSomethingIsNot(string s) => Assert.False(DisplayText.IsInvisible(s));

    [Fact]
    public void CleanTrimmedIsTheCleanedTextTrimmedOrNothing()
    {
        Assert.Equal("Hi", DisplayText.CleanTrimmed("  Hi\t"));
        Assert.Equal(CleanedSubject.Trim(), DisplayText.CleanTrimmed(HostileSubject));
        // Controls become spaces, and the trim takes them.
        Assert.Equal("a b", DisplayText.CleanTrimmed("\u0007a\u2028b\r\n"));
        Assert.Equal("", DisplayText.CleanTrimmed(" \u202E\u0007 "));
        // White space between characters that draw nothing is nothing too.
        Assert.Equal("", DisplayText.CleanTrimmed("\u200B \u200B"));
        Assert.Equal("", DisplayText.CleanTrimmed(" \u200F\u3000\uFEFF "));
        // A mark beside letters stays where it is.
        Assert.Equal("\u200Fמשה (Moshe)", DisplayText.CleanTrimmed(" \u200Fמשה (Moshe) "));
        Assert.Equal("\u0600", DisplayText.CleanTrimmed("\u0600"));
    }

    [Fact]
    public void AnIsolateHoldsTheCleanedText()
    {
        Assert.Equal("\u2068Alice\u2069", DisplayText.Isolate("Alice"));
        Assert.Equal("\u2068שלום\u2069", DisplayText.Isolate("שלום"));
        Assert.Equal("", DisplayText.Isolate(""));
        Assert.Equal("", DisplayText.Isolate(null));
        Assert.Equal("", DisplayText.Isolate("\u202E"));
        // A pop of the text's own cannot end the isolate early.
        Assert.Equal("\u2068ab\u2069", DisplayText.Isolate("a\u2069b"));
        Assert.Equal("\u2068" + CleanedName + "\u2069", DisplayText.Isolate(HostileName));
    }
}
