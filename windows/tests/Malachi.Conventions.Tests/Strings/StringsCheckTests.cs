// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The strings check of docs/windows-port.md §9, the counterpart of
// macos/scripts/check-strings.py and of its test: the Windows client keys
// its strings by the GTK msgids (L10n.T/N/C in C#, {l:T} in XAML) and its
// catalogues are po/*.po, so a msgid that is not in po/malachi.pot can
// never be translated. It fails on a msgid missing from the template (with
// its context and its plural; a call marked "Windows-only string" may
// miss, as check-strings.py accepts "macOS-only string"), on a msgid
// built at run time, and on a
// stale exclusion; it warns (a skip that lists them) about literals handed
// to a text sink or formatted into a translated sentence without the
// Windows-only mark; and it covers the template: every msgid is used in
// windows/src or excluded with a reason in windows/parity-exclusions.txt.
//
// The coverage cannot hold before the screens of phase E wave 2 exist: it
// skips with the list of the missing msgids until CoverageEnforced is true
// (or MALACHI_MSGID_COVERAGE=strict is set), which the integration of the
// last screen turns on.

using System;
using System.Collections.Generic;
using System.Linq;
using Xunit;

namespace Malachi.Conventions.Tests.Strings;

public sealed class StringsCheckTests
{
    /// <summary>Whether a template msgid neither used nor excluded fails the coverage (true once every screen exists).</summary>
    private const bool CoverageEnforced = false;

    // The markup extension itself hands its properties to L10n.
    private const string MarkupExtensionFile = "windows/src/Malachi.App/Localization/T.cs";

    private static bool CoverageStrict =>
        CoverageEnforced || string.Equals(Environment.GetEnvironmentVariable("MALACHI_MSGID_COVERAGE"), "strict", StringComparison.OrdinalIgnoreCase);

    private static IEnumerable<L10nCall> AllCalls => CSharpStrings.Sources.Calls.Concat(XamlStrings.Sources.Calls);

    [Fact]
    public void TheScannersSeeTheSources()
    {
        // A scanner that finds nothing would pass everything below.
        Assert.True(CSharpStrings.Sources.Files > 100, $"{CSharpStrings.Sources.Files} C# files");
        Assert.True(XamlStrings.Sources.Files >= 3, $"{XamlStrings.Sources.Files} XAML files");
        Assert.Contains(CSharpStrings.Sources.Calls, c => c.Kind == 'T' && c.Msgid == "Connecting to backend…");
        Assert.Contains(CSharpStrings.Sources.Calls, c => c.Kind == 'C' && c.Context is not null);
        Assert.Contains(CSharpStrings.Sources.Calls, c => c.Kind == 'N' && c.Plural is not null);
        Assert.Contains(XamlStrings.Sources.Calls, c => c.Msgid == "Search Mail");
        Assert.True(Template.Pot.Plain.Count > 400);
    }

    [Fact]
    public void EveryMsgidOfTheSourcesIsInTheTemplate()
    {
        var pot = Template.Pot;
        var problems = new List<string>();
        foreach (var call in AllCalls.Where(c => c.Msgid is not null && !c.WindowsOnly))
        {
            switch (call.Kind)
            {
                case 'T' when !pot.Plain.Contains(call.Msgid!):
                    problems.Add($"{call.Where}: missing msgid \"{call.Msgid}\"");
                    break;
                case 'C' when call.Context is null:
                    problems.Add($"{call.Where}: the context of \"{call.Msgid}\" is not a literal");
                    break;
                case 'C' when !pot.Contexts.Contains((call.Context, call.Msgid!)):
                    problems.Add($"{call.Where}: missing msgid \"{call.Msgid}\" with context \"{call.Context}\"");
                    break;
                case 'N' when !pot.Plurals.TryGetValue(call.Msgid!, out var plural):
                    problems.Add($"{call.Where}: missing plural msgid \"{call.Msgid}\"");
                    break;
                case 'N' when call.Plural is null || pot.Plurals[call.Msgid!] != call.Plural:
                    problems.Add($"{call.Where}: the plural of \"{call.Msgid}\" is \"{call.Plural}\", the template's \"{pot.Plurals[call.Msgid!]}\"");
                    break;
            }
        }
        Assert.True(problems.Count == 0, "msgids not in po/malachi.pot:\n" + string.Join('\n', problems));
    }

    [Fact]
    public void EveryMsgidIsALiteral()
    {
        var dynamic = AllCalls
            .Where(c => c.Msgid is null && !c.Where.StartsWith(MarkupExtensionFile + ":", StringComparison.Ordinal))
            .Select(c => c.Where)
            .ToList();
        Assert.True(dynamic.Count == 0, "msgids built at run time cannot be checked:\n" + string.Join('\n', dynamic));
    }

    [Fact]
    public void LiteralsInTextSinksAreMarkedWindowsOnly()
    {
        var unmarked = CSharpStrings.Sources.UnmarkedSinks.Concat(XamlStrings.Sources.UnmarkedSinks).ToList();
        if (unmarked.Count > 0)
        {
            Assert.Skip($"warning: {unmarked.Count} literals in text sinks are neither translated nor marked \"Windows-only string\":\n"
                + string.Join('\n', unmarked));
        }
    }

