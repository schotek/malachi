// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the language source of macos/Sources/MalachiCore/I18n/Localization.swift
// (Bundle.preferredLocalizations reads the user's languages); GTK: the
// LANGUAGE/LC_MESSAGES environment gettext reads. Windows reads the display
// languages (Malachi.Platform.Windows, WindowsPreferredLanguages); Core's
// default is the UI culture, so that Core runs anywhere.

using System.Collections.Generic;

namespace Malachi.Core.I18n;

/// <summary>The user's preferred user-interface languages.</summary>
public interface IPreferredLanguages
{
    /// <summary>
    /// BCP 47 tags ("cs-CZ", "en-US"), most preferred first; empty when
    /// nothing is known.
    /// </summary>
    IReadOnlyList<string> Languages { get; }
}
