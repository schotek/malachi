// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/JiraTranslationTests.swift and
// JiraSettingsTranslationTests.swift: the Czech side of ui/internal/jira,
// the cases jira_test.go, wizard_test.go, transitions_test.go and
// settings_test.go check with a fake catalogue (a context entry must win
// over the plain msgid), here against po/cs.po, and the intent of
// po_test.go: the port asks for exactly the msgids, contexts and plurals
// po/malachi.pot assigns to ui/internal/jira. The process-wide catalogue
// stays English (the tests run in parallel), so the port's own calls are
// read from its sources (Malachi.Core/IssueTrackers). Windows reads
// po/cs.po, so these always run.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using System.Text.RegularExpressions;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Tests.I18n;
using Xunit;

namespace Malachi.Core.Tests.IssueTrackers;

public sealed partial class JiraTranslationTests
{
    private static readonly Lazy<Catalogue> Czech = new(() => Catalogue.Load(RepositoryPo.Directory, ["cs"]));

    private static Catalogue Cs => Czech.Value;

    // po_test.go msgKey: a msgid with its context and plural ("" for none).
    private readonly record struct MsgKey(string Ctx, string Msgid, string Plural)
    {
        public override string ToString() => $"msgid \"{Msgid}\" (context \"{Ctx}\", plural \"{Plural}\")";
    }

    // A C-quoted string of a PO file or a C# literal without its quotes,
    // unescaped (\" \\ \n \t).
    private static string Unquote(string s)
    {
        var output = new StringBuilder(s.Length);
        for (var i = 0; i < s.Length; i++)
        {
            if (s[i] == '\\' && i + 1 < s.Length)
            {
                i++;
                output.Append(s[i] switch { 'n' => '\n', 't' => '\t', var c => c });
                continue;
            }
            output.Append(s[i]);
        }
        return output.ToString();
    }

    // po_test.go template: every entry of po/malachi.pot, and whether it
    // names a file of ui/internal/jira among its references.
    private static Dictionary<MsgKey, bool> Template()
    {
        var output = new Dictionary<MsgKey, bool>();
        string ctx = "", msgid = "", plural = "";
        var ours = false;
        string? field = null;
        void Flush()
        {
            if (msgid.Length > 0)
            {
                output[new MsgKey(ctx, msgid, plural)] = ours;
            }
            ctx = msgid = plural = "";
            ours = false;
            field = null;
        }
        void Append(string name, string value)
        {
            switch (name)
            {
                case "msgctxt":
                    ctx += value;
                    break;
                case "msgid":
                    msgid += value;
                    break;
                case "msgid_plural":
                    plural += value;
                    break;
                default:
                    break;
            }
        }
        foreach (var line in File.ReadAllText(RepositoryPo.Pot).Replace("\r\n", "\n", StringComparison.Ordinal).Split('\n'))
        {
            if (line.Trim().Length == 0)
            {
                Flush();
            }
            else if (line.StartsWith("#:", StringComparison.Ordinal))
            {
                ours = ours || line.Contains("ui/internal/jira/", StringComparison.Ordinal);
            }
            else if (line.Length >= 2 && line[0] == '"' && line[^1] == '"' && field is not null)
            {
                Append(field, Unquote(line[1..^1]));
            }
            else if (line.IndexOf(' ', StringComparison.Ordinal) is var space and > 0 && space + 1 < line.Length && line[space + 1] == '"' && line[^1] == '"')
            {
                field = line[..space] is "msgctxt" or "msgid" or "msgid_plural" ? line[..space] : null;
                if (field is not null)
                {
                    Append(field, Unquote(line[(space + 2)..^1]));
                }
            }
        }
        Flush();
        return output;
    }

    [GeneratedRegex("""L10n\.([TNC])\(\s*"((?:[^"\\]|\\.)*)"(?:\s*,\s*"((?:[^"\\]|\\.)*)")?""")]
    private static partial Regex L10nCall();

    // What the port translates: every L10n.T("…"), L10n.N("…", "…" and
    // L10n.C("…", "…") of its sources (po_test.go exercise with a recorder;
    // the sources stand in for the calls).
    private static HashSet<MsgKey> PortKeys()
    {
        var keys = new HashSet<MsgKey>();
        var dir = Path.Combine(RepositoryPo.Root, "windows", "src", "Malachi.Core", "IssueTrackers");
        foreach (var file in Directory.EnumerateFiles(dir, "*.cs"))
        {
            foreach (Match m in L10nCall().Matches(File.ReadAllText(file)))
            {
                var first = Unquote(m.Groups[2].Value);
                var second = m.Groups[3].Success ? Unquote(m.Groups[3].Value) : "";
                keys.Add(m.Groups[1].Value switch
                {
                    "C" => new MsgKey(first, second, ""),
                    "N" => new MsgKey("", first, second),
                    _ => new MsgKey("", first, ""),
                });
            }
        }
        return keys;
    }

