// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only seam of the port of macos/Sources/MalachiCore/Daemon/
// DaemonSupervisor.swift (spawn: Foundation.Process.run) and GTK
// ui/internal/daemon/daemon.go (spawn: exec.Cmd.Start). The state machine
// stays in Core; starting and stopping a process on Windows (a new process
// group, the console, CTRL_BREAK) is Malachi.Platform.Windows.Processes.
// DaemonProcessHost (docs/windows-port.md §5).

namespace Malachi.Core.Daemon;

/// <summary>Starts the daemon's process for <see cref="DaemonSupervisor"/>.</summary>
public interface IDaemonProcessHost
{
    /// <summary>
    /// Starts the process. Throws when it cannot be started, with a message
    /// that says why (the executable is missing, access was denied).
    /// </summary>
    IDaemonProcess Start(DaemonStartInfo startInfo);
}
