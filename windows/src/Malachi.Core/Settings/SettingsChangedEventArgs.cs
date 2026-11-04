// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The counterpart of the key path KVO hands macos/Sources/MalachiCore/Settings/Settings.swift
// (DefaultsObserver) and of the key GSettings' "changed" signal carries
// (ui/internal/settings/store.go, ConnectChanged).

using System;

namespace Malachi.Core.Settings;

/// <summary>The name of a stored value that changed.</summary>
public sealed class SettingsChangedEventArgs(string key) : EventArgs
{
    /// <summary>The value's name, as stored (a gschema name, case as written).</summary>
    public string Key { get; } = key;
}
