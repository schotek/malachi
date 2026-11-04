// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/I18n/Localization.swift (Catalogue) and
// of macos/scripts/po2strings.py (collect, categories_for, convert_po,
// convert_template); GTK: ui/internal/i18n (LocaleDir) and gettext's lookup.
//
// One language's strings, keyed by the GTK msgids verbatim; the value of an
// English lookup is the msgid itself, so English needs no file. macOS
// generates .lproj directories from po/ at build time; Windows reads
// locale\<lang>.po beside the executable (the build copies every po/LINGUAS
// entry there) or MALACHI_LOCALE_DIR at start, with the script's rules:
// header, obsolete, fuzzy and untranslated entries are left out, a plural
// entry only counts with every form, a context entry is keyed
// ctxt + U+0004 + msgid, the header's nplurals must match PluralRules, and a
// language PluralRules does not know is refused. Values stay in gettext
// (Go printf) syntax for GettextFormat; a translation whose directives
// differ from its msgid's is dropped at load, so a broken .po can never make
// a sentence lose or garble an argument (the English msgid is shown instead).

using System;
using System.Collections.Frozen;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using Microsoft.Extensions.Logging;

namespace Malachi.Core.I18n;

/// <summary>One language's strings, the lookups of <see cref="L10n"/>.</summary>
public sealed partial class Catalogue
{
    /// <summary>
    /// The environment variable naming a directory of <c>&lt;lang&gt;.po</c>
    /// files (the repository's po/ works), used when the app folder has no
    /// locale directory.
    /// </summary>
    public const string LocaleDirEnv = "MALACHI_LOCALE_DIR";

    // Invalid UTF-8 is an error, as it is for po2strings.py.
    private static readonly UTF8Encoding StrictUtf8 = new(encoderShouldEmitUTF8Identifier: false, throwOnInvalidBytes: true);

    private readonly FrozenDictionary<string, string> strings;

    // singular msgid -> CLDR category -> gettext format
    private readonly FrozenDictionary<string, FrozenDictionary<string, string>> plurals;

    private Catalogue(
        string language,
        Dictionary<string, string> strings,
        Dictionary<string, FrozenDictionary<string, string>> plurals,
        IReadOnlyList<string> rejected)
    {
        Language = language;
        this.strings = strings.ToFrozenDictionary(StringComparer.Ordinal);
        this.plurals = plurals.ToFrozenDictionary(StringComparer.Ordinal);
        Rejected = rejected;
    }

    /// <summary>No translations: every lookup falls back to the msgid.</summary>
    public static Catalogue English { get; } = new("en", [], [], []);

    /// <summary>The language of the file ("cs"); "en" for <see cref="English"/>.</summary>
    public string Language { get; }

    /// <summary>Whether the catalogue has no entries.</summary>
    public bool IsEmpty => strings.Count == 0 && plurals.Count == 0;

    /// <summary>
    /// The keys whose translations were dropped at load because their printf
    /// directives differ from the msgid's.
    /// </summary>
    public IReadOnlyList<string> Rejected { get; }

    // Lookup

    /// <summary>
    /// The raw catalogue value of <paramref name="key"/> (gettext format), if
    /// any; Swift's <c>string(_:)</c>, renamed because a method named String
    /// reads as the type.
    /// </summary>
    public string? Lookup(string key) => strings.GetValueOrDefault(key);

    /// <summary>The plural forms of <paramref name="singular"/> by CLDR category, if any.</summary>
    public IReadOnlyDictionary<string, string>? PluralForms(string singular) => plurals.GetValueOrDefault(singular);

    /// <summary>Translates <paramref name="msgid"/>.</summary>
    public string Translate(string msgid) => strings.GetValueOrDefault(msgid) ?? msgid;

    /// <summary>
    /// Translates <paramref name="msgid"/> and formats it with
    /// <paramref name="args"/> (<c>fmt.Sprintf(T(...))</c>).
    /// </summary>
    public string Translate(string msgid, params object?[] args) =>
        GettextFormat.Format(strings.GetValueOrDefault(msgid) ?? msgid, args);

    /// <summary>
    /// The plural form for <paramref name="n"/>, formatted with it. Without
    /// an entry the English rule applies to the msgids: <paramref name="singular"/>
    /// for 1 and -1, <paramref name="plural"/> otherwise. A form that leaves
    /// out the count (Czech "jeden soubor") is shown as it is.
    /// </summary>
    public string Plural(string singular, string plural, long n) => Plural(singular, plural, n, [n]);

