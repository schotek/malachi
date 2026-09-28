// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The Windows source of the user's languages for Catalogue.Default; the
// counterpart of the preferences Bundle.preferredLocalizations reads in
// macos/Sources/MalachiCore/I18n/Localization.swift (Catalogue.default), and
// of the LANGUAGE/LC_MESSAGES environment gettext reads for
// ui/internal/i18n. A desktop app has no per-app language on Windows; it
// follows the display languages (docs/windows-port.md §9).

using System;
using System.Collections.Generic;
using Malachi.Core.I18n;
using Windows.Win32;
using Windows.Win32.Foundation;

namespace Malachi.Platform.Windows.I18n;

/// <summary>
/// The user's preferred display languages (<c>GetUserPreferredUILanguages</c>),
/// most preferred first, as language names ("cs-CZ").
/// </summary>
public sealed class WindowsPreferredLanguages : IPreferredLanguages
{
    /// <summary>The shared instance.</summary>
    public static WindowsPreferredLanguages Instance { get; } = new();

    /// <summary>
    /// Read afresh on every call; the current UI culture when Windows cannot
    /// say.
    /// </summary>
    public IReadOnlyList<string> Languages => Read() ?? CurrentUICulturePreferredLanguages.Instance.Languages;

    private static unsafe string[]? Read()
    {
        // The list can grow between the two calls; try again then.
        for (var attempt = 0; attempt < 3; attempt++)
        {
            uint size = 0;
            if (!PInvoke.GetUserPreferredUILanguages(PInvoke.MUI_LANGUAGE_NAME, out _, default, ref size) || size == 0)
            {
                return null;
            }
            var buffer = new char[size];
            fixed (char* p = buffer)
            {
                if (!PInvoke.GetUserPreferredUILanguages(PInvoke.MUI_LANGUAGE_NAME, out _, new PZZWSTR(p), ref size))
                {
                    continue;
                }
            }
            // A double-null-terminated list of names.
            var languages = new string(buffer, 0, (int)Math.Min(size, (uint)buffer.Length))
                .Split('\0', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries);
            return languages.Length == 0 ? null : languages;
        }
        return null;
    }
}
