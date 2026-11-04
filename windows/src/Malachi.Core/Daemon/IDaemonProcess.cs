// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only seam of the port of macos/Sources/MalachiCore/Daemon/
// DaemonSupervisor.swift (the Process it keeps: terminationHandler,
// terminate, kill SIGKILL) and GTK ui/internal/daemon/daemon.go (cmd.Wait,
// Signal SIGTERM, Kill).

using System;
using System.Threading.Tasks;

namespace Malachi.Core.Daemon;

/// <summary>A daemon process an <see cref="IDaemonProcessHost"/> started.</summary>
public interface IDaemonProcess : IDisposable
{
    /// <summary>The process ID.</summary>
    int Id { get; }

    /// <summary>
    /// Completes with the exit code once the process has exited and what it
    /// wrote has been passed on (<see cref="ExitStatus.Describe"/>). It
    /// never faults.
    /// </summary>
    Task<int> Exited { get; }

    /// <summary>
    /// Asks the process to shut down cleanly, the SIGTERM of Go and Swift
    /// (CTRL_BREAK on Windows, which Go reads as an interrupt). False when
    /// the request could not be delivered, or was not sent because the
    /// process has exited (<see cref="Exited"/> may still be waiting for its
    /// last lines); the supervisor then kills it after its stop timeout all
    /// the same.
    /// </summary>
    bool RequestStop();

    /// <summary>Ends the process at once (SIGKILL; TerminateProcess). Nothing when it has exited.</summary>
    void Kill();
}
