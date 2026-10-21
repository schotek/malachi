// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the profile's part of macos/Sources/MalachiMail/App/AppState.swift
// (profile, wireProfile); GTK: window/assistant_profile.go (AttachProfile,
// RefreshProfile, OnProfileChange). The user's profile for the assistant
// (docs/api.md §4.18, docs/plans/assistant-profile.md), once for the
// application: one ProfileStore, read when the connection comes and again on
// notify.assistantProfileChanged and notify.accountsChanged. Every prompt
// carries it: the triage and Suggest Reply here, the panel, the compose
// window's rewrite and writing bar and the search in the user's own words
// where they are made (UserProfile.For the window's account there);
// Preferences → AI → About You edits it and the panel's proposal cards
// resolve through it.

using System;
using System.Collections.Generic;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Microsoft.Extensions.Logging;

namespace Malachi.App.Shell;

/// <summary>The user's profile for the assistant.</summary>
public sealed partial class AppState
{
    private readonly List<IDisposable> profileTokens = [];

    /// <summary>The user's profile, one for the application.</summary>
    public ProfileStore Profile { get; private set; } = null!;

    /// <summary>The profile for the prompts, every account's note; empty before the store exists.</summary>
    public UserProfile CurrentProfile => Profile is { } p ? p.Profile : UserProfile.Empty;

    // After the board (the triage and Suggest Reply carry the profile).
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
        BoardTriage.Profile = () => CurrentProfile;
        BoardReply.Profile = () => CurrentProfile;
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
