// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/scripts/test_po2strings.py (ParserTests, RealCatalogueTests,
// and test_nplurals_mismatch_is_an_error of OutputTests) over PoFile and
// Catalogue, which do at run time what po2strings.py does at build time.
// Not ported: FormatTests (Foundation conversion; GettextFormatTests covers
// the formats) and the rest of OutputTests (.strings/.stringsdict files,
// which Windows does not write). The values stay in gettext syntax here:
// where the script expects %@ and %ld, these expect %s and %d.

using System;
using System.IO;
using System.Linq;
using Malachi.Core.I18n;
using Xunit;

namespace Malachi.Core.Tests.I18n;

public sealed class PoFileTests
{
    private const string Header = """
        msgid ""
        msgstr ""
        "Content-Type: text/plain; charset=UTF-8\n"
        "Plural-Forms: nplurals=3; plural=(n==1) ? 0 : (n>=2 && n<=4) ? 1 : 2;\n"


        """;

    [Fact]
    public void MultilineAndEscapes()
    {
        var text = Header + """
            #: a.go:1
            msgid ""
            "Line one\n"
            "Line \"two\" \\ back\ttab"
            msgstr ""
            "Řádek jedna\n"
            "Řádek dva"

            """;
        var e = Assert.Single(Entries(text));
        Assert.Equal("Line one\nLine \"two\" \\ back\ttab", e.Msgid);
        Assert.Equal("Řádek jedna\nŘádek dva", e.Msgstr);
    }

    [Fact]
    public void HeaderPluralForms()
    {
        Assert.Equal(3, Parse(Header).NPlurals);
        Assert.Null(Parse("msgid \"\"\nmsgstr \"Plural-Forms: nplurals=INTEGER; plural=EXPRESSION;\\n\"\n").NPlurals);
        Assert.Equal("text/plain; charset=UTF-8", Parse(Header).Header()["Content-Type"]);
    }

    [Fact]
    public void FuzzyAndObsoleteAreSkipped()
    {
        var text = Header + """
            #, fuzzy
            msgid "Fuzzy"
            msgstr "Nejistý"

            #~ msgid "Old"
            #~ msgstr "Starý"

            msgid "Kept"
            msgstr "Zachován"

            """;
        var cs = Catalogue.FromPo("cs", Parse(text));
        Assert.Equal("Zachován", cs.Lookup("Kept"));
        Assert.Null(cs.Lookup("Fuzzy"));
        Assert.Null(cs.Lookup("Old"));
        var kinds = Entries(text).ToDictionary(e => e.Msgid, e => (e.Fuzzy, e.Obsolete));
        Assert.Equal(3, kinds.Count);
        Assert.Equal((true, false), kinds["Fuzzy"]);
        Assert.Equal((false, true), kinds["Old"]);
        Assert.Equal((false, false), kinds["Kept"]);
    }

    [Fact]
    public void ObsoleteCommentFormsAreSkipped()
    {
        // msgmerge keeps the previous msgid and the flags of obsolete entries
        // as "#~|" and "#~," comments; an obsolete plural has "#~ msgstr[i]".
        var text = Header + """
            #~| msgid "Older"
            #~ msgid "Old"
            #~ msgstr "Starý"

            #~, fuzzy
            #~ msgid "Old fuzzy"
            #~ msgstr "Starý nejistý"

            #, fuzzy
            #~ msgid "Old fuzzy too"
            #~ msgstr "Také starý"

            #~ msgid "%d old"
            #~ msgid_plural "%d olds"
            #~ msgstr[0] "a"
            #~ msgstr[1] "b"
            #~ msgstr[2] "c"

            #, fuzzy
            #| msgid "Older kept"
            msgid "Kept fuzzy"
            msgstr "Nejistý"

            msgid "Kept"
            msgstr "Zachován"

            """;
        var cs = Catalogue.FromPo("cs", Parse(text));
        Assert.Equal("Zachován", cs.Lookup("Kept"));
        Assert.Null(cs.PluralForms("%d old"));
        Assert.Equal(1, Count(cs, "Kept", "Old", "Old fuzzy", "Old fuzzy too", "Kept fuzzy"));
        var kinds = Entries(text).ToDictionary(e => e.Msgid, e => (e.Fuzzy, e.Obsolete));
        Assert.Equal((false, true), kinds["Old"]);
        Assert.Equal((true, true), kinds["Old fuzzy"]);
        Assert.Equal((true, true), kinds["Old fuzzy too"]);
        Assert.Equal((false, true), kinds["%d old"]);
        Assert.Equal((true, false), kinds["Kept fuzzy"]);
        Assert.Equal((false, false), kinds["Kept"]);
        Assert.Equal(6, kinds.Count);
    }

    [Fact]
    public void UntranslatedIsSkipped()
    {
        var text = Header + "msgid \"Missing\"\nmsgstr \"\"\n\nmsgid \"%d thing\"\nmsgid_plural \"%d things\"\nmsgstr[0] \"%d věc\"\nmsgstr[1] \"\"\nmsgstr[2] \"\"\n";
        var cs = Catalogue.FromPo("cs", Parse(text));
        Assert.True(cs.IsEmpty);
    }

