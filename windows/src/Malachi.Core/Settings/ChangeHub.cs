// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Settings/Settings.swift (ChangeHub);
// GTK: the handlers map and fire of ui/internal/settings/store.go.
//
// The handler table. Handlers of a key run in registration order (the
// savingCollapse/savingFavourites echo guards of the sidebar depend on it);
// the list is copied before they run, so a handler may remove itself or
// another one. A lock makes it safe to add and remove from any thread,
// because an external change may be raised on the backend's thread when the
// store has no synchronization context.

using System;
using System.Collections.Generic;
using System.Threading;

namespace Malachi.Core.Settings;

internal sealed class ChangeHub
{
    private readonly Lock gate = new();
    private readonly Dictionary<SettingsKey, List<KeyValuePair<long, Action>>> handlers = [];
    private long nextId;

    public long Add(SettingsKey key, Action handler)
    {
        lock (gate)
        {
            var id = nextId++;
            if (!handlers.TryGetValue(key, out var list))
            {
                list = [];
                handlers[key] = list;
            }
            list.Add(new KeyValuePair<long, Action>(id, handler));
            return id;
        }
    }

    public void Remove(SettingsKey key, long id)
    {
        lock (gate)
        {
            if (handlers.TryGetValue(key, out var list))
            {
                list.RemoveAll(entry => entry.Key == id);
            }
        }
    }

    public void Fire(SettingsKey key)
    {
        // Copy first: a handler may remove itself.
        Action[] fs;
        lock (gate)
        {
            if (!handlers.TryGetValue(key, out var list) || list.Count == 0)
            {
                return;
            }
            fs = new Action[list.Count];
            for (var i = 0; i < fs.Length; i++)
            {
                fs[i] = list[i].Value;
            }
        }
        foreach (var f in fs)
        {
            f();
        }
    }
}
