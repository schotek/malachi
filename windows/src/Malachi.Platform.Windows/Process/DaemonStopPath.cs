// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the three ways CTRL_BREAK reaches the daemon
// (docs/windows-port.md §5 Console; INPUT-SPIKES.md §4.3), the Windows
// counterpart of the one Process.terminate() of
// macos/Sources/MalachiCore/Daemon/DaemonSupervisor.swift (stop).

namespace Malachi.Platform.Windows.Processes;

/// <summary>How a stop request went to the daemon.</summary>
public enum DaemonStopPath
{
    /// <summary>It could not be delivered (the daemon is gone, or has no console to reach).</summary>
    Failed,

    /// <summary>
    /// The daemon shares the app's console (the app runs attached to a
    /// terminal): CTRL_BREAK to its process group, nothing else.
    /// </summary>
    Direct,

    /// <summary>
    /// The app has a console the daemon does not share: the app left its
    /// console, attached to the daemon's for the event, and came back.
    /// </summary>
    Juggled,

    /// <summary>
    /// The app has no console (started from Explorer or the Start menu): it
    /// attached to the daemon's hidden one for the event, then let go.
    /// </summary>
    Borrowed,

    /// <summary>
    /// Nothing was sent: the daemon had exited already, and its exit was
    /// only waiting for its last lines. An attached app does not leave its
    /// terminal's console for a process that is gone.
    /// </summary>
    Exited,
}
