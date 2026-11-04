// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the daemon's clean stop, the SIGTERM of
// macos/Sources/MalachiCore/Daemon/DaemonSupervisor.swift (stop:
// terminate) and ui/internal/daemon/daemon.go (Stop: Signal SIGTERM).
// Windows has no signal between processes; the daemon was started in a
// process group of its own, and CTRL_BREAK_EVENT to that group is what Go
// reads as an interrupt (a clean exit in 20-85 ms, the socket and key
// removed; docs/windows-port.md §0, §5). The event only travels within a
// console, so the recipe of INPUT-SPIKES.md §4.3 depends on where the app
// and the daemon are: the daemon on the app's console gets it directly; an
// app with a console of its own that the daemon does not share leaves it,
// attaches to the daemon's, sends and comes back; an app without a console
// borrows the daemon's hidden one, ignoring Ctrl+C meanwhile. The console
// attachment is the process's, so everything runs under the process-wide
// console gate.

using System;
using Malachi.Platform.Windows.Consoles;
using Windows.Win32;

namespace Malachi.Platform.Windows.Processes;

/// <summary>Delivers CTRL_BREAK_EVENT to a process group, whatever consoles are involved.</summary>
internal static class ConsoleBreak
{
    /// <summary>
    /// Sends CTRL_BREAK_EVENT to the process group of <paramref name="pid"/>
    /// (a process started with CREATE_NEW_PROCESS_GROUP, whose ID is its
    /// group's); <paramref name="attachment"/> gets a fresh terminal when
    /// the process came back to its console.
    /// </summary>
    public static DaemonStopPath Send(int pid, ConsoleAttachment? attachment)
    {
        var target = (uint)pid;
        lock (ConsoleAttachment.Gate)
        {
            var ours = ConsoleAttachment.ConsoleProcesses();
            if (ours.Length > 0 && Array.IndexOf(ours, target) >= 0)
            {
                return PInvoke.GenerateConsoleCtrlEvent(PInvoke.CTRL_BREAK_EVENT, target) ? DaemonStopPath.Direct : DaemonStopPath.Failed;
            }
            if (ours.Length > 0)
            {
                PInvoke.FreeConsole();
                var sent = PInvoke.AttachConsole(target) && PInvoke.GenerateConsoleCtrlEvent(PInvoke.CTRL_BREAK_EVENT, target);
                PInvoke.FreeConsole();
                ReturnTo(ours, target);
                attachment?.Reattached();
                return sent ? DaemonStopPath.Juggled : DaemonStopPath.Failed;
            }
            if (!PInvoke.AttachConsole(target))
            {
                return DaemonStopPath.Failed;
            }
            PInvoke.SetConsoleCtrlHandler(null, true);
            var delivered = PInvoke.GenerateConsoleCtrlEvent(PInvoke.CTRL_BREAK_EVENT, target);
            PInvoke.FreeConsole();
            PInvoke.SetConsoleCtrlHandler(null, false);
            return delivered ? DaemonStopPath.Borrowed : DaemonStopPath.Failed;
        }
    }

    // Back to the console the process had, through another process that is
    // on it (the terminal's shell), else the parent's (where an attached
    // app got it). A console nobody else was on is gone once left.
    private static void ReturnTo(uint[] previous, uint daemon)
    {
        var self = (uint)Environment.ProcessId;
        foreach (var pid in previous)
        {
            if (pid != self && pid != daemon && PInvoke.AttachConsole(pid))
            {
                return;
            }
        }
        PInvoke.AttachConsole(PInvoke.ATTACH_PARENT_PROCESS);
    }
}
