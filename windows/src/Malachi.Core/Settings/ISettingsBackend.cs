// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Where the settings live: UserDefaults under
// macos/Sources/MalachiCore/Settings/Settings.swift, GSettings (or the
// in-memory fallback) under ui/internal/settings/store.go. Windows keeps them
// in HKCU\Software\io.github.schotek.Malachi (RegistrySettingsBackend in
// Malachi.Platform.Windows); the tests and a store that cannot be opened use
// InMemorySettingsBackend (docs/windows-port.md §8). The typed values are
// the GSettings ones: b, i, s (enum nicks) and as.

using System;
using System.Collections.Generic;
using System.Diagnostics.CodeAnalysis;

namespace Malachi.Core.Settings;

/// <summary>
/// Stores the raw values of <see cref="SettingsStore"/> by gschema name.
/// Disposing it releases what it holds (the registry backend's key and
/// watcher thread); the owner disposes it after the stores over it.
/// </summary>
public interface ISettingsBackend : IDisposable
{
    /// <summary>Whether values survive a restart (GTK's <c>Persistent</c>).</summary>
    bool IsPersistent { get; }

    /// <summary>
    /// Raised when a value changes from outside (another process, a hand
    /// edit such as <c>reg add</c>), once per changed name, on any thread.
    /// The Set methods never raise it for what they wrote.
    /// </summary>
    event EventHandler<SettingsChangedEventArgs>? Changed;

    /// <summary>The stored boolean <paramref name="key"/>; false when it is unset or of another type.</summary>
    bool TryGetBoolean(string key, out bool value);

    /// <summary>The stored integer <paramref name="key"/>; false when it is unset or of another type.</summary>
    bool TryGetInt32(string key, out int value);

    /// <summary>The stored string <paramref name="key"/>; false when it is unset or of another type.</summary>
    bool TryGetString(string key, [NotNullWhen(true)] out string? value);

    /// <summary>The stored string list <paramref name="key"/>; false when it is unset or of another type.</summary>
    bool TryGetStringList(string key, [NotNullWhen(true)] out IReadOnlyList<string>? value);

    /// <summary>Stores a boolean.</summary>
    void SetBoolean(string key, bool value);

    /// <summary>Stores an integer.</summary>
    void SetInt32(string key, int value);

    /// <summary>Stores a string.</summary>
    void SetString(string key, string value);

    /// <summary>Stores a copy of a string list.</summary>
    void SetStringList(string key, IReadOnlyList<string> value);
}
