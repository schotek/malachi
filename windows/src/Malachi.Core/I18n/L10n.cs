// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/I18n/Localization.swift (L10n,
// CatalogueBox); GTK: ui/internal/i18n (T, N, C).
//
// The gettext shim of the Windows client. Keys are the GTK msgids verbatim
// (docs/windows-port.md §9); every user-visible string goes through T, N or
// C, and a string with no GTK msgid stays English with the comment
// "Windows-only string". Sentences are never built by concatenation: a
// pattern with arguments is formatted by GettextFormat, as fmt.Sprintf does
// in the GTK UI.

using System.Threading;

namespace Malachi.Core.I18n;

/// <summary>Translates the GTK msgids through the active <see cref="Catalogue"/>.</summary>
public static class L10n
{
    private static Catalogue catalogue = Catalogue.English;

    /// <summary>
    /// The catalogue in use: set once at start (<see cref="Catalogue.Default"/>),
    /// English until then. A catalogue is immutable and replaced whole, so
    /// <see cref="T(string)"/> is callable from any thread.
    /// </summary>
    public static Catalogue Catalogue
    {
        get => Volatile.Read(ref catalogue);
        set => Volatile.Write(ref catalogue, value ?? Catalogue.English);
    }

    /// <summary>Translates <paramref name="msgid"/>.</summary>
    public static string T(string msgid) => Catalogue.Translate(msgid);

    /// <summary>
    /// Translates <paramref name="msgid"/> and formats it with
    /// <paramref name="args"/> (<c>fmt.Sprintf(T(...))</c>).
    /// </summary>
    public static string T(string msgid, params object?[] args) => Catalogue.Translate(msgid, args);

    /// <summary>
    /// Translates the plural form for <paramref name="n"/> and substitutes
    /// <paramref name="n"/> (every plural msgid of the GTK UI carries exactly
    /// one <c>%d</c>). Without a catalogue entry the English rule applies:
    /// <paramref name="singular"/> for 1, <paramref name="plural"/> otherwise.
    /// </summary>
    public static string N(string singular, string plural, long n) => Catalogue.Plural(singular, plural, n);

    /// <summary>
    /// <see cref="N(string, string, long)"/> for a plural msgid with more than
    /// the one <c>%d</c>: the form is chosen for <paramref name="n"/> and
    /// formatted with <paramref name="args"/>, which name every directive,
    /// <paramref name="n"/> included where it appears
    /// (<c>fmt.Sprintf(N(singular, plural, n), args...)</c>).
    /// </summary>
    public static string N(string singular, string plural, long n, params object?[] args) =>
        Catalogue.Plural(singular, plural, n, args);

    /// <summary>Translates <paramref name="msgid"/> disambiguated by <paramref name="context"/> (pgettext).</summary>
    public static string C(string context, string msgid) => Catalogue.Context(context, msgid);

    /// <summary>
    /// Formats a gettext pattern (translated or not) with <paramref name="args"/>,
    /// for callers that fetched the string separately.
    /// </summary>
    public static string Format(string gettextFormat, params object?[] args) => GettextFormat.Format(gettextFormat, args);
}
