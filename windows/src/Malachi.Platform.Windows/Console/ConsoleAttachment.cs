// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: the console recipe of docs/windows-port.md §5 (Console),
// measured in spikes2/INPUT-SPIKES.md §4 (ConsoleSpike.EarlyInit and
// OnConsoleCtrl). It gives make run-windows what ui/main.go and the macOS
// app get from a terminal for free: their log and the daemon's in it, and
// Ctrl+C quitting gracefully. A WinUI app has no console of its own; Main
// calls Initialize before anything touches System.Console:
//
// 1. The standard handles a launcher passed stop being inheritable: .NET
//    starts every child with bInheritHandles, and a daemon holding a
//    PowerShell pipe keeps the pipeline waiting for ever (spike runs 8-10).
// 2. AttachConsole(ATTACH_PARENT_PROCESS): the terminal the app was started
//    from; from Explorer or the Start menu it fails (ERROR_INVALID_HANDLE)
//    and the app has no console.
// 3. When attached, the control handler: an inherited "ignore Ctrl+C" is
//    undone, then CTRL_C, CTRL_BREAK and CTRL_CLOSE go to the app's
//    callback (on a thread the system creates). For CTRL_CLOSE, LOGOFF and
//    SHUTDOWN the process ends as soon as the handler returns, so it waits
//    (up to 4.5 s of the system's 5) until the app says its shutdown is
//    complete. The callback marks the app as quitting first
//    (DaemonSupervisor.BeginStopping): the daemon got CTRL_CLOSE as well
//    and stops by itself, and must not be restarted.
// 4. The terminal for log lines: a pipe or file the launcher passed as
//    stderr wins (it asked to capture), else the attached console's
//    CONOUT$, else none (the log file under logs\ has them regardless).
//
// Gate is the process-wide lock around anything that depends on which
// console the process is attached to: the daemon's stop moves the process
// to the daemon's console for a moment (Processes.ConsoleBreak).

using System;
using System.Threading;
using System.Threading.Tasks;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.Storage.FileSystem;
using Windows.Win32.System.Console;

namespace Malachi.Platform.Windows.Consoles;

/// <summary>The process's attachment to the terminal it was started from.</summary>
public sealed class ConsoleAttachment
{
    /// <summary>
    /// How long the handler holds a closing terminal (CTRL_CLOSE) for the
    /// app's shutdown; the system ends the process after about five seconds.
    /// </summary>
    public static readonly TimeSpan CloseGrace = TimeSpan.FromMilliseconds(4500);

    /// <summary>Held around everything that depends on the process's console.</summary>
    internal static readonly Lock Gate = new();

    private static ConsoleAttachment? current;

    // The system calls it on its own thread for as long as the process
    // lives, so it must never be collected.
    private static PHANDLER_ROUTINE? routine;

    private readonly Action<ConsoleControl>? onControl;
    private readonly TimeProvider time;
    private readonly TaskCompletionSource shutdown = new(TaskCreationOptions.RunContinuationsAsynchronously);

    internal ConsoleAttachment(bool attached, TerminalWriter? terminal, Action<ConsoleControl>? onControl, TimeProvider? time)
    {
        IsAttached = attached;
        Terminal = terminal;
        this.onControl = onControl;
        this.time = time ?? TimeProvider.System;
    }

    /// <summary>The attachment <see cref="Initialize"/> made, or null before it ran.</summary>
    public static ConsoleAttachment? Current
    {
        get
        {
            lock (Gate)
            {
                return current;
            }
        }
    }

    /// <summary>
    /// Whether this process has a console now (its own, the one it attached
    /// to, or the daemon's for the moment of a stop).
    /// </summary>
    public static bool HasConsole => ConsoleProcesses().Length > 0;

    /// <summary>Whether the process attached to the console of the terminal it was started from.</summary>
    public bool IsAttached { get; }

    /// <summary>
    /// Where log lines reach the terminal: the stderr a launcher passed, or
    /// the attached console; null when there is neither.
    /// </summary>
    public TerminalWriter? Terminal { get; }

