// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// A DEVELOPMENT AID, not a feature (docs/screenshots.md §6, docs/windows-port.md
// §9): a desktop app has no per-app language on Windows, it follows the
// display languages (Malachi.Platform.Windows, WindowsPreferredLanguages),
// and changing those means a sign-out. The Windows-only variable
// MALACHI_LANGUAGES stands in for them in an isolated instance (the
// screenshots, a test): BCP 47 tags most preferred first, separated by
// ";" or ",", of which the first is also the regional format (the dates,
// the first day of the week), as LANG does for the GTK client and
// AppleLanguages with AppleLocale for the macOS one. Unset or empty, nothing
// here applies; nothing of it is translated.

using System;
using System.Collections.Generic;
using System.Globalization;

namespace Malachi.Core.I18n;

/// <summary>
/// The user-interface languages <c>MALACHI_LANGUAGES</c> names, and the
/// regional format of the first.
/// </summary>
public sealed class EnvironmentPreferredLanguages : IPreferredLanguages
{
    /// <summary>The variable.</summary>
    public const string Variable = "MALACHI_LANGUAGES";

    private EnvironmentPreferredLanguages(IReadOnlyList<string> languages, CultureInfo? culture)
    {
        Languages = languages;
        Culture = culture;
    }

    /// <summary>The tags, most preferred first; never empty.</summary>
    public IReadOnlyList<string> Languages { get; }

    /// <summary>
    /// The culture of the first tag, for the formats; null when Windows
    /// does not know the tag (the languages still pick the catalogue).
    /// </summary>
    public CultureInfo? Culture { get; }

    /// <summary>
    /// What <see cref="Variable"/> says in <paramref name="environment"/>
    /// (the process's when null); null when it is unset, empty or names no
    /// tag.
    /// </summary>
    public static EnvironmentPreferredLanguages? FromEnvironment(IReadOnlyDictionary<string, string>? environment = null)
    {
        string? value;
        if (environment is null)
        {
            value = Environment.GetEnvironmentVariable(Variable);
        }
        else
        {
            environment.TryGetValue(Variable, out value);
        }
        return Of(value);
    }

    /// <summary>The languages of <paramref name="value"/> (<see cref="Parse"/>); null without any.</summary>
    public static EnvironmentPreferredLanguages? Of(string? value)
    {
        var languages = Parse(value);
        return languages.Count == 0 ? null : new EnvironmentPreferredLanguages(languages, CultureOf(languages[0]));
    }

    /// <summary>
    /// The tags of <paramref name="value"/>, separated by <c>;</c>, <c>,</c>
    /// or white space, trimmed, each once (case-insensitively), in their
    /// order; a tag is ASCII letters and digits in groups joined by <c>-</c>
    /// (or <c>_</c>, read as <c>-</c>), anything else is left out.
    /// </summary>
    public static IReadOnlyList<string> Parse(string? value)
    {
        var tags = new List<string>();
        if (string.IsNullOrWhiteSpace(value))
        {
            return tags;
        }
        foreach (var raw in value.Split([';', ',', ' ', '\t'], StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries))
        {
            var tag = raw.Replace('_', '-');
            if (!IsTag(tag) || tags.Exists(t => string.Equals(t, tag, StringComparison.OrdinalIgnoreCase)))
            {
                continue;
            }
            tags.Add(tag);
        }
        return tags;
    }

    /// <summary>
    /// Makes <see cref="Culture"/> the culture of this thread and of every
    /// thread to come (the formats), when there is one.
    /// </summary>
    public void Apply()
    {
        if (Culture is not { } culture)
        {
            return;
        }
        CultureInfo.DefaultThreadCurrentCulture = culture;
        CultureInfo.DefaultThreadCurrentUICulture = culture;
        CultureInfo.CurrentCulture = culture;
        CultureInfo.CurrentUICulture = culture;
    }

    private static CultureInfo? CultureOf(string tag)
    {
        try
        {
            return CultureInfo.GetCultureInfo(tag, predefinedOnly: true);
        }
        catch (CultureNotFoundException)
        {
            return null;
        }
    }

    private static bool IsTag(string tag)
    {
        if (tag.Length == 0 || tag.Length > 35)
        {
            return false;
        }
        foreach (var part in tag.Split('-'))
        {
            if (part.Length == 0 || part.Length > 8)
            {
                return false;
            }
            foreach (var c in part)
            {
                if (!char.IsAsciiLetterOrDigit(c))
                {
                    return false;
                }
            }
        }
        return true;
    }
}
