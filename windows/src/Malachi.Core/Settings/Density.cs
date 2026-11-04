// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Settings/Settings.swift (Settings.Density);
// GTK: ui/internal/settings/store.go (Density), the gschema enum
// io.github.schotek.Malachi.Density.

namespace Malachi.Core.Settings;

/// <summary>The message list's density; stored as its gschema nick.</summary>
public enum Density
{
    /// <summary><c>comfortable</c>.</summary>
    Comfortable,

    /// <summary><c>compact</c>.</summary>
    Compact,
}