    /// <summary>
    /// The recipe of the file header, once per process, first thing in
    /// Main. <paramref name="onControl"/> gets the console's control events
    /// while attached, on a thread of the system; it must not block, and for
    /// <see cref="ConsoleControl.Close"/> the process ends
    /// <see cref="CloseGrace"/> later or at <see cref="ShutdownCompleted"/>.
    /// </summary>
    public static ConsoleAttachment Initialize(Action<ConsoleControl>? onControl = null, TimeProvider? time = null)
    {
        lock (Gate)
        {
            if (current is not null)
            {
                throw new InvalidOperationException("the console attachment is made once per process");
            }
            foreach (var which in new[] { STD_HANDLE.STD_INPUT_HANDLE, STD_HANDLE.STD_OUTPUT_HANDLE, STD_HANDLE.STD_ERROR_HANDLE })
            {
                var std = PInvoke.GetStdHandle(which);
                if (IsHandle(std))
                {
                    PInvoke.SetHandleInformation(std, (uint)HANDLE_FLAGS.HANDLE_FLAG_INHERIT, 0);
                }
            }
            var stderr = PInvoke.GetStdHandle(STD_HANDLE.STD_ERROR_HANDLE);
            var captured = IsHandle(stderr) && PInvoke.GetFileType(stderr) is FILE_TYPE.FILE_TYPE_PIPE or FILE_TYPE.FILE_TYPE_DISK;

            bool attached = PInvoke.AttachConsole(PInvoke.ATTACH_PARENT_PROCESS);
            var terminal = captured
                ? TerminalWriter.ForInheritedHandle(stderr)
                : attached ? TerminalWriter.OpenConsoleOutput() : null;
            var attachment = new ConsoleAttachment(attached, terminal, onControl, time);
            if (attached)
            {
                // A parent that started us in a new process group, or called
                // SetConsoleCtrlHandler(NULL, TRUE), left Ctrl+C ignored.
                PInvoke.SetConsoleCtrlHandler(null, false);
                routine = attachment.OnControl;
                PInvoke.SetConsoleCtrlHandler(routine, true);
            }
            current = attachment;
            return attachment;
        }
    }

    /// <summary>
    /// The app's shutdown is complete: a handler holding a closing terminal
    /// returns, and the system ends the process.
    /// </summary>
    public void ShutdownCompleted() => shutdown.TrySetResult();

    /// <summary>The processes attached to this process's console; empty without one.</summary>
    internal static uint[] ConsoleProcesses()
    {
        Span<uint> probe = stackalloc uint[64];
        var count = PInvoke.GetConsoleProcessList(probe);
        while (count > probe.Length)
        {
            probe = new uint[count + 16];
            count = PInvoke.GetConsoleProcessList(probe);
        }
        return probe[..(int)count].ToArray();
    }

    /// <summary>
    /// Routes one control event to the callback; true when it is one of
    /// ours (handled: no default ExitProcess).
    /// </summary>
    internal bool HandleControl(uint ctrlType)
    {
        ConsoleControl kind;
        switch (ctrlType)
        {
            case PInvoke.CTRL_C_EVENT:
                kind = ConsoleControl.Interrupt;
                break;
            case PInvoke.CTRL_BREAK_EVENT:
                kind = ConsoleControl.Break;
                break;
            case PInvoke.CTRL_CLOSE_EVENT:
                kind = ConsoleControl.Close;
                break;
            case PInvoke.CTRL_LOGOFF_EVENT:
                kind = ConsoleControl.Logoff;
                break;
            case PInvoke.CTRL_SHUTDOWN_EVENT:
                kind = ConsoleControl.Shutdown;
                break;
            default:
                return false;
        }
        // For these the process ends when this returns: it is held for the
        // shutdown, from the moment the event came.
        var holds = kind is ConsoleControl.Close or ConsoleControl.Logoff or ConsoleControl.Shutdown;
        var grace = holds ? Task.Delay(CloseGrace, time) : Task.CompletedTask;
        try
        {
            onControl?.Invoke(kind);
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            // A handler must return: the event is still ours.
        }
        if (holds)
        {
            Task.WhenAny(shutdown.Task, grace).Wait();
        }
        return true;
    }

    /// <summary>
    /// The process is back on its console after the daemon's stop borrowed
    /// the daemon's: a fresh CONOUT$ for the terminal. Called with
    /// <see cref="Gate"/> held.
    /// </summary>
    internal void Reattached() => Terminal?.ReopenConsoleOutput();

    // Neither NULL (no such handle) nor INVALID_HANDLE_VALUE.
    private static bool IsHandle(HANDLE handle) => handle != HANDLE.Null && (nint)handle != -1;

    private BOOL OnControl(uint ctrlType) => HandleControl(ctrlType);
}
