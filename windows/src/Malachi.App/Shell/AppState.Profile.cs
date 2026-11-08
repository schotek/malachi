// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the profile's part of macos/Sources/MalachiMail/App/AppState.swift
// (profile, wireProfile); GTK: window/assistant_profile.go (AttachProfile,
// RefreshProfile, OnProfileChange). The user's profile for the assistant
// (docs/api.md §4.18, docs/plans/assistant-profile.md), once for the
// application: one ProfileStore, read when the connection comes and again on
// notify.assistantProfileChanged and notify.accountsChanged. The daemon
// puts it into every prompt it builds (assistant.prompt; the one-shot
// requests name the account whose note goes with it); Preferences → AI →
// About You edits it and the panel's proposal cards resolve through it.

using System;
using System.Collections.Generic;
using Malachi.Core.Controllers;
using Microsoft.Extensions.Logging;

namespace Malachi.App.Shell;

/// <summary>The user's profile for the assistant.</summary>
public sealed partial class AppState
{
    private readonly List<IDisposable> profileTokens = [];

    /// <summary>The user's profile, one for the application.</summary>
    public ProfileStore Profile { get; private set; } = null!;

    // After the board.
    private void InitializeProfile()
    {
        Profile = new ProfileStore(Client, Logs.CreateLogger<ProfileStore>());
        profileTokens.Add(Notifications.AddConnectionState(s =>
        {
            if (s is ConnectionState.Connected or ConnectionState.InfoFailed)
            {
                Profile.Refresh();
            }
        }));
        profileTokens.Add(Notifications.AddAssistantProfileChanged(Profile.Refresh));
        profileTokens.Add(Notifications.AddAccountsChanged(Profile.Refresh));
        if (Notifications.ConnectionState is ConnectionState.Connected)
        {
            Profile.Refresh();
        }
    }

    private void CloseProfile()
    {
        foreach (var t in profileTokens)
        {
            t.Dispose();
        }
        profileTokens.Clear();
    }
}
