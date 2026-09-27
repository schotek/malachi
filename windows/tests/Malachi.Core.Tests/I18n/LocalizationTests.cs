// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/LocalizationTests.swift
// (LocalizationTests) and of ui/internal/i18n/i18n_test.go (TestLocaleDir,
// TestUnboundReturnsMsgid). The Swift suite writes a Czech .lproj as
// po2strings.py would; the Windows catalogue is the .po itself, so the same
// entries are written as cs.po. The process-wide L10n.Catalogue is never
// reassigned here: tests run in parallel and others expect English.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using Malachi.Core.I18n;
using Microsoft.Extensions.Logging;
using Xunit;

namespace Malachi.Core.Tests.I18n;

public sealed class LocalizationTests : IDisposable
{
    private const string InfoMsgid = "Connected to malachid %s (pid %d)";

    private const string Header = """
        msgid ""
        msgstr ""
        "Content-Type: text/plain; charset=UTF-8\n"
        "Plural-Forms: nplurals=3; plural=(n==1) ? 0 : (n>=2 && n<=4) ? 1 : 2;\n"


        """;

    // The entries of the Swift suite's Czech .lproj.
    private const string CzechPo = Header + """
        msgid "Connected"
        msgstr "Připojeno"

        #, c-format
        msgid "Connected to malachid %s (pid %d)"
        msgstr "Připojeno k malachid %s (pid %d)"

        #, c-format
        msgid "Protocol mismatch: UI %d, backend %d"
        msgstr "Nesouhlasí verze protokolu: UI %d, backend %d"

        msgctxt "folder"
        msgid "Inbox"
        msgstr "Doručená pošta"

        msgctxt "participant list separator"
        msgid ", "
        msgstr ", "

        msgid "Line\nbreak \"quoted\""
        msgstr "Zalomený\nřádek \"v uvozovkách\""

        #, no-c-format
        msgid "%-d %b"
        msgstr "%-d. %-m."

        #, c-format
        msgid "%d message"
        msgid_plural "%d messages"
        msgstr[0] "%d zpráva"
        msgstr[1] "%d zprávy"
        msgstr[2] "%d zpráv"

        """;

    private readonly List<string> roots = [];

    public void Dispose()
    {
        foreach (var root in roots)
        {
            Directory.Delete(root, recursive: true);
        }
    }

    [Fact]
    public void EnglishFallback()
    {
        var en = Catalogue.English;
        Assert.True(en.IsEmpty);
        Assert.Equal("en", en.Language);
        Assert.Equal("Connected", en.Translate("Connected"));
        Assert.Equal("Connected to malachid 1.2 (pid 42)", en.Translate(InfoMsgid, "1.2", 42));
        Assert.Equal("Inbox", en.Context("folder", "Inbox"));
        Assert.Equal("1 message", en.Plural("%d message", "%d messages", 1));
        Assert.Equal("2 messages", en.Plural("%d message", "%d messages", 2));
        Assert.Equal("0 messages", en.Plural("%d message", "%d messages", 0));
        Assert.Equal("-1 message", en.Plural("%d message", "%d messages", -1));
        // A plural msgid with more than the one %d: n picks, args fill.
        Assert.Equal("1 unread of 1234", en.Plural("%d unread of %d", "%d unread of %d", 1, 1, 1234));
        Assert.Equal("12 unread of 1234", en.Plural("%d unread of %d", "%d unread of %d", 12, 12, 1234));
    }

    [Fact]
    public void GlobalShimDefaultsToEnglish()
    {
        Assert.True(L10n.Catalogue.IsEmpty);
        Assert.Equal("Connected", L10n.T("Connected"));
        Assert.Equal("Connected to malachid 1.2 (pid 42)", L10n.T(InfoMsgid, "1.2", 42));
        Assert.Equal("1 message", L10n.N("%d message", "%d messages", 1));
        Assert.Equal("5 messages", L10n.N("%d message", "%d messages", 5));
        Assert.Equal("3 unread of 40", L10n.N("%d unread of %d", "%d unread of %d", 3, 3, 40));
        Assert.Equal("Inbox", L10n.C("folder", "Inbox"));
        Assert.Equal(", ", L10n.C("participant list separator", ", "));
        Assert.Equal("1 of 3 attachments could not be saved", L10n.Format("%d of %d attachments could not be saved", 1, 3));
        Assert.Equal("1.5 MiB", L10n.Format("%.1f MiB", 1.5));
        Assert.Equal("13 KiB", L10n.Format("%.0f KiB", 12.6));
        Assert.Equal("7 B", L10n.T("%d B", 7));
    }