    /// <summary>
    /// <see cref="Plural(string, string, long)"/> formatted with
    /// <paramref name="args"/> instead of <paramref name="n"/> alone, for a
    /// msgid with several directives ("%d unread of %d"); <paramref name="n"/>
    /// only picks the form.
    /// </summary>
    public string Plural(string singular, string plural, long n, params object?[] args)
    {
        if (plurals.TryGetValue(singular, out var forms))
        {
            var category = PluralRules.Category(Language, n);
            if (forms.TryGetValue(category, out var pattern) || forms.TryGetValue("other", out pattern))
            {
                return GettextFormat.Format(pattern, args, reportExtra: false);
            }
        }
        return GettextFormat.Format(n is 1 or -1 ? singular : plural, args, reportExtra: false);
    }

    /// <summary>Translates <paramref name="msgid"/> disambiguated by <paramref name="context"/> (pgettext).</summary>
    public string Context(string context, string msgid) => strings.GetValueOrDefault(ContextKey(context, msgid)) ?? msgid;

    /// <summary>The key of a context entry, gettext's EOT convention.</summary>
    public static string ContextKey(string context, string msgid) => context + PoEntry.ContextSeparator + msgid;

    // Loading

    /// <summary>
    /// The catalogue of a translation (po2strings.py <c>convert_po</c>).
    /// Throws <see cref="PoFormatException"/> for a language
    /// <see cref="PluralRules"/> does not know, a header whose nplurals does
    /// not match its table, duplicate entries and plural entries with a
    /// context.
    /// </summary>
    public static Catalogue FromPo(string language, PoFile po, string name = "<po>")
    {
        ArgumentNullException.ThrowIfNull(language);
        ArgumentNullException.ThrowIfNull(po);
        if (!PluralRules.Knows(language))
        {
            throw new PoFormatException($"no plural table for language '{language}'; add it to PluralRules");
        }
        var categories = PluralRules.Categories(language);
        if (po.NPlurals is int nplurals && nplurals != categories.Count)
        {
            throw new PoFormatException(
                $"{name}: header says nplurals={nplurals}, the table for '{language}' has {categories.Count} forms");
        }
        return Collect(language, po, categories, template: false, name);
    }

    /// <summary>
    /// The English catalogue of the template, every msgid its own value
    /// (po2strings.py <c>convert_template</c>).
    /// </summary>
    public static Catalogue FromTemplate(PoFile pot, string name = "<pot>")
    {
        ArgumentNullException.ThrowIfNull(pot);
        return Collect("en", pot, PluralRules.Categories("en"), template: true, name);
    }

    /// <summary>
    /// Loads the first of <paramref name="languages"/> that has a readable
    /// <c>&lt;lang&gt;.po</c> in <paramref name="directory"/>; English, which
    /// needs no file, ends the search. <see cref="English"/> when none has.
    /// </summary>
    public static Catalogue Load(string directory, IEnumerable<string> languages, ILogger? logger = null)
    {
        ArgumentNullException.ThrowIfNull(directory);
        ArgumentNullException.ThrowIfNull(languages);
        foreach (var language in languages)
        {
            if (language.Length == 0 || language.Contains("..", StringComparison.Ordinal)
                || language.AsSpan().IndexOfAny(Path.GetInvalidFileNameChars()) >= 0)
            {
                continue;
            }
            var path = Path.Combine(directory, language + ".po");
            if (File.Exists(path))
            {
                try
                {
                    var catalogue = FromPo(language, PoFile.Parse(File.ReadAllText(path, StrictUtf8), path), path);
                    if (logger is not null && catalogue.Rejected.Count > 0)
                    {
                        LogRejected(logger, language, catalogue.Rejected.Count, string.Join(" | ", catalogue.Rejected));
                    }
                    return catalogue;
                }
                catch (Exception e) when (e is PoFormatException or IOException or UnauthorizedAccessException
                                              or DecoderFallbackException)
                {
                    if (logger is not null)
                    {
                        LogNotLoaded(logger, language, e.Message);
                    }
                }
            }
            if (PluralRules.Base(language) == "en")
            {
                return English;
            }
        }
        return English;
    }

