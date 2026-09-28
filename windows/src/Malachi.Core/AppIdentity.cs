// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The names the Windows client shows and registers under, in one place.
// Scaffold (phase B): the first type of Malachi.Core; the ported code of
// phase C and D joins it.

namespace Malachi.Core;

/// <summary>The application's names, as CLAUDE.md fixes them.</summary>
public static class AppIdentity
{
    /// <summary>
    /// The application ID, never shortened or re-cased: the preferences key
    /// under HKCU\Software, the notifications' AppUserModelID, the prefix of
    /// the Credential Manager targets.
    /// </summary>
    public const string AppId = "io.github.schotek.Malachi";

    /// <summary>
    /// The product name. The main window's title, which window.blp does not
    /// translate either.
    /// </summary>
    public const string DisplayName = "Malachi Mail";
}
