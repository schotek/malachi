// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the process host of DaemonSupervisor (docs/windows-port.md
// §5 Supervisor and Console), the platform half of
// macos/Sources/MalachiCore/Daemon/DaemonSupervisor.swift (spawn, stop)
// and ui/internal/daemon/daemon.go (spawn, Stop). The daemon is started in
// a process group of its own (the terminal's Ctrl+C spares it; CTRL_BREAK
// to the group stops it), with no window of its own only when the app has
// no console (otherwise a console-less start makes Windows open a terminal
// window for it, spike run 18), with NUL as stdin and nothing inherited but
// its output pipe (ChildProcess). Its lines go to the daemon log under
// logs\ and, when the app is attached to a terminal, there too: GTK and
// macOS hand the daemon their stderr, which a WinUI app from the Start menu
// does not have. The console the child gets is decided and created under
// the process-wide console gate, so a stop moving the process between
// consoles cannot come in between.

using System;
using Malachi.Core.Daemon;
using Malachi.Platform.Windows.Consoles;

namespace Malachi.Platform.Windows.Processes;

/// <summary>Starts malachid for <see cref="DaemonSupervisor"/> on Windows.</summary>
public sealed class DaemonProcessHost : IDaemonProcessHost
{
    private readonly ConsoleAttachment? console;
    private readonly RotatingLogFile? log;

    /// <summary>
    /// A host that writes the daemon's output to <paramref name="log"/> and
    /// to the terminal of <paramref name="console"/>; either may be null.
    /// </summary>
    public DaemonProcessHost(ConsoleAttachment? console = null, RotatingLogFile? log = null)
    {
        this.console = console;
        this.log = log;
    }

    /// <summary>The clock of the wait for the last lines after an exit.</summary>
    public TimeProvider Time { get; init; } = TimeProvider.System;

    /// <summary>
    /// Starts the daemon with its own hidden console even when the app has
    /// one, so that the stop has to move between consoles (tests of
    /// <see cref="DaemonStopPath.Juggled"/>).
    /// </summary>
    internal bool ForceOwnConsole { get; init; }

    /// <inheritdoc/>
    public IDaemonProcess Start(DaemonStartInfo startInfo)
    {
        ArgumentNullException.ThrowIfNull(startInfo);
        ChildProcess child;
        lock (ConsoleAttachment.Gate)
        {
            var noWindow = ForceOwnConsole || !ConsoleAttachment.HasConsole;
            child = ChildProcess.Start(
                startInfo.Executable, startInfo.Arguments, startInfo.Environment, newProcessGroup: true, noWindow: noWindow);
        }
        return new DaemonProcess(child, Line, console, Time);
    }

    private void Line(string text)
    {
        log?.WriteLine(text);
        console?.Terminal?.WriteLine(text);
    }
}
