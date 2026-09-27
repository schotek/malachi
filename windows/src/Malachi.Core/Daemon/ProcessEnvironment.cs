// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only helper for the ports of macos/Sources/MalachiCore/Daemon/
// DaemonSupervisor.swift and Paths.swift, which read
// ProcessInfo.processInfo.environment: the process's environment as a
// dictionary, and lookups that compare names as the system does
// (case-insensitively on Windows, where Path and PATH are one variable)
// whatever comparer the caller's dictionary has.

using System;
using System.Collections;
using System.Collections.Generic;

namespace Malachi.Core.Daemon;

/// <summary>Environment dictionaries with the system's comparison of names.</summary>
internal static class ProcessEnvironment
{
    /// <summary>How the system compares variable names.</summary>
    public static StringComparer NameComparer { get; } =
        OperatingSystem.IsWindows() ? StringComparer.OrdinalIgnoreCase : StringComparer.Ordinal;

    /// <summary>A snapshot of this process's environment.</summary>
    public static Dictionary<string, string?> Current()
    {
        var result = new Dictionary<string, string?>(NameComparer);
        foreach (DictionaryEntry entry in Environment.GetEnvironmentVariables())
        {
            if (entry.Key is string name && entry.Value is string value)
            {
                result[name] = value;
            }
        }
        return result;
    }

    /// <summary>
    /// A copy of <paramref name="environment"/> with the system's comparison
    /// of names; a null value leaves the variable out.
    /// </summary>
    public static Dictionary<string, string> Copy(IReadOnlyDictionary<string, string?> environment)
    {
        var result = new Dictionary<string, string>(NameComparer);
        foreach (var (name, value) in environment)
        {
            if (value is not null)
            {
                result[name] = value;
            }
        }
        return result;
    }

    /// <summary>Whether <paramref name="name"/> is set, empty or not, and its value.</summary>
    public static bool TryGet(IReadOnlyDictionary<string, string?> environment, string name, out string value)
    {
        if (environment.TryGetValue(name, out var direct) && direct is not null)
        {
            value = direct;
            return true;
        }
        foreach (var (key, v) in environment)
        {
            if (v is not null && NameComparer.Equals(key, name))
            {
                value = v;
                return true;
            }
        }
        value = "";
        return false;
    }

    /// <summary>The value of <paramref name="name"/>, or null when it is unset or empty.</summary>
    public static string? NonEmpty(IReadOnlyDictionary<string, string?> environment, string name) =>
        TryGet(environment, name, out var value) && value.Length > 0 ? value : null;
}
