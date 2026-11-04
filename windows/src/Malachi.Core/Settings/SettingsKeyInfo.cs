// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Settings/Settings.swift
// (registrationDefaults and the bounds); GTK: the defaults map and the
// constants of ui/internal/settings/store.go. One row per key of
// data/io.github.schotek.Malachi.gschema.xml, the facade's single source of
// defaults and ranges, which Malachi.Conventions.Tests compares with the
// gschema.

using System.Collections.Generic;

namespace Malachi.Core.Settings;

/// <summary>What the schema says about one key.</summary>
public sealed class SettingsKeyInfo
{
    internal SettingsKeyInfo(
        SettingsKey key,
        string name,
        string type,
        object defaultValue,
        int? minimum = null,
        int? maximum = null,
        IReadOnlyList<string>? choices = null,
        bool windowsOnly = false)
    {
        Key = key;
        Name = name;
        Type = type;
        Default = defaultValue;
        Minimum = minimum;
        Maximum = maximum;
        Choices = choices ?? [];
        WindowsOnly = windowsOnly;
    }

    /// <summary>The key.</summary>
    public SettingsKey Key { get; }

    /// <summary>The gschema name ("text-zoom"), the name of the stored value.</summary>
    public string Name { get; }

    /// <summary>The GVariant type: <c>b</c>, <c>i</c>, <c>s</c> (enum keys too) or <c>as</c>.</summary>
    public string Type { get; }

    /// <summary>The default: a bool, an int, a string (an enum's nick) or an empty string list.</summary>
    public object Default { get; }

    /// <summary>The lower bound of an integer key's range, if it has one.</summary>
    public int? Minimum { get; }

    /// <summary>The upper bound of an integer key's range, if it has one.</summary>
    public int? Maximum { get; }

    /// <summary>The nicks of an enum key in gschema order; empty for other keys.</summary>
    public IReadOnlyList<string> Choices { get; }

    /// <summary>Whether the gschema lacks the key (ctrl-r, the counterpart of macOS's command-r).</summary>
    public bool WindowsOnly { get; }
}