    [Fact]
    public void ContextKeyUsesEot()
    {
        var text = Header + "msgctxt \"folder\"\nmsgid \"Inbox\"\nmsgstr \"Doručená pošta\"\n";
        var cs = Catalogue.FromPo("cs", Parse(text));
        Assert.Equal("Doručená pošta", cs.Lookup("folder\u0004Inbox"));
        Assert.Equal("folder\u0004Inbox", Assert.Single(Entries(text)).Key);
    }

    [Fact]
    public void TemplateUsesMsgidAsValue()
    {
        var text = "msgid \"\"\nmsgstr \"Plural-Forms: nplurals=INTEGER; plural=EXPRESSION;\\n\"\n\nmsgctxt \"folder\"\nmsgid \"Inbox\"\nmsgstr \"\"\n\nmsgid \"Hi %s\"\nmsgstr \"\"\n";
        var en = Catalogue.FromTemplate(Parse(text));
        Assert.Equal("en", en.Language);
        Assert.Equal("Inbox", en.Lookup(Catalogue.ContextKey("folder", "Inbox")));
        Assert.Equal("Hi %s", en.Lookup("Hi %s"));
    }

    [Fact]
    public void PluralMappingCsAndEn()
    {
        var text = Header + """
            #, c-format
            msgid "%d message"
            msgid_plural "%d messages"
            msgstr[0] "%d zpráva"
            msgstr[1] "%d zprávy"
            msgstr[2] "%d zpráv"

            """;
        var cs = Catalogue.FromPo("cs", Parse(text)).PluralForms("%d message")!;
        Assert.Equal(3, cs.Count);
        Assert.Equal("%d zpráva", cs["one"]);
        Assert.Equal("%d zprávy", cs["few"]);
        Assert.Equal("%d zpráv", cs["other"]);
        var en = Catalogue.FromTemplate(Parse(text)).PluralForms("%d message")!;
        Assert.Equal(2, en.Count);
        Assert.Equal("%d message", en["one"]);
        Assert.Equal("%d messages", en["other"]);
    }

    [Fact]
    public void NoCFormatIsLeftAlone()
    {
        var text = Header + "#, no-c-format\nmsgid \"%-d %b\"\nmsgstr \"%-d. %-m.\"\n\n#, c-format\nmsgid \"%d B\"\nmsgstr \"%d B\"\n";
        var cs = Catalogue.FromPo("cs", Parse(text));
        Assert.Equal("%-d. %-m.", cs.Lookup("%-d %b"));
        Assert.Equal("%d B", cs.Lookup("%d B"));
        Assert.Empty(cs.Rejected);
    }

    [Fact]
    public void BadEscapeIsAnError()
    {
        var e = Assert.Throws<PoFormatException>(() => Parse("msgid \"bad \\q\"\nmsgstr \"\"\n"));
        Assert.Equal(@"<test>:1: unknown escape \q", e.Message);
    }

    [Fact]
    public void UnknownLanguageIsAnError()
    {
        Assert.Throws<PoFormatException>(() => Catalogue.FromPo("tlh", Parse(Header)));
        Assert.False(PluralRules.Knows("tlh"));
        Assert.Equal(["one", "other"], PluralRules.Categories("pt_BR"));
        Assert.Equal(["one", "few", "other"], PluralRules.Categories("cs"));
    }

    [Fact]
    public void NPluralsMismatchIsAnError()
    {
        var e = Assert.Throws<PoFormatException>(() => Catalogue.FromPo("en", Parse(Header + "msgid \"x\"\nmsgstr \"y\"\n"), "en.po"));
        Assert.Equal("en.po: header says nplurals=3, the table for 'en' has 2 forms", e.Message);
    }

    [Fact]
    public void PotParsesAndHasPlurals()
    {
        var en = Catalogue.FromTemplate(PoFile.Parse(File.ReadAllText(RepositoryPo.Pot), RepositoryPo.Pot));
        var forms = en.PluralForms("%d message")!;
        Assert.Equal("%d message", forms["one"]);
        Assert.Equal("%d messages", forms["other"]);
        Assert.Equal("Inbox", en.Lookup(Catalogue.ContextKey("folder", "Inbox")));
        Assert.Equal("Connected to malachid %s (pid %d)", en.Lookup("Connected to malachid %s (pid %d)"));
        Assert.Equal("%-d %b", en.Lookup("%-d %b"));
        Assert.Equal("%a, %-d %b %Y at %H:%M", en.Lookup("%a, %-d %b %Y at %H:%M"));
    }