    /// <summary>
    /// The catalogue for the user's language: from the first of
    /// <see cref="LocaleDirectories"/> that has any <c>.po</c>, the first
    /// match of <paramref name="preferredLanguages"/>
    /// (<see cref="PreferredLocalizations"/>); English otherwise.
    /// </summary>
    /// <param name="environment">The environment; null reads the process's.</param>
    /// <param name="preferredLanguages">The user's languages; null is the current UI culture.</param>
    /// <param name="appDirectory">The executable's directory; null is <see cref="AppContext.BaseDirectory"/>.</param>
    /// <param name="logger">Where a catalogue that cannot be used is reported.</param>
    public static Catalogue Default(
        IReadOnlyDictionary<string, string>? environment = null,
        IPreferredLanguages? preferredLanguages = null,
        string? appDirectory = null,
        ILogger? logger = null)
    {
        var preferences = (preferredLanguages ?? CurrentUICulturePreferredLanguages.Instance).Languages;
        foreach (var root in LocaleDirectories(environment, appDirectory))
        {
            var available = AvailableLanguages(root);
            if (available.Count == 0)
            {
                continue;
            }
            return Load(root, PreferredLocalizations(available, preferences), logger);
        }
        return English;
    }

    /// <summary>
    /// Where catalogues are looked for, in order: <c>locale</c> beside the
    /// executable, then <see cref="LocaleDirEnv"/> when it is set.
    /// </summary>
    public static IReadOnlyList<string> LocaleDirectories(
        IReadOnlyDictionary<string, string>? environment = null, string? appDirectory = null)
    {
        var result = new List<string> { Path.Combine(appDirectory ?? AppContext.BaseDirectory, "locale") };
        var dir = environment is null
            ? Environment.GetEnvironmentVariable(LocaleDirEnv)
            : environment.GetValueOrDefault(LocaleDirEnv);
        if (!string.IsNullOrEmpty(dir))
        {
            result.Add(dir);
        }
        return result;
    }

    /// <summary>The languages with a <c>&lt;lang&gt;.po</c> in <paramref name="directory"/>, sorted.</summary>
    public static IReadOnlyList<string> AvailableLanguages(string directory)
    {
        ArgumentNullException.ThrowIfNull(directory);
        try
        {
            if (!Directory.Exists(directory))
            {
                return [];
            }
            return Directory.EnumerateFiles(directory, "*.po")
                .Where(path => string.Equals(Path.GetExtension(path), ".po", StringComparison.OrdinalIgnoreCase))
                .Select(path => Path.GetFileNameWithoutExtension(path))
                .Where(language => language.Length > 0)
                .Order(StringComparer.Ordinal)
                .ToList();
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
            return [];
        }
    }

    /// <summary>
    /// The available languages in the order <paramref name="preferences"/>
    /// asks for them (the counterpart of Foundation's
    /// <c>Bundle.preferredLocalizations</c>): for each preference its exact
    /// match, then its bare language ("cs" for "cs-CZ"), then other regions of
    /// it. English is always available, needs no file and closes the list, so
    /// a user who prefers English, or a language nobody translated, never
    /// gets another translation.
    /// </summary>
    public static IReadOnlyList<string> PreferredLocalizations(IEnumerable<string> available, IEnumerable<string> preferences)
    {
        ArgumentNullException.ThrowIfNull(available);
        ArgumentNullException.ThrowIfNull(preferences);
        var candidates = available.Where(a => a.Length > 0).Distinct(StringComparer.Ordinal).ToList();
        var english = candidates.FirstOrDefault(a => Normalize(a) == "en") ?? "en";
        if (!candidates.Contains(english))
        {
            candidates.Add(english);
        }
        var result = new List<string>();
        foreach (var preference in preferences)
        {
            var wanted = Normalize(preference);
            if (wanted.Length == 0)
            {
                continue;
            }
            var language = PluralRules.Base(wanted);
            AddAll(result, candidates.Where(c => Normalize(c) == wanted));
            AddAll(result, candidates.Where(c => Normalize(c) == language));
            AddAll(result, candidates.Where(c => PluralRules.Base(c) == language));
        }
        AddAll(result, [english]);
        return result;
    }

    private static void AddAll(List<string> list, IEnumerable<string> items)
    {
        foreach (var item in items)
        {
            if (!list.Contains(item))
            {
                list.Add(item);
            }
        }
    }

    private static string Normalize(string tag) => tag.Trim().Replace('_', '-').ToLowerInvariant();

