// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/I18n/PluralRules.swift; the languages it
// knows are PLURAL_CATEGORIES of macos/scripts/po2strings.py. GTK: gettext
// evaluates the Plural-Forms expression of the .mo file.
//
// The catalogue picks the plural form itself, so the rule and the category
// table must agree with the one of po2strings.py (a third copy of it). A
// catalogue in a language the table does not know is refused when it is
// loaded (Knows); the lookup itself falls back to the English rule, which is
// also gettext's default.

using System;
using System.Collections.Frozen;
using System.Collections.Generic;

namespace Malachi.Core.I18n;

/// <summary>
/// The gettext plural rules of the languages the catalogues may be
/// translated into, and the CLDR category of each <c>msgstr[i]</c>.
/// </summary>
public static class PluralRules
{
    private static readonly string[] Two = ["one", "other"];
    private static readonly string[] SlavicOther = ["one", "few", "other"];
    private static readonly string[] SlavicMany = ["one", "few", "many"];

    // PLURAL_CATEGORIES of po2strings.py: the languages with a known rule.
    private static readonly FrozenSet<string> Known = FrozenSet.ToFrozenSet(
        ["en", "de", "nl", "sv", "da", "nb", "nn", "fi", "it", "es", "pt", "fr", "cs", "sk", "pl", "ru", "uk", "ro"],
        StringComparer.Ordinal);

    /// <summary>The gettext plural index (<c>msgstr[i]</c>) of <paramref name="n"/> in <paramref name="language"/>.</summary>
    public static int Index(string language, long n)
    {
        // abs(n) without the overflow of long.MinValue.
        var m = n < 0 ? (ulong)(-(n + 1)) + 1 : (ulong)n;
        switch (Base(language))
        {
            case "cs":
            case "sk":
                return m == 1 ? 0 : m is >= 2 and <= 4 ? 1 : 2;
            case "pl":
                return m == 1 ? 0 : FewSlavic(m) ? 1 : 2;
            case "ru":
            case "uk":
                return m % 10 == 1 && m % 100 != 11 ? 0 : FewSlavic(m) ? 1 : 2;
            case "fr":
                return m > 1 ? 1 : 0;
            case "ro":
                return m == 1 ? 0 : m == 0 || (m % 100 > 0 && m % 100 < 20) ? 1 : 2;
            default:
                return m != 1 ? 1 : 0;
        }
    }

    /// <summary>The CLDR category name of <paramref name="n"/>, the key of its plural form.</summary>
    public static string Category(string language, long n)
    {
        var table = Categories(language);
        var i = Index(language, n);
        return i < table.Count ? table[i] : "other";
    }

    /// <summary>Category names in <c>msgstr</c> index order, as po2strings.py writes them.</summary>
    public static IReadOnlyList<string> Categories(string language) => Base(language) switch
    {
        "cs" or "sk" or "ro" => SlavicOther,
        "pl" or "ru" or "uk" => SlavicMany,
        _ => Two,
    };

    /// <summary>
    /// Whether the table has a rule for <paramref name="language"/>; a
    /// catalogue in any other language is an error (po2strings.py
    /// <c>categories_for</c>), never a guess.
    /// </summary>
    public static bool Knows(string language) => Known.Contains(Base(language));

    /// <summary>"cs" for "cs", "cs-CZ", "cs_CZ" and "CS".</summary>
    public static string Base(string language)
    {
        ArgumentNullException.ThrowIfNull(language);
        var cut = language.AsSpan().IndexOfAny('-', '_');
        return (cut < 0 ? language : language[..cut]).ToLowerInvariant();
    }

    private static bool FewSlavic(ulong n) => n % 10 is >= 2 and <= 4 && (n % 100 < 10 || n % 100 >= 20);
}
