// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the startup task an MSIX package declares
// (windows/msix/AppxManifest.xml, windows.startupTask), as thin as the
// WinRT class it stands for (Windows.ApplicationModel.StartupTask), so that
// PackagedLaunchAtLogin's rules are tested without a package.

namespace Malachi.Platform.Windows.Startup;

/// <summary>The states of a package's startup task (Windows' StartupTaskState).</summary>
public enum StartupTaskStatus
{
    /// <summary>Off, and the app may turn it on.</summary>
    Disabled,

    /// <summary>Turned off by the user in Windows Settings or Task Manager; only they turn it on again.</summary>
    DisabledByUser,

    /// <summary>On.</summary>
    Enabled,

    /// <summary>Turned off by the organisation's policy.</summary>
    DisabledByPolicy,

    /// <summary>Turned on by the organisation's policy; the app cannot turn it off.</summary>
    EnabledByPolicy,
}

/// <summary>A package's startup task.</summary>
public interface IStartupTask
{
    /// <summary>The task's state now.</summary>
    StartupTaskStatus State { get; }

    /// <summary>Asks Windows to turn the task on and returns the state afterwards.</summary>
    StartupTaskStatus RequestEnable();

    /// <summary>Turns the task off (nothing where the user or a policy decides).</summary>
    void Disable();
}
