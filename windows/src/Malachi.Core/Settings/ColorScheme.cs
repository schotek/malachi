// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Settings/Settings.swift (Settings.ColorScheme);
// GTK: ui/internal/settings/store.go (ColorScheme), the gschema enum
// io.github.schotek.Malachi.ColorScheme.

namespace Malachi.Core.Settings;

/// <summary>The app's colour scheme; stored as its gschema nick.</summary>
public enum ColorScheme
{
    /// <summary><c>system</c>: follow Windows.</summary>
    System,

    /// <summary><c>light</c>.</summary>
    Light,

    /// <summary><c>dark</c>.</summary>
    Dark,
}
