// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/LocalizationTests.swift
// (GeneratedLocalizationTests) and of ui/internal/i18n/i18n_test.go
// (TestCzechCatalogue). macOS reads the .lproj catalogues make locale
// generates and skips without them; Windows reads po/cs.po itself, and
// po/malachi.pot for the English template macOS turns into en.lproj, so
// these tests always run.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Linq;
using Malachi.Core.I18n;
using Xunit;

namespace Malachi.Core.Tests.I18n;

public sealed class GeneratedLocalizationTests
{
    // 2026-09-02 15:04 in Prague, a Wednesday.
    private static readonly TimeZoneInfo Prague = TimeZoneInfo.FindSystemTimeZoneById("Europe/Prague");
    private static readonly DateTimeOffset Fixed = new(2026, 9, 2, 15, 4, 0, TimeSpan.FromHours(2));

    private static readonly Lazy<Catalogue> Czech = new(() => Catalogue.Load(RepositoryPo.Directory, ["cs"]));
    private static readonly Lazy<Catalogue> Template = new(() =>
        Catalogue.FromTemplate(PoFile.Parse(File.ReadAllText(RepositoryPo.Pot), RepositoryPo.Pot), RepositoryPo.Pot));

    private static Catalogue Cs => Czech.Value;

    private static Catalogue En => Template.Value;

    [Fact]
    public void PlainMsgids()
    {
        var cs = Cs;
        Assert.Equal("cs", cs.Language);
        Assert.Equal("Démon není dostupný", cs.Translate("Backend unavailable"));
        Assert.Equal("Použít", cs.Translate("Use"));
        Assert.Equal("Připojování k démonu…", cs.Translate("Connecting to backend…"));
        // The catalogue keeps gettext's %s and %d; GettextFormat formats them.
        Assert.Equal("Připojeno k malachid 1.2 (pid 42)", cs.Translate("Connected to malachid %s (pid %d)", "1.2", 42));
        Assert.Equal("1 z 3 příloh se nepodařilo uložit", cs.Translate("%d of %d attachments could not be saved", 1, 3));
        Assert.Equal("Synchronizuje se %s… %d %%", cs.Lookup("Syncing %s… %d %%"));
        Assert.Equal("Synchronizuje se Inbox… 5 %", cs.Translate("Syncing %s… %d %%", "Inbox", 5));
    }

    [Fact]
    public void ContextKeys()
    {
        var cs = Cs;
        Assert.Equal("Doručená pošta", cs.Context("folder", "Inbox"));
        Assert.Equal("Koš", cs.Context("folder", "Trash"));
        Assert.Equal("Koncepty", cs.Context("folder", "Drafts"));
        Assert.Equal(", ", cs.Context("participant list separator", ", "));
        Assert.Equal("Doručená pošta", cs.Lookup(Catalogue.ContextKey("folder", "Inbox")));
        // English keeps the context keys too (they must resolve to the plain msgid).
        Assert.Equal("Inbox", En.Context("folder", "Inbox"));
        Assert.Equal("Inbox", En.Lookup(Catalogue.ContextKey("folder", "Inbox")));
        Assert.Equal("Inbox", Catalogue.English.Context("folder", "Inbox"));
    }

    [Fact]
    public void PluralForms()
    {
        var cs = Cs;
        Assert.Equal(["few", "one", "other"], cs.PluralForms("%d message")!.Keys.Order(StringComparer.Ordinal));
        Assert.Equal("1 zpráva", cs.Plural("%d message", "%d messages", 1));
        Assert.Equal("2 zprávy", cs.Plural("%d message", "%d messages", 2));
        Assert.Equal("5 zpráv", cs.Plural("%d message", "%d messages", 5));
        Assert.Equal(
            "3 nebezpečné prvky byly ze zprávy odstraněny",
            cs.Plural("%d unsafe element was removed from the message", "%d unsafe elements were removed from the message", 3));
        // Two numbers, the form by the first (folders.go folderCountsText).
        Assert.Equal("1 nepřečtená z 1234", cs.Plural("%d unread of %d", "%d unread of %d", 1, 1, 1234));
        Assert.Equal("3 nepřečtené z 1234", cs.Plural("%d unread of %d", "%d unread of %d", 3, 3, 1234));
        Assert.Equal("12 nepřečtených z 1234", cs.Plural("%d unread of %d", "%d unread of %d", 12, 12, 1234));
        Assert.Equal(["one", "other"], En.PluralForms("%d message")!.Keys.Order(StringComparer.Ordinal));
        Assert.Equal("1 message", En.Plural("%d message", "%d messages", 1));
        Assert.Equal("2 messages", En.Plural("%d message", "%d messages", 2));
        Assert.Equal("5 messages", En.Plural("%d message", "%d messages", 5));
    }

