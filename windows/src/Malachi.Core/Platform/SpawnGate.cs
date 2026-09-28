// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §5): GTK and macOS start their
// children with only the descriptors they name (Go marks every descriptor
// close-on-exec, Foundation's Process closes the rest in the child), and
// Windows hands a child every inheritable handle of its parent unless the
// start lists the handles (PROC_THREAD_ATTRIBUTE_HANDLE_LIST). The daemon's
// start lists them (Malachi.Platform.Windows ChildProcess), but it has to
// make NUL and one pipe end inheritable while CreateProcess runs, and
// Process.Start, which starts the MCP bridge (BridgeRunner), lists nothing:
// a bridge started in that window would hold the daemon's output pipe, and
// the daemon's exit would wait for the bridge's. Both starts take this gate,
// so neither comes in between the other. It lives in Core, beside
// BridgeRunner, and holds no Windows API; Malachi.Platform.Windows takes it
// inside the console gate of the daemon's start, never the other way round.

using System.Threading;

namespace Malachi.Core.Platform;

/// <summary>
/// The lock every start of a child process takes while the app has
/// handles made inheritable for it, or while the start would hand on
/// whatever is inheritable: the daemon's start from the creation of its
/// NUL handle and pipe to their close, the bridge's around
/// <c>Process.Start</c>. Held for the synchronous start alone, never
/// across an await.
/// </summary>
public static class SpawnGate
{
    private static readonly Lock Gate = new();

    /// <summary>
    /// Waits for the gate and holds it until the returned scope is
    /// disposed (<c>using (SpawnGate.Enter()) { … }</c>).
    /// </summary>
    public static Lock.Scope Enter() => Gate.EnterScope();

    /// <summary>Whether the calling thread holds the gate.</summary>
    public static bool IsHeldByCurrentThread => Gate.IsHeldByCurrentThread;
}