    [Fact]
    public void CsRoundTrip()
    {
        var po = PoFile.Parse(File.ReadAllText(RepositoryPo.CsPo), RepositoryPo.CsPo);
        Assert.Equal(3, po.NPlurals);
        var cs = Catalogue.FromPo("cs", po);
        var forms = cs.PluralForms("%d message")!;
        Assert.Equal("%d zpráva", forms["one"]);
        Assert.Equal("%d zprávy", forms["few"]);
        Assert.Equal("%d zpráv", forms["other"]);
        Assert.Equal("Doručená pošta", cs.Lookup(Catalogue.ContextKey("folder", "Inbox")));
        Assert.Equal("Připojeno k malachid %s (pid %d)", cs.Lookup("Connected to malachid %s (pid %d)"));
        Assert.Equal("%a %-d. %-m. %Y v %H:%M", cs.Lookup("%a, %-d %b %Y at %H:%M"));
        Assert.Null(cs.Lookup("Project bootstrap. Nothing works yet.")); // obsolete
    }

    // Windows: the comments that open an entry stay with it when a msgctxt
    // follows (po2strings.py closed the entry there and lost its flags), so
    // a fuzzy context entry is left out as any fuzzy entry is.
    [Fact]
    public void FlagsBeforeAContextStayWithTheEntry()
    {
        var text = Header + """
            #: ui/internal/window/folders.go:1
            #, fuzzy
            msgctxt "folder"
            msgid "Trash"
            msgstr "Odpadky"

            #. TRANSLATORS: a comment
            #, c-format
            msgctxt "button"
            msgid "Delete %s"
            msgstr "Smazat %s"

            """;
        var entries = Entries(text);
        Assert.Equal(2, entries.Length);
        Assert.True(entries[0].Fuzzy);
        Assert.Equal("folder", entries[0].Msgctxt);
        Assert.Contains("c-format", entries[1].Flags);
        var cs = Catalogue.FromPo("cs", Parse(text));
        Assert.Null(cs.Lookup(Catalogue.ContextKey("folder", "Trash")));
        Assert.Equal("Smazat %s", cs.Lookup(Catalogue.ContextKey("button", "Delete %s")));
    }

    [Fact]
    public void MalformedFilesNameTheLine()
    {
        Assert.Equal(
            "<test>:1: continuation string without a keyword",
            Assert.Throws<PoFormatException>(() => Parse("\"orphan\"\n")).Message);
        Assert.Equal(
            "<test>:2: cannot parse msgstr",
            Assert.Throws<PoFormatException>(() => Parse("msgid \"x\"\nmsgstr\n")).Message);
        Assert.Equal(
            "<test>:2: duplicate msgid in one entry",
            Assert.Throws<PoFormatException>(() => Parse("msgid \"x\"\nmsgid \"y\"\n")).Message);
        Assert.Equal(
            "<test>:1: trailing backslash",
            Assert.Throws<PoFormatException>(() => Parse("msgid \"x\\\"\n")).Message);
        Assert.Equal(
            @"<test>:1: bad \x escape",
            Assert.Throws<PoFormatException>(() => Parse("msgid \"\\xg\"\n")).Message);
        Assert.Equal(
            "<test>:1: bad plural index 99999999999",
            Assert.Throws<PoFormatException>(() => Parse("msgstr[99999999999] \"x\"\n")).Message);
        Assert.Throws<PoFormatException>(() => Catalogue.FromPo("cs", Parse(Header + "msgid \"x\"\nmsgstr \"a\"\n\nmsgid \"x\"\nmsgstr \"b\"\n")));
        Assert.Throws<PoFormatException>(() => Catalogue.FromPo("cs", Parse(
            Header + "msgctxt \"c\"\nmsgid \"%d x\"\nmsgid_plural \"%d xs\"\nmsgstr[0] \"a\"\nmsgstr[1] \"b\"\nmsgstr[2] \"c\"\n")));
    }

    [Fact]
    public void EscapesAndLineEnds()
    {
        var octal = Assert.Single(Entries("msgid \"\\101\\x42\\a\\b\\f\\v\\r\"\nmsgstr \"x\"\n"));
        Assert.Equal("AB\a\b\f\v\r", octal.Msgid);
        // CRLF files parse as LF ones; a later msgid without a blank line
        // starts a new entry, and so does a comment after a msgstr.
        var entries = Entries(Header.Replace("\n", "\r\n", StringComparison.Ordinal) + "msgid \"a\"\r\nmsgstr \"b\"\r\nmsgid \"c\"\r\nmsgstr \"d\"\r\n#, fuzzy\r\nmsgid \"e\"\r\nmsgstr \"f\"");
        Assert.Equal(["a", "c", "e"], entries.Select(e => e.Msgid));
        Assert.Equal([6, 8, 10], entries.Select(e => e.Line));
        Assert.True(entries[2].Fuzzy);
        Assert.Equal(3, Parse(Header.Replace("\n", "\r\n", StringComparison.Ordinal)).NPlurals);
    }

    private static int Count(Catalogue catalogue, params string[] keys) => keys.Count(k => catalogue.Lookup(k) is not null);

    private static PoFile Parse(string text) => PoFile.Parse(text, "<test>");

    private static PoEntry[] Entries(string text) => [.. Parse(text).Entries.Where(e => !e.IsHeader)];
}