    [Fact]
    public void DateMsgidsThroughStrftime()
    {
        var cs = Cs;
        // The four msgids of ui/internal/widget/format.go and their cs.po translations.
        Assert.Equal("%H:%M", cs.Translate("%H:%M"));
        Assert.Equal("%-d. %-m.", cs.Translate("%-d %b"));
        Assert.Equal("%-d. %-m. %Y", cs.Translate("%Y-%m-%d"));
        Assert.Equal("%a %-d. %-m. %Y v %H:%M", cs.Translate("%a, %-d %b %Y at %H:%M"));
        Assert.Equal("15:04", Render(cs.Translate("%H:%M"), "cs-CZ"));
        Assert.Equal("2. 9.", Render(cs.Translate("%-d %b"), "cs-CZ"));
        Assert.Equal("2. 9. 2026", Render(cs.Translate("%Y-%m-%d"), "cs-CZ"));
        Assert.Equal("st 2. 9. 2026 v 15:04", Render(cs.Translate("%a, %-d %b %Y at %H:%M"), "cs-CZ"));
        Assert.Equal("Wed, 2 Sep 2026 at 15:04", Render(En.Translate("%a, %-d %b %Y at %H:%M"), "en-US"));
    }

    [Fact]
    public void MissingKeysFallBackToTheMsgid()
    {
        var cs = Cs;
        Assert.Equal("Not a msgid of Malachi Mail", cs.Translate("Not a msgid of Malachi Mail"));
        Assert.Equal("Not a msgid with x", cs.Translate("Not a msgid with %s", "x"));
        Assert.Equal("Not a folder", cs.Context("folder", "Not a folder"));
        Assert.Equal("2 widgets", cs.Plural("%d widget", "%d widgets", 2));
        Assert.Equal("1 widget", cs.Plural("%d widget", "%d widgets", 1));
    }

    [Fact]
    public void DefaultPicksFromTheLocaleDir()
    {
        var environment = new Dictionary<string, string> { [Catalogue.LocaleDirEnv] = RepositoryPo.Directory };
        var picked = Catalogue.Default(environment, new Preferences("cs-CZ"), appDirectory: RepositoryPo.Directory);
        Assert.False(picked.IsEmpty);
        Assert.Equal("cs", picked.Language);
        // Whichever language is picked, the context key resolves, so the
        // choice cannot break folder names.
        string[] languages = ["cs-CZ", "en-US", "de-DE"];
        string[] inbox = ["Inbox", "Doručená pošta"];
        foreach (var language in languages)
        {
            var any = Catalogue.Default(environment, new Preferences(language), appDirectory: RepositoryPo.Directory);
            Assert.Contains(any.Context("folder", "Inbox"), inbox);
        }
    }

    // ui/internal/i18n TestCzechCatalogue: a translation end to end, and the
    // plural form 2-4 as gettext hands it out (unformatted).
    [Fact]
    public void CzechCatalogue()
    {
        var cs = Cs;
        Assert.Equal("Démon není dostupný", cs.Translate("Backend unavailable"));
        Assert.Equal(
            "%d nebezpečné prvky byly ze zprávy odstraněny",
            cs.PluralForms("%d unsafe element was removed from the message")!["few"]);
    }

    // Every translation of po/cs.po passes the load-time check: none is
    // dropped for printf directives that differ from its msgid.
    [Fact]
    public void EveryCzechTranslationIsUsed()
    {
        Assert.Empty(Cs.Rejected);
    }

    // Every language po/LINGUAS ships is one the plural table knows, with the
    // header's nplurals (po2strings.py refuses anything else, and so does
    // Catalogue).
    [Fact]
    public void EveryShippedLanguageLoads()
    {
        var languages = File.ReadAllLines(RepositoryPo.Linguas)
            .Select(line => line.Split('#')[0].Trim())
            .Where(line => line.Length > 0)
            .SelectMany(line => line.Split((char[]?)null, StringSplitOptions.RemoveEmptyEntries))
            .ToList();
        Assert.NotEmpty(languages);
        foreach (var language in languages)
        {
            Assert.True(PluralRules.Knows(language), language);
            var path = Path.Combine(RepositoryPo.Directory, language + ".po");
            var po = PoFile.Parse(File.ReadAllText(path), path);
            Assert.Equal(PluralRules.Categories(language).Count, po.NPlurals);
            var catalogue = Catalogue.FromPo(language, po, path);
            Assert.False(catalogue.IsEmpty);
            Assert.Empty(catalogue.Rejected);
        }
    }

    private static string Render(string strftime, string locale) =>
        Strftime.Format(Fixed, strftime, CultureInfo.GetCultureInfo(locale), Prague);

    private sealed class Preferences(params string[] languages) : IPreferredLanguages
    {
        public IReadOnlyList<string> Languages { get; } = languages;
    }
}
