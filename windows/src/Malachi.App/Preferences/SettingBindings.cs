// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/PreferenceBindings.swift
// (PreferenceBinding.bind for a switch, a spin control and a pop-up with
// its choices; PreferenceBindingSet and its syncing guard); GTK:
// ui/internal/settings Store.Bind and window/preferences.go bindChoice. A
// control and its settings key follow each other both ways: a change of
// the control is written, a change of the key (from this window, another
// window or a reg add) is shown, and the control's own change of that is
// not written back. The bindings end with the window (the GTK dialog
// undoes them when it closes).

using System;
using System.Collections.Generic;
using Malachi.Core.Settings;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Preferences;

/// <summary>The two-way bindings of one preferences window.</summary>
internal sealed class SettingBindings : IDisposable
{
    private readonly SettingsStore settings;
    private readonly List<SettingsChangeToken> tokens = [];

    /// <summary>Bindings over <paramref name="settings"/>.</summary>
    public SettingBindings(SettingsStore settings)
    {
        this.settings = settings;
    }

    /// <summary>A switch row (Adw.SwitchRow's <c>active</c>).</summary>
    public void Toggle(ToggleSwitch toggle, SettingsKey key, Func<bool> get, Action<bool> set)
    {
        var syncing = false;
        void Show()
        {
            syncing = true;
            toggle.IsOn = get();
            syncing = false;
        }
        Show();
        toggle.Toggled += (_, _) =>
        {
            if (!syncing)
            {
                set(toggle.IsOn);
            }
        };
        tokens.Add(settings.OnChange(key, Show));
    }

    /// <summary>
    /// A spin row (Adw.SpinRow's <c>value</c>): the settings clamp the value
    /// to the key's range, and the field shows what was stored; an emptied
    /// field shows the stored value again.
    /// </summary>
    public void Number(NumberBox box, SettingsKey key, Func<int> get, Action<int> set)
    {
        var syncing = false;
        void Show()
        {
            syncing = true;
            box.Value = get();
            syncing = false;
        }
        Show();
        box.ValueChanged += (_, e) =>
        {
            if (syncing)
            {
                return;
            }
            if (double.IsNaN(e.NewValue))
            {
                Show();
                return;
            }
            set((int)Math.Round(e.NewValue));
            if (box.Value != get())
            {
                Show();
            }
        };
        tokens.Add(settings.OnChange(key, Show));
    }

    /// <summary>
    /// A combo row over a string enum (preferences.go <c>bindChoice</c>):
    /// the items are <paramref name="choices"/> in order; a value the list
    /// does not have selects the first.
    /// </summary>
    public void Choice<T>(ComboBox combo, SettingsKey key, IReadOnlyList<T> choices, Func<T> get, Action<T> set)
        where T : struct, Enum
    {
        var syncing = false;
        int IndexOf(T value)
        {
            for (var i = 0; i < choices.Count; i++)
            {
                if (EqualityComparer<T>.Default.Equals(choices[i], value))
                {
                    return i;
                }
            }
            return 0;
        }
        void Show()
        {
            var want = IndexOf(get());
            if (combo.SelectedIndex != want)
            {
                syncing = true;
                combo.SelectedIndex = want;
                syncing = false;
            }
        }
        Show();
        combo.SelectionChanged += (_, _) =>
        {
            if (!syncing && combo.SelectedIndex >= 0 && combo.SelectedIndex < choices.Count)
            {
                set(choices[combo.SelectedIndex]);
            }
        };
        tokens.Add(settings.OnChange(key, Show));
    }

    /// <summary>Ends every binding: the controls stay as they are and no longer follow the keys.</summary>
    public void Dispose()
    {
        foreach (var token in tokens)
        {
            token.Cancel();
        }
        tokens.Clear();
    }
}
