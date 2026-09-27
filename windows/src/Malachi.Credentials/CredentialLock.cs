// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows only, no Swift counterpart (the Keychain serialises its own
// callers): one malachi-credentials at a time in the user's session.
// Credential Manager loses updates when several processes use it at once,
// even when all but one only read (measured on Windows 11 24H2: a write
// that reads back as missing, a deleted item that comes back), while the
// calls of one process are consistent. The daemon runs a helper per
// operation and may run several at once, so every run takes this lock
// around its store operation (docs/windows-port.md §10).

using System;
using System.Threading;

namespace Malachi.Credentials;

/// <summary>A named mutex every helper run holds while it uses the store.</summary>
/// <param name="Name">The mutex's name, in the session's namespace.</param>
/// <param name="Timeout">How long a run waits for the one before it.</param>
internal sealed record CredentialLock(string Name, TimeSpan Timeout)
{
    /// <summary>
    /// The lock of the user's session: what the helper takes. Ten seconds
    /// is ample for the runs queued before it and well inside the 30 s the
    /// daemon gives a run.
    /// </summary>
    public static CredentialLock Session { get; } = new(@"Local\io.github.schotek.Malachi.credentials", TimeSpan.FromSeconds(10));

    /// <summary>
    /// Waits for the lock; the turn releases it when disposed. Null when the
    /// wait timed out. A run that died holding it (the daemon kills one
    /// that overruns) leaves it abandoned, and the next run simply takes it.
    /// </summary>
    public Turn? Take()
    {
        var mutex = new Mutex(false, Name);
        try
        {
            if (mutex.WaitOne(Timeout))
            {
                return new Turn(mutex);
            }
        }
        catch (AbandonedMutexException)
        {
            return new Turn(mutex);
        }
        mutex.Dispose();
        return null;
    }

    /// <summary>The lock, held until disposed (on the thread that took it).</summary>
    internal sealed class Turn(Mutex mutex) : IDisposable
    {
        /// <inheritdoc/>
        public void Dispose()
        {
            mutex.ReleaseMutex();
            mutex.Dispose();
        }
    }
}
