// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: a thread of its own in a single-threaded apartment for the
// shell's COM work (Attachment Services, ShellExecuteEx, the Open With
// dialog), which wants COM initialised as STA and may take a while (an
// antivirus scan, a dialog) that the UI thread must not wait for.

using System;
using System.Threading;
using System.Threading.Tasks;

namespace Malachi.Platform.Windows.Launch;

/// <summary>Runs work on a new STA thread and completes with its result.</summary>
internal static class StaThread
{
    /// <summary>
    /// Runs <paramref name="work"/> on a new background STA thread (COM
    /// initialised apartment-threaded, uninitialised when it ends).
    /// Cancellation is looked at before it starts; the shell's calls cannot
    /// be interrupted.
    /// </summary>
    public static Task<T> RunAsync<T>(Func<T> work, CancellationToken cancellationToken)
    {
        ArgumentNullException.ThrowIfNull(work);
        if (cancellationToken.IsCancellationRequested)
        {
            return Task.FromCanceled<T>(cancellationToken);
        }
        var completion = new TaskCompletionSource<T>(TaskCreationOptions.RunContinuationsAsynchronously);
        var thread = new Thread(() =>
        {
            try
            {
                completion.TrySetResult(work());
            }
            catch (Exception e)
            {
                completion.TrySetException(e);
            }
        })
        {
            IsBackground = true,
            Name = "Malachi shell (STA)",
        };
        thread.SetApartmentState(ApartmentState.STA);
        thread.Start();
        return completion.Task;
    }
}
