// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/updates/edition.go (Edition, EditionLine); Swift:
// macos/Sources/MalachiCore/Updates/UpdatesEdition.swift. Where this copy
// came from, as the About window says under the update line: the
// installer from the website or the MSIX package of the Microsoft Store
// (the App decides from its package identity).

using Malachi.Core.I18n;

namespace Malachi.Core.Updates;

/// <summary>updates.Edition: where this copy of Malachi Mail came from.</summary>
public enum UpdateEdition
{
    /// <summary>The website's packages: the Windows installer, every development build.</summary>
    Website,
    /// <summary>Apple's Mac App Store (the macOS client's).</summary>
    MacAppStore,
    /// <summary>The Microsoft Store's MSIX package.</summary>
    MicrosoftStore,
}

public static partial class UpdateCheck
{
    /// <summary>updates.EditionLine: the About window's line for <paramref name="e"/>.</summary>
    public static string EditionLine(UpdateEdition e) => e switch
    {
        // TRANSLATORS: In the About window: this copy came from Apple's Mac App Store.
        UpdateEdition.MacAppStore => L10n.T("Version from the Mac App Store"),
        // TRANSLATORS: In the About window: this copy came from the Microsoft Store.
        UpdateEdition.MicrosoftStore => L10n.T("Version from the Microsoft Store"),
        // TRANSLATORS: In the About window: this copy came from the website; malachimail.app stays as it is.
        _ => L10n.T("Version from malachimail.app"),
    };
}
