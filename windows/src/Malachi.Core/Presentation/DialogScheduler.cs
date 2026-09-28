// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §11.4): a WinUI window shows one
// ContentDialog at a time (a second ShowAsync throws), while GTK stacks
// Adw.AlertDialogs and macOS runs a second NSAlert application-modal
// (Alerts.swift run(_:on:)). The alert service keeps one of these per
// window: every dialog waits for the ones asked before it, in order, and a
// dialog that fails does not block the next.

using System;
using System.Threading.Tasks;

namespace Malachi.Core.Presentation;

/// <summary>Runs one dialog after the other. UI-thread-affine.</summary>
public sealed class DialogScheduler
{
    private Task tail = Task.CompletedTask;
    private int count;

    /// <summary>Whether a dialog is shown or waits.</summary>
    public bool IsBusy => count > 0;

    /// <summary>How many dialogs are shown or wait.</summary>
    public int Count => count;

    /// <summary>
    /// Runs <paramref name="show"/> once every dialog enqueued before it has
    /// ended, and returns its answer.
    /// </summary>
    public Task<T> Enqueue<T>(Func<Task<T>> show)
    {
        ArgumentNullException.ThrowIfNull(show);
        count++;
        var previous = tail;
        var run = RunAfterAsync(previous, show);
        tail = run;
        return run;
    }

    private async Task<T> RunAfterAsync<T>(Task previous, Func<Task<T>> show)
    {
        try
        {
            try
            {
                await previous;
            }
            catch (Exception e) when (e is not OutOfMemoryException)
            {
                // The earlier dialog's caller has its failure; this one runs.
            }
            return await show();
        }
        finally
        {
            count--;
        }
    }
}
