// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/PluralRulesTests.swift, plus the
// table of known languages of macos/scripts/po2strings.py
// (test_unknown_language_is_an_error).

using System.Linq;
using Malachi.Core.I18n;
using Xunit;

namespace Malachi.Core.Tests.I18n;

public sealed class PluralRulesTests
{
    [Fact]
    public void English()
    {
        Assert.Equal(1, PluralRules.Index("en", 0));
        Assert.Equal(0, PluralRules.Index("en", 1));
        Assert.Equal(1, PluralRules.Index("en", 2));
        Assert.Equal(0, PluralRules.Index("en", -1));
        Assert.Equal("one", PluralRules.Category("en", 1));
        Assert.Equal("other", PluralRules.Category("en", 5));
        Assert.Equal(["one", "other"], PluralRules.Categories("en"));
    }

    [Theory]
    [InlineData("cs")]
    [InlineData("sk")]
    [InlineData("cs-CZ")]
    [InlineData("cs_CZ")]
    [InlineData("CS")]
    public void CzechAndSlovak(string lang)
    {
        Assert.Equal(0, PluralRules.Index(lang, 1));
        Assert.Equal(1, PluralRules.Index(lang, 2));
        Assert.Equal(1, PluralRules.Index(lang, 4));
        Assert.Equal(2, PluralRules.Index(lang, 5));
        Assert.Equal(2, PluralRules.Index(lang, 0));
        Assert.Equal(2, PluralRules.Index(lang, 22));
        Assert.Equal("one", PluralRules.Category("cs", 1));
        Assert.Equal("few", PluralRules.Category("cs", 3));
        Assert.Equal("other", PluralRules.Category("cs", 5));
    }

    [Fact]
    public void Polish()
    {
        Assert.Equal("one", PluralRules.Category("pl", 1));
        Assert.Equal("few", PluralRules.Category("pl", 2));
        Assert.Equal("many", PluralRules.Category("pl", 5));
        Assert.Equal("many", PluralRules.Category("pl", 12));
        Assert.Equal("few", PluralRules.Category("pl", 22));
        Assert.Equal("many", PluralRules.Category("pl", 0));
    }

    [Theory]
    [InlineData("ru")]
    [InlineData("uk")]
    public void RussianAndUkrainian(string lang)
    {
        Assert.Equal("one", PluralRules.Category(lang, 1));
        Assert.Equal("one", PluralRules.Category(lang, 21));
        Assert.Equal("many", PluralRules.Category(lang, 11));
        Assert.Equal("few", PluralRules.Category(lang, 3));
        Assert.Equal("many", PluralRules.Category(lang, 13));
        Assert.Equal("many", PluralRules.Category(lang, 5));
    }

    [Fact]
    public void French()
    {
        Assert.Equal(0, PluralRules.Index("fr", 0));
        Assert.Equal(0, PluralRules.Index("fr", 1));
        Assert.Equal(1, PluralRules.Index("fr", 2));
    }

    [Fact]
    public void Romanian()
    {
        Assert.Equal("one", PluralRules.Category("ro", 1));
        Assert.Equal("few", PluralRules.Category("ro", 0));
        Assert.Equal("few", PluralRules.Category("ro", 19));
        Assert.Equal("other", PluralRules.Category("ro", 20));
        Assert.Equal("few", PluralRules.Category("ro", 101));
    }

    [Fact]
    public void UnknownLanguageUsesTheEnglishRule()
    {
        Assert.Equal(0, PluralRules.Index("tlh", 1));
        Assert.Equal(1, PluralRules.Index("tlh", 3));
        Assert.Equal("other", PluralRules.Category("", 3));
    }

    // po2strings.py: an unknown language is an error when a catalogue is
    // loaded, never a guess; a region does not matter.
    [Fact]
    public void OnlyTheTableLanguagesAreKnown()
    {
        Assert.False(PluralRules.Knows("tlh"));
        Assert.False(PluralRules.Knows(""));
        Assert.True(PluralRules.Knows("pt_BR"));
        Assert.True(PluralRules.Knows("cs-CZ"));
        Assert.Equal(["one", "other"], PluralRules.Categories("pt_BR"));
        Assert.Equal(["one", "few", "other"], PluralRules.Categories("cs"));
        string[] table = ["en", "de", "nl", "sv", "da", "nb", "nn", "fi", "it", "es", "pt", "fr", "cs", "sk", "pl", "ru", "uk", "ro"];
        Assert.All(table, language => Assert.True(PluralRules.Knows(language), language));
        Assert.All(table, language => Assert.True(PluralRules.Index(language, 7) < PluralRules.Categories(language).Count, language));
    }

    [Fact]
    public void LargestMagnitudesDoNotOverflow()
    {
        Assert.Equal(1, PluralRules.Index("en", long.MinValue));
        Assert.Equal("many", PluralRules.Category("pl", long.MinValue));
        Assert.Equal("other", PluralRules.Category("cs", long.MaxValue));
        Assert.Equal("cs", PluralRules.Base("CS_cz"));
        Assert.True(Enumerable.Range(0, 200).All(n => PluralRules.Category("ru", n) is "one" or "few" or "many"));
    }
}