    // po2strings.py collect, plus the directive check.
    private static Catalogue Collect(string language, PoFile po, IReadOnlyList<string> categories, bool template, string name)
    {
        var strings = new Dictionary<string, string>(StringComparer.Ordinal);
        var plurals = new Dictionary<string, FrozenDictionary<string, string>>(StringComparer.Ordinal);
        var seenPlurals = new HashSet<string>(StringComparer.Ordinal);
        var seen = new HashSet<string>(StringComparer.Ordinal);
        var rejected = new List<string>();
        foreach (var e in po.Entries)
        {
            if (e.IsHeader || e.Obsolete || e.Fuzzy)
            {
                continue;
            }
            if (e.MsgidPlural is null)
            {
                var value = template ? e.Msgid : e.Msgstr;
                if (value.Length == 0)
                {
                    continue;
                }
                if (!seen.Add(e.Key))
                {
                    throw new PoFormatException($"{name}: duplicate entry '{e.Key}' (line {e.Line})");
                }
                if (!template && ChecksDirectives(e) && !GettextFormat.SameDirectives(e.Msgid, value))
                {
                    rejected.Add(e.Key);
                    continue;
                }
                strings[e.Key] = value;
                continue;
            }
            if (e.Msgctxt is not null)
            {
                throw new PoFormatException(
                    $"{name}: plural entries with msgctxt are not supported ('{e.Msgid}', line {e.Line})");
            }
            string[] forms;
            if (template)
            {
                forms = [e.Msgid, e.MsgidPlural];
            }
            else
            {
                forms = [.. categories.Select((_, i) => e.MsgstrPlural.GetValueOrDefault(i, ""))];
                if (forms.Any(f => f.Length == 0) || e.MsgstrPlural.Count != categories.Count)
                {
                    // Partially translated: msgfmt would reject it; fall back.
                    continue;
                }
            }
            if (!seenPlurals.Add(e.Msgid))
            {
                throw new PoFormatException($"{name}: duplicate plural entry '{e.Msgid}' (line {e.Line})");
            }
            if (!template && ChecksDirectives(e) && !FormsMatch(e, forms, language))
            {
                rejected.Add(e.Msgid);
                continue;
            }
            plurals[e.Msgid] = categories.Zip(forms).ToFrozenDictionary(p => p.First, p => p.Second, StringComparer.Ordinal);
        }
        return new Catalogue(language, strings, plurals, rejected);
    }

    // Every plural form consumes what one of the msgids consumes. As
    // msgfmt -c allows, a form used for a single n ("jeden soubor" for 1 in
    // Czech) may leave out the count when the count is all the msgids
    // consume; Plural does not report the unused count.
    private static bool FormsMatch(PoEntry e, string[] forms, string language)
    {
        var countOnly = GettextFormat.Directives(e.MsgidPlural!) is ["1:d"] && GettextFormat.Directives(e.Msgid) is ["1:d"];
        for (var i = 0; i < forms.Length; i++)
        {
            if (GettextFormat.SameDirectives(forms[i], e.Msgid) || GettextFormat.SameDirectives(forms[i], e.MsgidPlural!))
            {
                continue;
            }
            if (countOnly && GettextFormat.Directives(forms[i]).Count == 0 && SingleValue(language, i))
            {
                continue;
            }
            return false;
        }
        return true;
    }

    // Whether plural form index is used for one n only.
    private static bool SingleValue(string language, int index)
    {
        var count = 0;
        for (var n = 0; n <= 1000 && count < 2; n++)
        {
            if (PluralRules.Index(language, n) == index)
            {
                count++;
            }
        }
        return count == 1;
    }

    // A printf entry: flagged c-format, or not flagged no-c-format and with a
    // directive in its msgids. Other strings are never formatted, so a
    // translation may use '%' freely there ("100 %").
    private static bool ChecksDirectives(PoEntry e) =>
        e.Flags.Contains("c-format")
        || (e.IsPrintfFormat
            && (GettextFormat.Directives(e.Msgid).Count > 0
                || (e.MsgidPlural is not null && GettextFormat.Directives(e.MsgidPlural).Count > 0)));

    [LoggerMessage(Level = LogLevel.Warning, Message = "Catalogue {Language} not loaded: {Reason}")]
    private static partial void LogNotLoaded(ILogger logger, string language, string reason);

    [LoggerMessage(Level = LogLevel.Warning,
        Message = "Catalogue {Language}: {Count} translations dropped, their printf directives differ from the msgid: {Keys}")]
    private static partial void LogRejected(ILogger logger, string language, int count, string keys);
}
