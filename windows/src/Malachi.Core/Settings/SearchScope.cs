// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Settings/Settings.swift (Settings.SearchScope);
// GTK: ui/internal/settings/store.go (SearchScope), the gschema enum
// io.github.schotek.Malachi.SearchScope.

namespace Malachi.Core.Settings;

/// <summary>
/// Where a search looks: the selected folder, its account, or every account;
/// stored as its gschema nick.
/// </summary>
public enum SearchScope
{
    /// <summary><c>folder</c>.</summary>
    Folder,

    /// <summary><c>account</c>.</summary>
    Account,

    /// <summary><c>all</c>.</summary>
    All,
}
