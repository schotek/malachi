// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Core's default language source for Catalogue.Default (see
// IPreferredLanguages): the current UI culture alone. The app passes the
// Windows display languages instead, which also carry the user's fallbacks.

using System.Collections.Generic;
using System.Globalization;

namespace Malachi.Core.I18n;

/// <summary>The current UI culture as the one preferred language.</summary>
public sealed class CurrentUICulturePreferredLanguages : IPreferredLanguages
{
    /// <summary>The shared instance.</summary>
    public static CurrentUICulturePreferredLanguages Instance { get; } = new();

    /// <inheritdoc/>
    public IReadOnlyList<string> Languages
    {
        get
        {
            var name = CultureInfo.CurrentUICulture.Name;
            return name.Length == 0 ? [] : [name];
        }
    }
}
