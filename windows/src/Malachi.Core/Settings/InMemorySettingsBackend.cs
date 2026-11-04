// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the in-memory fallback of ui/internal/settings/store.go
// (NewMemory, the mem map); macOS: the UserDefaults suite of
// macos/Tests/MalachiCoreTests/SettingsTests.swift (Scratch). Values last
// until the process exits: for tests, and for a settings store that cannot
// be opened (the app logs that and keeps working, as GTK does without its
// schema).

using System;
using System.Collections.Generic;
using System.Diagnostics.CodeAnalysis;
using System.Linq;
using System.Threading;

namespace Malachi.Core.Settings;

/// <summary>Settings kept in memory for the process lifetime.</summary>
public sealed class InMemorySettingsBackend : ISettingsBackend
{
    private readonly Lock gate = new();
    private readonly Dictionary<string, object> values = new(StringComparer.Ordinal);

    /// <inheritdoc/>
    public event EventHandler<SettingsChangedEventArgs>? Changed;

    /// <inheritdoc/>
    public bool IsPersistent => false;

    /// <inheritdoc/>
    public bool TryGetBoolean(string key, out bool value)
    {
        lock (gate)
        {
            if (values.TryGetValue(key, out var stored) && stored is bool b)
            {
                value = b;
                return true;
            }
        }
        value = false;
        return false;
    }

    /// <inheritdoc/>
    public bool TryGetInt32(string key, out int value)
    {
        lock (gate)
        {
            if (values.TryGetValue(key, out var stored) && stored is int i)
            {
                value = i;
                return true;
            }
        }
        value = 0;
        return false;
    }

    /// <inheritdoc/>
    public bool TryGetString(string key, [NotNullWhen(true)] out string? value)
    {
        lock (gate)
        {
            if (values.TryGetValue(key, out var stored) && stored is string s)
            {
                value = s;
                return true;
            }
        }
        value = null;
        return false;
    }

    /// <inheritdoc/>
    public bool TryGetStringList(string key, [NotNullWhen(true)] out IReadOnlyList<string>? value)
    {
        lock (gate)
        {
            if (values.TryGetValue(key, out var stored) && stored is string[] list)
            {
                value = (string[])list.Clone();
                return true;
            }
        }
        value = null;
        return false;
    }

    /// <inheritdoc/>
    public void SetBoolean(string key, bool value) => Store(key, value);

    /// <inheritdoc/>
    public void SetInt32(string key, int value) => Store(key, value);

    /// <inheritdoc/>
    public void SetString(string key, string value)
    {
        ArgumentNullException.ThrowIfNull(value);
        Store(key, value);
    }

    /// <inheritdoc/>
    public void SetStringList(string key, IReadOnlyList<string> value)
    {
        ArgumentNullException.ThrowIfNull(value);
        Store(key, value.ToArray());
    }

    /// <summary>
    /// Stores <paramref name="value"/> (a bool, an int, a string or a string
    /// list; null removes it, back to the default) as another process or a
    /// hand edit would, and raises <see cref="Changed"/> on the calling
    /// thread.
    /// </summary>
    public void ChangeExternally(string key, object? value)
    {
        ArgumentNullException.ThrowIfNull(key);
        lock (gate)
        {
            if (value is null)
            {
                values.Remove(key);
            }
            else
            {
                values[key] = value is IEnumerable<string> list ? list.ToArray() : value;
            }
        }
        Changed?.Invoke(this, new SettingsChangedEventArgs(key));
    }

    /// <summary>Nothing to release; the values stay readable.</summary>
    public void Dispose()
    {
    }

    private void Store(string key, object value)
    {
        ArgumentNullException.ThrowIfNull(key);
        lock (gate)
        {
            values[key] = value;
        }
    }
}
