// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Settings/Settings.swift
// (Settings.ChangeToken); GTK: the remove function OnChanged returns in
// ui/internal/settings/store.go.

namespace Malachi.Core.Settings;

/// <summary>
/// Removes a handler installed by <see cref="SettingsStore.OnChange"/>.
/// Dropping the token does not remove the handler; call <see cref="Cancel"/>.
/// </summary>
public sealed class SettingsChangeToken
{
    private readonly ChangeHub hub;
    private readonly SettingsKey key;
    private readonly long id;

    internal SettingsChangeToken(ChangeHub hub, SettingsKey key, long id)
    {
        this.hub = hub;
        this.key = key;
        this.id = id;
    }

    /// <summary>Removes the handler; a handler may cancel its own token while it runs.</summary>
    public void Cancel() => hub.Remove(key, id);
}
