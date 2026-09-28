// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/AppearancePaneViewController.swift
// (bindIfReady); GTK: ui/internal/window/preferences.go (NewPreferences'
// bindings and bindChoice for the colour scheme and the density). Every
// row is bound two-way to its settings key; the consumers follow the keys
// live elsewhere (the colour scheme in the shell's WindowTracker, the list,
// the grouping and the reader in their screens).

using Malachi.App.Shell;
using Malachi.Core.Model;
using Malachi.Core.Settings;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Preferences;

/// <summary>The Appearance page of the preferences.</summary>
public sealed partial class AppearancePage : UserControl
{
    /// <summary>The page of a preferences window over <paramref name="state"/>.</summary>
    internal AppearancePage(AppState state, SettingBindings bindings)
    {
        InitializeComponent();
        var s = state.Settings;
        bindings.Choice(ColorSchemeBox, SettingsKey.ColorScheme, PreferenceChoices.ColorSchemeChoices, () => s.ColorScheme, v => s.ColorScheme = v);
        bindings.Choice(DensityBox, SettingsKey.Density, PreferenceChoices.DensityChoices, () => s.Density, v => s.Density = v);
        bindings.Toggle(GroupByConversationSwitch, SettingsKey.GroupByConversation, () => s.GroupByConversation, v => s.GroupByConversation = v);
        bindings.Toggle(ShowPreviewLineSwitch, SettingsKey.ShowPreviewLine, () => s.ShowPreviewLine, v => s.ShowPreviewLine = v);
        bindings.Toggle(ShowAvatarsSwitch, SettingsKey.ShowAvatars, () => s.ShowAvatars, v => s.ShowAvatars = v);
        bindings.Toggle(MonochromeAvatarsSwitch, SettingsKey.MonochromeAvatars, () => s.MonochromeAvatars, v => s.MonochromeAvatars = v);
        bindings.Toggle(MonospacePlainTextSwitch, SettingsKey.MonospacePlainText, () => s.MonospacePlainText, v => s.MonospacePlainText = v);
        bindings.Number(TextZoomBox, SettingsKey.TextZoom, () => s.TextZoom, v => s.TextZoom = v);
    }
}
