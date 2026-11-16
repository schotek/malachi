// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §17): launch at login of the app
// in an MSIX package. A package without unvirtualized resources has its
// writes to HKCU redirected to its own registry, so a Run value it wrote
// would never be read by Windows at sign-in; the package declares a startup
// task instead (windows.startupTask), which Windows starts and lists under
// Settings → Apps → Startup with the user's own switch. The task is
// authoritative as the Run value is for the unpackaged app (LaunchAtLogin):
// the launch-at-login key only mirrors it, and a task the user or a policy
// turned off is shown as such and never overwritten. The start by the task
// is a StartupTask activation, which the app takes as --background
// (Program.RequestOf).

using System;
using Malachi.Core.Settings;
using Malachi.Platform.Windows.Registration;

namespace Malachi.Platform.Windows.Startup;

/// <summary>Launch at login through the package's startup task.</summary>
public sealed class PackagedLaunchAtLogin : ILaunchAtLogin
{
    private readonly IStartupTask task;
    private readonly Func<string, bool> openUri;

    /// <summary>
    /// Launch at login through <paramref name="task"/>;
    /// <paramref name="openUri"/> opens a Settings page
    /// (<see cref="SystemSettings.Open"/> when null).
    /// </summary>
    public PackagedLaunchAtLogin(IStartupTask task, Func<string, bool>? openUri = null)
    {
        ArgumentNullException.ThrowIfNull(task);
        this.task = task;
        this.openUri = openUri ?? SystemSettings.Open;
    }

    /// <inheritdoc/>
    public LaunchAtLoginStatus Status => StatusOf(task.State);

    /// <inheritdoc/>
    public bool IsEnabled => Status == LaunchAtLoginStatus.Enabled;

    /// <summary>What the Launch at Login row shows for a task's state.</summary>
    public static LaunchAtLoginStatus StatusOf(StartupTaskStatus state) => state switch
    {
        StartupTaskStatus.Enabled or StartupTaskStatus.EnabledByPolicy => LaunchAtLoginStatus.Enabled,
        StartupTaskStatus.DisabledByUser => LaunchAtLoginStatus.DisabledByUser,
        StartupTaskStatus.DisabledByPolicy => LaunchAtLoginStatus.DisabledByPolicy,
        _ => LaunchAtLoginStatus.NotRegistered,
    };

    /// <inheritdoc/>
    public LaunchAtLoginOutcome SetEnabled(bool enabled)
    {
        if (enabled)
        {
            return StatusOf(task.RequestEnable()) switch
            {
                LaunchAtLoginStatus.Enabled => LaunchAtLoginOutcome.Granted,
                LaunchAtLoginStatus.DisabledByUser or LaunchAtLoginStatus.DisabledByPolicy => LaunchAtLoginOutcome.RequiresApproval,
                _ => LaunchAtLoginOutcome.NotGranted,
            };
        }
        task.Disable();
        // A task a policy turned on stays on.
        return Status == LaunchAtLoginStatus.Enabled ? LaunchAtLoginOutcome.NotGranted : LaunchAtLoginOutcome.Granted;
    }

    /// <inheritdoc/>
    public bool MirrorInto(SettingsStore settings)
    {
        ArgumentNullException.ThrowIfNull(settings);
        var on = IsEnabled;
        settings.LaunchAtLogin = on;
        return on;
    }

    /// <inheritdoc/>
    public bool OpenSystemSettings() => openUri(SystemSettings.StartupAppsUri);
}