    [Fact]
    public void LoadsACatalogueFromAnLproj()
    {
        var root = CzechRoot();

        Assert.Equal(["cs"], Catalogue.AvailableLanguages(root));
        var cs = Catalogue.Load(root, ["de", "cs", "en"]);
        Assert.Equal("cs", cs.Language);
        Assert.False(cs.IsEmpty);
        Assert.Equal("Připojeno", cs.Translate("Connected"));
        Assert.Equal("Připojeno k malachid 1.2 (pid 42)", cs.Translate(InfoMsgid, "1.2", 42));
        Assert.Equal("Nesouhlasí verze protokolu: UI 1, backend 2", cs.Translate("Protocol mismatch: UI %d, backend %d", 1, 2));
        Assert.Equal("Zalomený\nřádek \"v uvozovkách\"", cs.Translate("Line\nbreak \"quoted\""));
        // Missing keys fall back to the msgid, formatted from gettext syntax.
        Assert.Equal("Not in the catalogue", cs.Translate("Not in the catalogue"));
        Assert.Equal("3 unread", cs.Translate("%d unread", 3));
        // strftime msgids come back verbatim for Strftime.
        Assert.Equal("%-d. %-m.", cs.Translate("%-d %b"));
    }

    [Fact]
    public void ContextKeysUseTheEOTSeparator()
    {
        var cs = Catalogue.Load(CzechRoot(), ["cs"]);
        Assert.Equal("folder\u0004Inbox", Catalogue.ContextKey("folder", "Inbox"));
        Assert.Equal("Doručená pošta", cs.Lookup("folder\u0004Inbox"));
        Assert.Equal("Doručená pošta", cs.Context("folder", "Inbox"));
        Assert.Equal(", ", cs.Context("participant list separator", ", "));
        Assert.Equal("Inbox", cs.Context("nonexistent", "Inbox"));
        // The plain key is not the context key.
        Assert.Equal("Inbox", cs.Translate("Inbox"));
    }

    [Fact]
    public void CzechPlurals()
    {
        var cs = Catalogue.Load(CzechRoot(), ["cs"]);
        Assert.Equal(["few", "one", "other"], cs.PluralForms("%d message")!.Keys.Order(StringComparer.Ordinal));
        Assert.Equal("1 zpráva", cs.Plural("%d message", "%d messages", 1));
        Assert.Equal("2 zprávy", cs.Plural("%d message", "%d messages", 2));
        Assert.Equal("4 zprávy", cs.Plural("%d message", "%d messages", 4));
        Assert.Equal("5 zpráv", cs.Plural("%d message", "%d messages", 5));
        Assert.Equal("0 zpráv", cs.Plural("%d message", "%d messages", 0));
        // An untranslated plural uses the English rule on the msgids.
        Assert.Equal("2 attachments", cs.Plural("%d attachment", "%d attachments", 2));
        Assert.Equal("1 attachment", cs.Plural("%d attachment", "%d attachments", 1));
    }

    [Fact]
    public void MissingLanguageIsEnglish()
    {
        var root = CzechRoot();
        var none = Catalogue.Load(root, ["de", "fr"]);
        Assert.True(none.IsEmpty);
        Assert.Equal("en", none.Language);
        Assert.True(Catalogue.Load(root, []).IsEmpty);
        // Only files named <lang>.po count: not a directory, not another file.
        Directory.CreateDirectory(Path.Combine(root, "de.po"));
        File.WriteAllText(Path.Combine(root, "fr.txt"), "msgid \"\"");
        Assert.Equal(["cs"], Catalogue.AvailableLanguages(root));
        Assert.True(Catalogue.Load(root, ["de"]).IsEmpty);
        // English needs no file and ends the search (Windows: English is
        // always available, so an English user never gets Czech).
        Assert.True(Catalogue.Load(root, ["en", "cs"]).IsEmpty);
        Assert.True(Catalogue.Load(root, ["en-GB", "cs"]).IsEmpty);
    }