    // po_test.go TestMsgidsInTemplate for the port: every msgid it
    // translates is in the template with its context and plural, and every
    // entry that names ui/internal/jira is one it translates.
    [Fact]
    public void PortUsesTheMsgidsOfTheGoPackage()
    {
        var pot = Template();
        var used = PortKeys();
        Assert.Contains(true, pot.Values); // some entry names ui/internal/jira
        Assert.True(used.Count > 50, $"found only {used.Count} calls");
        var missing = used.Where(k => !pot.ContainsKey(k)).ToArray();
        Assert.True(missing.Length == 0, "po/malachi.pot lacks " + string.Join("; ", missing));
        var unused = pot.Where(e => e.Value && !used.Contains(e.Key)).Select(e => e.Key).ToArray();
        Assert.True(unused.Length == 0, "po/malachi.pot names ui/internal/jira for " + string.Join("; ", unused) + ", which the Windows port does not translate");
    }

    // Every msgid the port asks for has a Czech translation.
    [Fact]
    public void EveryMsgidOfThePortIsTranslated()
    {
        var cs = Cs;
        foreach (var k in PortKeys())
        {
            if (k.Plural.Length > 0)
            {
                Assert.True(cs.PluralForms(k.Msgid) is not null, $"no Czech plural for {k}");
            }
            else if (k.Ctx.Length > 0)
            {
                Assert.True(cs.Lookup(Catalogue.ContextKey(k.Ctx, k.Msgid)) is not null, $"no Czech translation for {k}");
            }
            else
            {
                Assert.True(cs.Lookup(k.Msgid) is not null, $"no Czech translation for {k}");
            }
        }
    }

    // jira_test.go TestVirtualFolders, TestIssueCard and wizard_test.go
    // TestWizardTexts in Czech: the context entry, never the plain msgid.
    [Fact]
    public void ContextsPickTheJiraEntries()
    {
        var cs = Cs;
        Assert.Equal("Přiřazené mně", cs.Context("folder", "Assigned to Me"));
        Assert.Equal("Sledované", cs.Context("folder", "Watching"));
        Assert.Equal("Neuzavřené", cs.Context("folder", "Open"));
        Assert.NotEqual("Neuzavřené", cs.Translate("Open")); // the verb must not be the folder
        Assert.Equal("Nezadáno", cs.Context("jira value", "None"));
        Assert.Equal("Žádné", cs.Translate("None")); // the plain None is another entry
        Assert.Equal("Nepřiřazeno", cs.Translate("Unassigned"));
        Assert.Equal("Prostory", cs.Context("jira", "Spaces"));
        Assert.Equal("Interní", cs.Context("jira", "Internal"));
        Assert.Equal("Přidat účet _Jira…", cs.Translate("Add _Jira Account…"));
        Assert.Equal("; ", cs.Context("change list separator", "; "));
        Assert.Equal("Změnit stav", cs.Translate("Change Status"));
        Assert.Equal("Vyžaduje pole v Jiře", cs.Translate("Needs fields in Jira"));
        Assert.Equal("Žádná změna stavu není k dispozici", cs.Translate("No status change is available"));
    }

    [Fact]
    public void FormattedSentences()
    {
        var cs = Cs;
        Assert.Equal("Stav: K řešení → Probíhá", cs.Translate("Status: %s → %s", "K řešení", "Probíhá"));
        Assert.Equal("Řešitel: Nepřiřazeno → Jana Dvořáková", cs.Translate("Assignee: %s → %s", "Nepřiřazeno", "Jana Dvořáková"));
        Assert.Equal("Nalezeno: Acme Jira, verze 9.12.4", cs.Translate("Found %s, version %s", "Acme Jira", "9.12.4"));
        Assert.Equal("Nalezeno: Jira Cloud", cs.Translate("Found %s", "Jira Cloud"));
        Assert.Equal("Otevřít ITSD-42 v prohlížeči", cs.Translate("Open %s in the Browser", "ITSD-42"));
        Assert.Equal("Komentář k ITSD-42", cs.Translate("Comment on %s", "ITSD-42"));
        Assert.Equal("Web Jira odmítl token účtu Acme", cs.Translate("The Jira site rejected the token of %s", "Acme"));
        Assert.Equal("přes Issue Sync", cs.Translate("via %s", "Issue Sync"));
        Assert.Equal("Stav změněn na Probíhá", cs.Translate("Status changed to %s", "Probíhá"));
        Assert.Equal("Stav se nepodařilo změnit: Přechod není povolen", cs.Translate("The status could not be changed: %s", "Přechod není povolen"));
        // The progressive forms fit the generic sentences (a neuter noun, as "Uložení konceptu").
        Assert.Equal("Nastavení stavu selhalo: server je nedostupný", cs.Translate("%s failed: the server could not be reached", cs.Translate("Changing the status")));
        Assert.Equal("Načtení změn stavu selhalo: server vrátil chybu", cs.Translate("%s failed: the server returned an error", cs.Translate("Loading the status changes")));
    }

