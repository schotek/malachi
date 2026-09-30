// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/JiraPatternTests.swift (the
// patternCases of ui/internal/jira/settings_test.go TestPatternError, what
// Go's regexp/syntax says of each pattern; unicodeClassNames;
// hostilePatterns), and a Windows addition: a lone surrogate is Go's
// invalid UTF-8.

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;
using Xunit;

namespace Malachi.Core.Tests.IssueTrackers;

public sealed class JiraPatternTests
{
    public static TheoryData<string, string> PatternCases => new()
    {
        { @"^Remote comment create date:.*$", "" },
        { @"abc", "" },
        { @"a|b", "" },
        { @"(a|b)*c+?", "" },
        { @"příliš žluťoučký", "" },
        { @"[a-z]+", "" },
        { @"[]a]", "" },
        { @"[^]a]", "" },
        { @"[a-]", "" },
        { @"[-a]", "" },
        { @"[a\-z]", "" },
        { @"[á-ž]", "" },
        { @"[ž-á]", "invalid character class range" },
        { @"[z-a]", "invalid character class range" },
        { @"[a", "missing closing ]" },
        { @"[", "missing closing ]" },
        { @"[]", "missing closing ]" },
        { @"[^]", "missing closing ]" },
        { @"[a-", "missing closing ]" },
        { @"[a\", "trailing backslash at end of expression" },
        { @"[a-\d]", "invalid escape sequence" },
        { @"[\d-z]", "" },
        { @"[\b]", "invalid escape sequence" },
        { @"[\x41-\x5a]", "" },
        { @"[a-z&&[^b]]", "" },
        { @"[[:alpha:]]", "" },
        { @"[[:^alpha:]]", "" },
        { @"[[:word:][:xdigit:]]", "" },
        { @"[[:foo:]]", "invalid character class range" },
        { @"[[:alpha:]", "missing closing ]" },
        { @"[[=a=]]", "" },
        { @"\d+\s\w\D\S\W", "" },
        { @"\pL+", "" },
        { @"\PL", "" },
        { @"\p{Greek}", "" },
        { @"\p{^Greek}", "" },
        { @"\p{Lu}", "" },
        { @"[\pL\d]", "" },
        { @"\p{Foo}", "invalid character class range" },
        { @"[\p{Foo}]", "invalid character class range" },
        { @"\p{L", "invalid character class range" },
        { @"\pX", "invalid character class range" },
        { @"(?i)abc", "" },
        { @"(?i:abc)", "" },
        { @"(?i-s:abc)", "" },
        { @"(?U)a*", "" },
        { @"(?m)^a$", "" },
        { @"(?s).", "" },
        { @"(?i)(?-i)", "" },
        { @"(?P<name>a)", "" },
        { @"(?<name>a)", "" },
        { @"(?P<na me>a)", "invalid named capture" },
        { @"(?P<>a)", "invalid named capture" },
        { @"(?P<n", "invalid named capture" },
        { @"(?<=a)", "invalid named capture" },
        { @"(?<!a)", "invalid named capture" },
        { @"(?=a)", "invalid or unsupported Perl syntax" },
        { @"(?!a)", "invalid or unsupported Perl syntax" },
        { @"(?>a)", "invalid or unsupported Perl syntax" },
        { @"(?#c)", "invalid or unsupported Perl syntax" },
        { @"(?'n'a)", "invalid or unsupported Perl syntax" },
        { @"(?x)a", "invalid or unsupported Perl syntax" },
        { @"(?i", "invalid or unsupported Perl syntax" },
        { @"(?", "invalid or unsupported Perl syntax" },
        { @"(?-)", "invalid or unsupported Perl syntax" },
        { @"(?i-)", "invalid or unsupported Perl syntax" },
        { @"(?--i)", "invalid or unsupported Perl syntax" },
        { @"(", "missing closing )" },
        { @"(a", "missing closing )" },
        { @"((a)", "missing closing )" },
        { @")", "unexpected )" },
        { @"a)", "unexpected )" },
        { @"(a))", "unexpected )" },
        { @"()", "" },
        { @"(?:)", "" },
        { @"(a|b|)", "" },
        { @"a||b", "" },
        { @"|", "" },
        { @"*", "missing argument to repetition operator" },
        { @"+a", "missing argument to repetition operator" },
        { @"?", "missing argument to repetition operator" },
        { @"{2}", "missing argument to repetition operator" },
        { @"|*", "missing argument to repetition operator" },
        { @"a|*", "missing argument to repetition operator" },
        { @"(*)", "missing argument to repetition operator" },
        { @"(?i)*", "missing argument to repetition operator" },
        { @"(|a)*", "" },
        { @"(?:)*", "" },
        { @"^*", "" },
        { @"$*", "" },
        { @"\b*", "" },
        { @"a**", "invalid nested repetition operator" },
        { @"a*+", "invalid nested repetition operator" },
        { @"a++", "invalid nested repetition operator" },
        { @"a???", "invalid nested repetition operator" },
        { @"a{2}{3}", "invalid nested repetition operator" },
        { @"a{2}*", "invalid nested repetition operator" },
        { @"a{2}+", "invalid nested repetition operator" },
        { @"a*?", "" },
        { @"a??", "" },
        { @"a{2,3}?", "" },
        { @"a{2}", "" },
        { @"a{2,}", "" },
        { @"a{2,3}", "" },
        { @"a{0}", "" },
        { @"a{01}", "" },
        { @"a{,3}", "" },
        { @"a{}", "" },
        { @"a{2", "" },
        { @"x{2}{", "" },
        { @"a{1000}", "" },
        { @"a{1001}", "invalid repeat count" },
        { @"a{3,2}", "invalid repeat count" },
        { @"a{100000000000}", "invalid repeat count" },
        { @"(a{2}){3}", "" },
        { @"(a{100}){10}", "" },
        { @"(a{100}){11}", "invalid repeat count" },
        { @"((a{10}){10}){10}", "" },
        { @"((a{10}){10}){11}", "invalid repeat count" },
        { @"(a{1000}){0}", "" },
        { @"(a{1000}b{1000}c{1000}d{1000}){1000}", "expression too large" },
        { @"\", "trailing backslash at end of expression" },
        { @"a\", "trailing backslash at end of expression" },
        { @"\.", "" },
        { @"\_", "" },
        { @"\-", "" },
        { @"\q", "invalid escape sequence" },
        { @"\e", "invalid escape sequence" },
        { @"\h", "invalid escape sequence" },
        { @"\1", "invalid escape sequence" },
        { @"\8", "invalid escape sequence" },
        { @"\12", "" },
        { @"\0", "" },
        { @"\07", "" },
        { @"\x41", "" },
        { @"\x4", "invalid escape sequence" },
        { @"\x", "invalid escape sequence" },
        { @"\xg1", "invalid escape sequence" },
        { @"\x{41}", "" },
        { @"\x{10FFFF}", "" },
        { @"\x{110000}", "invalid escape sequence" },
        { @"\x{}", "invalid escape sequence" },
        { @"\x{4g}", "invalid escape sequence" },
        { @"\a\f\n\r\t\v", "" },
        { @"\A\z", "" },
        { @"\b\B", "" },
        { @"\Z", "invalid escape sequence" },
        { @"\C", "invalid escape sequence" },
        { @"\G", "invalid escape sequence" },
        { @"\cA", "invalid escape sequence" },
        { @"\k<n>", "invalid escape sequence" },
        { @"\N{x}", "invalid escape sequence" },
        { @"\E", "invalid escape sequence" },
        { @"\Q.*\E", "" },
        { @"\Q.*", "" },
        { @"\Qa\Eb*", "" },
        { @"\Q(\E)", "unexpected )" },
        { @"[\Q]\E]", "invalid escape sequence" },
    };

    [Theory]
    [MemberData(nameof(PatternCases))]
    public void PatternError(string pattern, string reason) => Assert.Equal(reason, Jira.PatternError(pattern));

    [Fact]
    public void PatternErrorOfTheSuggestionAndDeepNesting()
    {
        Assert.Equal(158, PatternCases.Count);
        Assert.Equal("", Jira.PatternError(Jira.SuggestedMetadataFilter));
        // A pattern as long as an entry may be, of groups nested as deep as
        // that allows.
        var deep = new string('(', API.Limits.MaxJiraPatternBytes / 2) + new string(')', API.Limits.MaxJiraPatternBytes / 2);
        Assert.Equal("", Jira.PatternError(deep));
        Assert.Equal("unexpected )", Jira.PatternError(deep[1..]));
    }

    // The names of Unicode classes are looked up without regard to case,
    // spaces, hyphens and underscores (parse.go canonicalName), the scripts
    // of several words too, which Go 1.26 misses.
    [Fact]
    public void UnicodeClassNames()
    {
        foreach (var name in new[]
        {
            "L", "l", "Lu", "LC", "Letter", "letter", "Greek", "greek", " greek ", "Any", "ASCII", "Assigned",
            "Old_Italic", "olditalic", "Old-Italic", "SignWriting", "^Cyrillic", "Uppercase_Letter", "punct",
        })
        {
            Assert.True(Jira.PatternError(@"\p{" + name + "}") == "", @"\p{" + name + "}");
            Assert.True(Jira.PatternError(@"[\P{" + name + "}]") == "", @"[\P{" + name + "}]");
        }
        foreach (var name in new[] { "", "^", "Foo", "Gree", "Greeks", "L u x", "Ž" })
        {
            Assert.Equal("invalid character class range", Jira.PatternError(@"\p{" + name + "}"));
        }
        Assert.Equal("invalid character class range", Jira.PatternError(@"\p"));
        Assert.Equal("invalid character class range", Jira.PatternError(@"\pž"));
        Assert.Equal("invalid character class range", Jira.PatternError(@"\p{Greek"));
    }

    // Hostile input: long, deep and repetitive patterns are answered, not
    // followed into the ground.
    [Fact]
    public void HostilePatterns()
    {
        var open = new string('(', 100_000);
        Assert.Equal("missing closing )", Jira.PatternError(open));
        Assert.Equal("expression nests too deeply", Jira.PatternError(open + new string(')', 100_000)));
        Assert.Equal("", Jira.PatternError(new string('a', 200_000)));
        Assert.Equal("", Jira.PatternError(string.Concat(Enumerable.Repeat("a*", 50_000))));
        Assert.Equal("missing closing ]", Jira.PatternError(new string('[', 50_000)));
        Assert.Equal("", Jira.PatternError(string.Concat(Enumerable.Repeat("a|", 50_000))));
        Assert.Equal("trailing backslash at end of expression", Jira.PatternError(new string('\\', 50_001)));
        Assert.Equal("invalid repeat count", Jira.PatternError("(((a{10}){10}){10}){10}"));
        Assert.Equal("", Jira.PatternError("\0[\0-" + (char)0x1F + "]")); // control characters are characters
        Assert.Equal("", Jira.PatternError("😀+[😀-😂]"));
        Assert.Equal("invalid character class range", Jira.PatternError("[😂-😀]"));
    }

    // Windows: Go's regexp.Compile refuses invalid UTF-8, which in a C#
    // string is a lone surrogate; a U+FFFD the user typed is a character.
    [Fact]
    public void LoneSurrogateIsInvalidUtf8()
    {
        Assert.Equal("invalid UTF-8", Jira.PatternError("a" + JiraTests.Invalid));
        Assert.Equal("", Jira.PatternError("a" + (char)0xFFFD));
    }
}