    [Fact]
    public void DefaultCatalogueHonoursTheLocaleDir()
    {
        var root = CzechRoot();
        var empty = TempRoot();
        var app = TempRoot();
        var czech = new Preferences("cs-CZ");

        Assert.True(Catalogue.Default(Env(empty), czech, app).IsEmpty);
        Assert.True(Catalogue.Default(Env(null), czech, app).IsEmpty);
        // The only translation available is picked for a Czech user.
        var picked = Catalogue.Default(Env(root), czech, app);
        Assert.Equal("cs", picked.Language);
        Assert.Equal("Připojeno", picked.Translate("Connected"));
        // Windows: English is always available, so an English user stays
        // English even when Czech is the only file.
        Assert.True(Catalogue.Default(Env(root), new Preferences("en-US", "cs-CZ"), app).IsEmpty);
        // The catalogues beside the executable come first.
        Directory.CreateDirectory(Path.Combine(app, "locale"));
        File.WriteAllText(Path.Combine(app, "locale", "cs.po"), Header + "msgid \"Connected\"\nmsgstr \"Připojeno (aplikace)\"\n", Encoding.UTF8);
        Assert.Equal("Připojeno (aplikace)", Catalogue.Default(Env(root), czech, app).Translate("Connected"));
    }

    // The language the catalogue would pick: the counterpart of Foundation's
    // matching of the user's languages against the available ones.
    [Fact]
    public void LanguageMatchingFollowsFoundation()
    {
        string[] available = ["cs", "en"];
        Assert.Equal("cs", Catalogue.PreferredLocalizations(available, ["cs"])[0]);
        Assert.Equal("cs", Catalogue.PreferredLocalizations(available, ["cs-CZ"])[0]);
        Assert.Equal("en", Catalogue.PreferredLocalizations(available, ["en-GB"])[0]);
        Assert.Equal("cs", Catalogue.PreferredLocalizations(available, ["de", "cs"])[0]);
        // An unknown language yields something from the list, never nothing.
        var unknown = Catalogue.PreferredLocalizations(available, ["tlh"]);
        Assert.True(unknown.Count > 0 && unknown.All(available.Contains));
        // English is always there and closes the list; a region of the same
        // language is taken when the exact one is missing.
        Assert.Equal(["cs", "en"], Catalogue.PreferredLocalizations(["cs"], ["cs_CZ"]));
        Assert.Equal(["en"], Catalogue.PreferredLocalizations(["cs"], []));
        Assert.Equal(["pt_BR", "en"], Catalogue.PreferredLocalizations(["pt_BR"], ["pt-PT"]));
        Assert.Equal(["pt_BR", "pt", "en"], Catalogue.PreferredLocalizations(["pt", "pt_BR"], ["pt-BR"]));
        Assert.Equal(["en", "cs"], Catalogue.PreferredLocalizations(["cs"], ["en-US", "cs-CZ"]));
    }

    // ui/internal/i18n TestLocaleDir: where the catalogues are looked for.
    [Fact]
    public void LocaleDir()
    {
        var app = Path.Combine(Path.GetTempPath(), "app");
        Assert.Equal([Path.Combine(app, "locale"), @"C:\x"], Catalogue.LocaleDirectories(Env(@"C:\x"), app));
        Assert.Equal([Path.Combine(app, "locale")], Catalogue.LocaleDirectories(Env(""), app));
        Assert.Equal([Path.Combine(app, "locale")], Catalogue.LocaleDirectories(Env(null), app));
        Assert.Equal(Path.Combine(AppContext.BaseDirectory, "locale"), Catalogue.LocaleDirectories(Env(null))[0]);
    }

    // ui/internal/i18n TestUnboundReturnsMsgid: without a catalogue the
    // helpers return the msgid, so tests that never load one are
    // deterministic.
    [Fact]
    public void UnboundReturnsMsgid()
    {
        Assert.Equal("Hello", L10n.T("Hello"));
        Assert.Equal("one", L10n.N("one", "many", 1));
        Assert.Equal("many", L10n.N("one", "many", 2));
        Assert.Equal("Hello", L10n.C("ctx", "Hello"));
    }