    [Fact]
    public void LiteralFormatArgumentsAreMarkedWindowsOnly()
    {
        var literals = CSharpStrings.Sources.LiteralArguments;
        if (literals.Count > 0)
        {
            Assert.Skip($"warning: {literals.Count} literals are formatted into translated sentences:\n" + string.Join('\n', literals));
        }
    }

    [Fact]
    public void EveryTemplateMsgidIsUsedOrExcluded()
    {
        var used = Used();
        var excluded = ParityExclusions.Load().Select(e => e.Key).ToHashSet(StringComparer.Ordinal);
        var missing = Template.Pot.Entries
            .Where(e => !used.Contains(e.Msgid) && !excluded.Contains(Template.KeyOf(e.Msgctxt, e.Msgid)))
            .Select(e => e.Msgctxt is null ? $"\"{e.Msgid}\"" : $"\"{e.Msgid}\" (context \"{e.Msgctxt}\")")
            .ToList();
        if (missing.Count == 0)
        {
            return;
        }
        var message = $"{missing.Count} msgids of po/malachi.pot are neither used in windows/src nor excluded in windows/parity-exclusions.txt:\n"
            + string.Join('\n', missing);
        if (!CoverageStrict)
        {
            Assert.Skip("until the screens of phase E wave 2 exist: " + message);
        }
        Assert.Fail(message);
    }

    [Fact]
    public void TheExclusionsAreTemplateMsgidsWithReasonsThatTheSourcesDoNotUse()
    {
        var pot = Template.Pot;
        var keys = pot.Entries.Select(e => Template.KeyOf(e.Msgctxt, e.Msgid)).ToHashSet(StringComparer.Ordinal);
        var used = Used();
        var problems = new List<string>();
        var seen = new HashSet<string>(StringComparer.Ordinal);
        foreach (var (key, reason, line) in ParityExclusions.Load())
        {
            var where = $"windows/parity-exclusions.txt:{line}";
            if (!seen.Add(key))
            {
                problems.Add($"{where}: listed twice");
            }
            if (!keys.Contains(key))
            {
                problems.Add($"{where}: \"{key}\" is not a msgid of po/malachi.pot");
            }
            if (reason.Length == 0)
            {
                problems.Add($"{where}: no reason");
            }
            var msgid = key.Contains((char)4, StringComparison.Ordinal) ? key[(key.IndexOf((char)4, StringComparison.Ordinal) + 1)..] : key;
            if (used.Contains(msgid))
            {
                problems.Add($"{where}: \"{msgid}\" is used in windows/src; drop the exclusion");
            }
        }
        Assert.True(problems.Count == 0, string.Join('\n', problems));
    }

    [Theory]
    [InlineData("{l:T Msgid='Search Mail'}", "Search Mail", null)]
    [InlineData("{l:T Msgid='Folder', Context='search scope'}", "Folder", "search scope")]
    [InlineData("{l:T Context=\"search scope\", Msgid=\"All Accounts\"}", "All Accounts", "search scope")]
    [InlineData("{ l:T  Msgid = 'Move to _Trash' }", "Move to _Trash", null)]
    [InlineData("{l:T Msgid='a, b {c}'}", "a, b {c}", null)]
    [InlineData("{l:T Msgid=Messages}", "Messages", null)]
    public void MarkupExtensionsParse(string text, string msgid, string? context)
    {
        var args = XamlStrings.ParseExtension(text, new HashSet<string> { "l" }, "T");
        Assert.NotNull(args);
        Assert.Equal(msgid, args["Msgid"]);
        Assert.Equal(context, args.GetValueOrDefault("Context"));
    }

    [Theory]
    [InlineData("{x:Bind Title}")]
    [InlineData("{l:Other Msgid='x'}")]
    [InlineData("{m:T Msgid='x'}")]
    [InlineData("Plain text")]
    public void OtherValuesAreNoMsgid(string text) =>
        Assert.Null(XamlStrings.ParseExtension(text, new HashSet<string> { "l" }, "T"));

    [Fact]
    public void TheExclusionFileEscapes()
    {
        Assert.Equal("a\nb", ParityExclusions.Unescape("a\\nb"));
        Assert.Equal("say \"hi\"", ParityExclusions.Unescape("say \\\"hi\\\""));
        Assert.Equal("folder" + (char)4 + "Inbox", ParityExclusions.Unescape("folder\\004Inbox"));
        Assert.Equal("a\\b", ParityExclusions.Unescape("a\\\\b"));
    }

    // What the sources use: every C# string literal and every msgid of XAML.
    private static HashSet<string> Used()
    {
        var used = new HashSet<string>(CSharpStrings.Sources.Literals, StringComparer.Ordinal);
        foreach (var call in XamlStrings.Sources.Calls.Where(c => c.Msgid is not null))
        {
            used.Add(call.Msgid!);
        }
        return used;
    }
}
