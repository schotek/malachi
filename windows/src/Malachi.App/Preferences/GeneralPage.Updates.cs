// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first, Preferences → General → Updates (ui/internal/updates
// PrefsGroup, PrefsTitle, PrefsSubtitle, AboutLine, CheckNow, CanCheckNow;
// docs/plans/update-check.md): Check for Updates Automatically, the
// daemon's preference (update.setPrefs, on by default), and the status line
// with Check Now (update.check), from the application's
// UpdateStatusController (AppState.Updates.cs). The switch is insensitive
// until the daemon answered (and for a daemon without the update check);
// Check Now while CanCheckNow says no or a check of this client runs.
// Every text is Core's (UpdateCheck).

using System;
using Malachi.Core.Updates;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;

namespace Malachi.App.Preferences;

public sealed partial class GeneralPage
{
    // The switch is being set from the status, not by the user.
    private bool syncingUpdates;
    private EventHandler? updatesChanged;

    private void InitializeUpdatesGroup()
    {
        UpdatesHeading.Text = UpdateCheck.PrefsGroup();
        UpdatesCheckRow.Header = UpdateCheck.PrefsTitle();
        UpdatesCheckRow.Description = UpdateCheck.PrefsSubtitle();
        AutomationProperties.SetName(UpdatesCheckSwitch, UpdateCheck.PrefsTitle());
        UpdatesCheckNowButton.Content = UpdateCheck.CheckNow();
        updatesChanged = (_, _) => UpdateUpdatesGroup();
        state.Updates.Changed += updatesChanged;
        // A status read before the window opened may be old: ask again.
        state.Updates.Refresh();
        UpdateUpdatesGroup();
    }

    private void CloseUpdatesGroup()
    {
        if (updatesChanged is { } handler)
        {
            state.Updates.Changed -= handler;
            updatesChanged = null;
        }
    }

    private void UpdateUpdatesGroup()
    {
        var status = state.Updates.Status;
        syncingUpdates = true;
        try
        {
            UpdatesCheckRow.IsEnabled = status is not null;
            UpdatesCheckSwitch.IsOn = status is { Enabled: true };
        }
        finally
        {
            syncingUpdates = false;
        }
        UpdatesStatusRow.Header = state.UpdateLine;
        UpdatesStatusRow.Visibility = status is null ? Visibility.Collapsed : Visibility.Visible;
        UpdatesCheckNowButton.IsEnabled = UpdateCheck.CanCheckNow(status) && !state.Updates.Checking;
    }

    private void OnUpdatesCheckToggled(object sender, RoutedEventArgs e)
    {
        if (syncingUpdates)
        {
            return;
        }
        state.Updates.SetEnabled(UpdatesCheckSwitch.IsOn);
    }

    private void OnUpdatesCheckNowClick(object sender, RoutedEventArgs e) => state.Updates.CheckNow();
}