    [Fact]
    public void Plurals()
    {
        var cs = Cs;
        Assert.Equal("přibližně 1 úkol", cs.Plural("about %d issue", "about %d issues", 1));
        Assert.Equal("přibližně 3 úkoly", cs.Plural("about %d issue", "about %d issues", 3));
        Assert.Equal("přibližně 5 úkolů", cs.Plural("about %d issue", "about %d issues", 5));
        Assert.Equal("přibližně 0 úkolů", cs.Plural("about %d issue", "about %d issues", 0));
        Assert.Equal("Vyberte nejvýš 200 prostorů", cs.Plural("Select at most %d space", "Select at most %d spaces", API.Limits.MaxJiraSpaces));
    }

    // settings_test.go TestSettingsTexts and TestStatusGroups in Czech.
    [Fact]
    public void SettingsSectionsAndRows()
    {
        var cs = Cs;
        Assert.Equal("Účet Jira", cs.Translate("Jira Account"));
        Assert.Equal("Nahradit token…", cs.Translate("Replace Token…"));
        Assert.Equal("Zobrazovat změny stavu a řešitele", cs.Translate("Show Status and Assignee Changes"));
        Assert.Equal("Uzavřené stavy", cs.Translate("Closed Statuses"));
        Assert.Equal("Notifikační e-maily", cs.Translate("Notification E-mails"));
        Assert.Equal("Komentáře od botů", cs.Translate("Comments Posted by Bots"));
        Assert.Equal("Účty botů", cs.Translate("Bot Accounts"));
        Assert.Equal("Skryté řádky", cs.Translate("Hidden Lines"));
        Assert.Equal("Předpony jmen", cs.Translate("Name Prefixes"));
        Assert.Equal("Hotovo", cs.Context("status category", "Done"));
        Assert.Equal("K řešení", cs.Context("status category", "To Do"));
        Assert.Equal("Ostatní", cs.Context("status category", "Other"));
    }

    // settings_test.go TestSuggestions and TestCheckEntry in Czech: the
    // entry and the reason go into the sentence as they are.
    [Fact]
    public void SettingsSentences()
    {
        var cs = Cs;
        Assert.Equal("Přidat Issue Sync – Synchronization for Jira", cs.Translate("Add %s", Jira.SuggestedBotName));
        Assert.Equal("Přidat ^Remote comment create date:.*$", cs.Translate("Add %s", Jira.SuggestedMetadataFilter));
        Assert.Equal("Tento vzor není platný: missing closing )", cs.Translate("This pattern is not valid: %s", "missing closing )"));
        // A reason with a per cent sign is text, not a format.
        Assert.Equal("Přidat 100 %d %s", cs.Translate("Add %s", "100 %d %s"));
    }

    [Fact]
    public void SettingsPlurals()
    {
        var cs = Cs;
        Assert.Equal("Vyberte nejvýš 64 stavů", cs.Plural("Select at most %d status", "Select at most %d statuses", API.Limits.MaxJiraStatuses));
        Assert.Equal("Vyberte nejvýš 1 stav", cs.Plural("Select at most %d status", "Select at most %d statuses", 1));
        Assert.Equal("Vyberte nejvýš 3 stavy", cs.Plural("Select at most %d status", "Select at most %d statuses", 3));
        Assert.Equal("Seznam pojme nejvýš 32 položek", cs.Plural("The list holds at most %d entry", "The list holds at most %d entries", API.Limits.MaxJiraListEntries));
        Assert.Equal("Seznam pojme nejvýš 1 položku", cs.Plural("The list holds at most %d entry", "The list holds at most %d entries", 1));
        Assert.Equal("Seznam pojme nejvýš 2 položky", cs.Plural("The list holds at most %d entry", "The list holds at most %d entries", 2));
    }
}