    // A translation whose printf directives differ from its msgid's would
    // lose or garble an argument; it is dropped and the msgid shown.
    [Fact]
    public void TranslationsWithOtherDirectivesAreDropped()
    {
        var root = TempRoot();
        File.WriteAllText(Path.Combine(root, "cs.po"), Header + """
            #, c-format
            msgid "%s of %d"
            msgstr "%d z %s"

            #, c-format
            msgid "Deleted %d"
            msgstr "Smazáno"

            #, c-format
            msgid "%s in %s"
            msgstr "%2$s: %1$s"

            msgid "Zoom"
            msgstr "Lupa 100 %"

            #, c-format
            msgid "%d file"
            msgid_plural "%d files"
            msgstr[0] "jeden soubor"
            msgstr[1] "%d soubory"
            msgstr[2] "%d souborů"

            #, c-format
            msgid "%d folder"
            msgid_plural "%d folders"
            msgstr[0] "%d složka"
            msgstr[1] "%s složky"
            msgstr[2] "%d složek"

            #, no-c-format
            msgid "%H:%M"
            msgstr "%H.%M h"

            """, Encoding.UTF8);
        var logger = new ListLogger();
        var cs = Catalogue.Load(root, ["cs"], logger);
        Assert.Equal("cs", cs.Language);
        Assert.Equal(["%s of %d", "Deleted %d", "%d folder"], cs.Rejected);
        Assert.Equal("x of 3", cs.Translate("%s of %d", "x", 3));
        Assert.Equal("Deleted 2", cs.Translate("Deleted %d", 2));
        Assert.Equal("Inbox: Ann", cs.Translate("%s in %s", "Ann", "Inbox"));
        Assert.Equal("Lupa 100 %", cs.Translate("Zoom"));
        Assert.Equal("jeden soubor", cs.Plural("%d file", "%d files", 1));
        Assert.Equal("3 soubory", cs.Plural("%d file", "%d files", 3));
        Assert.Equal("3 folders", cs.Plural("%d folder", "%d folders", 3));
        Assert.Equal("%H.%M h", cs.Translate("%H:%M"));
        Assert.Contains(logger.Messages, m => m.Contains("3 translations dropped", StringComparison.Ordinal));
    }

    // A catalogue that cannot be used is reported and English takes over;
    // loading never throws.
    [Fact]
    public void BrokenCataloguesFallBackToEnglish()
    {
        var root = TempRoot();
        var logger = new ListLogger();
        File.WriteAllText(Path.Combine(root, "cs.po"), "msgid \"bad \\q\"\nmsgstr \"\"\n");
        Assert.True(Catalogue.Load(root, ["cs"], logger).IsEmpty);
        File.WriteAllText(Path.Combine(root, "tlh.po"), Header + "msgid \"x\"\nmsgstr \"y\"\n");
        Assert.True(Catalogue.Load(root, ["tlh"], logger).IsEmpty);
        File.WriteAllText(Path.Combine(root, "de.po"), Header + "msgid \"x\"\nmsgstr \"y\"\n");
        Assert.True(Catalogue.Load(root, ["de"], logger).IsEmpty);
        File.WriteAllBytes(Path.Combine(root, "pl.po"), [0x6D, 0x73, 0x67, 0x69, 0x64, 0x20, 0x22, 0xFF, 0x22, 0x0A]);
        Assert.True(Catalogue.Load(root, ["pl"], logger).IsEmpty);
        Assert.Equal(4, logger.Messages.Count);
        Assert.Contains(logger.Messages, m => m.Contains("unknown escape", StringComparison.Ordinal));
        Assert.Contains(logger.Messages, m => m.Contains("no plural table for language 'tlh'", StringComparison.Ordinal));
        Assert.Contains(logger.Messages, m => m.Contains("nplurals=3", StringComparison.Ordinal));
        // A broken preferred catalogue gives way to the next preference.
        File.WriteAllText(Path.Combine(root, "sk.po"), Header + "msgid \"Connected\"\nmsgstr \"Pripojené\"\n", Encoding.UTF8);
        Assert.Equal("sk", Catalogue.Load(root, ["cs", "sk"], logger).Language);
        // Paths are not languages.
        Assert.True(Catalogue.Load(root, [@"..\cs", "", "c:s"], logger).IsEmpty);
    }

    private static Dictionary<string, string> Env(string? localeDir) =>
        localeDir is null ? [] : new() { [Catalogue.LocaleDirEnv] = localeDir };

    private string CzechRoot()
    {
        var root = TempRoot();
        File.WriteAllText(Path.Combine(root, "cs.po"), CzechPo, Encoding.UTF8);
        return root;
    }

    private string TempRoot()
    {
        var root = Path.Combine(Path.GetTempPath(), "malachi-l10n-" + Guid.NewGuid().ToString("N")[..8]);
        Directory.CreateDirectory(root);
        roots.Add(root);
        return root;
    }

    private sealed class Preferences(params string[] languages) : IPreferredLanguages
    {
        public IReadOnlyList<string> Languages { get; } = languages;
    }

    private sealed class ListLogger : ILogger
    {
        public List<string> Messages { get; } = [];

        public IDisposable? BeginScope<TState>(TState state)
            where TState : notnull => null;

        public bool IsEnabled(LogLevel logLevel) => true;

        public void Log<TState>(LogLevel logLevel, EventId eventId, TState state, Exception? exception, Func<TState, Exception?, string> formatter) =>
            Messages.Add(formatter(state, exception));
    }
}
