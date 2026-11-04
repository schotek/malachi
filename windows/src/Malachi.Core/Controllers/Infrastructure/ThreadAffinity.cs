// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The counterpart of Swift's @MainActor on the controllers of
// macos/Sources/MalachiCore/Controllers (and of the GTK UI's rule that
// everything runs in the main loop, glib.IdleAdd from elsewhere):
// docs/windows-port.md §7.1. Windows-only file: C# has no actor isolation,
// so the thread is recorded and checked.

using System;
using System.Diagnostics;
using System.Threading;

namespace Malachi.Core.Controllers.Infrastructure;

/// <summary>
/// The thread an object belongs to: the UI thread whose
/// <see cref="SynchronizationContext"/> (WinUI's
/// DispatcherQueueSynchronizationContext, a single-thread context in tests)
/// brings every continuation of its <c>await</c>s back to it. The
/// controllers keep their generation counters, <c>op</c> counters and
/// <c>closed</c> flags without locks because of it; <see cref="VerifyAccess"/>
/// catches a call from elsewhere in debug builds.
/// </summary>
public sealed class ThreadAffinity
{
    /// <summary>Belongs to the calling thread and its synchronization context.</summary>
    public ThreadAffinity()
    {
        ThreadId = Environment.CurrentManagedThreadId;
        Context = SynchronizationContext.Current;
    }

    /// <summary>The managed id of the owning thread.</summary>
    public int ThreadId { get; }

    /// <summary>The owning thread's context when the object was made; null for a thread without one.</summary>
    public SynchronizationContext? Context { get; }

    /// <summary>Whether the calling thread is the owning one.</summary>
    public bool CheckAccess() => Environment.CurrentManagedThreadId == ThreadId;

    /// <summary>
    /// Throws <see cref="InvalidOperationException"/> when called from
    /// another thread; compiled away outside debug builds of the caller.
    /// </summary>
    [Conditional("DEBUG")]
    public void VerifyAccess()
    {
        if (!CheckAccess())
        {
            throw new InvalidOperationException(
                $"called on thread {Environment.CurrentManagedThreadId}; the object belongs to the UI thread {ThreadId}");
        }
    }
}
