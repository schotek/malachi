// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Shared/ToastPresenter.swift (the queue
// of show, dismissCurrent, present, replaceCurrent, scheduleDismissal and
// presentNext); GTK: Adw.ToastOverlay as window.go Toast (5 s) and ToastFor
// use it; the name is the Swift view's, since this half decides what it
// presents. A presentation class that macOS keeps in AppKit
// (docs/windows-port.md §7.4): the WinUI overlay (Malachi.App ToastHost)
// draws Current and animates its changes. One toast at a time, the rest
// queued in order; 0 seconds keeps a toast until the next one replaces it,
// and a toast without a timeout gives way to the next one at once. Swift's
// DispatchWorkItem is a timer on the injected TimeProvider, whose tick is
// posted to the synchronization context the presenter was made on (the UI
// thread); without one it runs where it fires (tests on a fake clock).

using System;
using System.Collections.Generic;
using System.Threading;

namespace Malachi.Core.Presentation;

/// <summary>The toasts of one window: what is shown now and what waits. UI-thread-affine.</summary>
public sealed class ToastPresenter : IDisposable
{
    /// <summary>How long a toast stays by default (window.go <c>Toast</c>).</summary>
    public const int DefaultSeconds = 5;

    private readonly Queue<Toast> waiting = new();
    private readonly TimeProvider time;
    private readonly SynchronizationContext? context;
    private ITimer? dismissal;
    private int generation;

    /// <summary>A queue whose timeouts run on <paramref name="timeProvider"/> (the system clock when null).</summary>
    public ToastPresenter(TimeProvider? timeProvider = null)
    {
        time = timeProvider ?? TimeProvider.System;
        context = SynchronizationContext.Current;
    }

    /// <summary>
    /// <see cref="Current"/> changed: a toast appeared, was replaced, or
    /// went (null). The view shows the new one, or hides.
    /// </summary>
    public event EventHandler<Toast?>? CurrentChanged;

    /// <summary>The toast shown now, or null.</summary>
    public Toast? Current { get; private set; }

    /// <summary>How many toasts wait for the current one.</summary>
    public int Waiting => waiting.Count;

    /// <summary>
    /// Shows <paramref name="text"/> for <paramref name="seconds"/> (the
    /// default 5 s; 0 until the next toast replaces it): now when nothing
    /// is shown or the current toast has no timeout, after the others
    /// otherwise.
    /// </summary>
    public void Show(string text, int seconds = DefaultSeconds)
    {
        ArgumentNullException.ThrowIfNull(text);
        var toast = new Toast(text, Math.Max(seconds, 0));
        if (Current is { Seconds: 0 })
        {
            // A toast without a timeout gives way to the next one at once.
            Present(toast);
            return;
        }
        if (Current is null)
        {
            Present(toast);
        }
        else
        {
            waiting.Enqueue(toast);
        }
    }

    /// <summary>Hides the current toast now (a click on it) and shows the next queued one, if any.</summary>
    public void DismissCurrent()
    {
        if (Current is null)
        {
            return;
        }
        CancelDismissal();
        Current = null;
        CurrentChanged?.Invoke(this, null);
        if (waiting.Count > 0)
        {
            Present(waiting.Dequeue());
        }
    }

    /// <summary>Stops the timer; nothing changes afterwards on its own.</summary>
    public void Dispose()
    {
        CancelDismissal();
        waiting.Clear();
    }

    private void Present(Toast toast)
    {
        CancelDismissal();
        Current = toast;
        CurrentChanged?.Invoke(this, toast);
        if (toast.Seconds == 0)
        {
            return;
        }
        var ticket = generation;
        dismissal = time.CreateTimer(_ => Tick(ticket), null, TimeSpan.FromSeconds(toast.Seconds), Timeout.InfiniteTimeSpan);
    }

    private void Tick(int ticket)
    {
        if (context is null)
        {
            Expire(ticket);
        }
        else
        {
            context.Post(_ => Expire(ticket), null);
        }
    }

    // A timer that fired for a toast that is no longer current (replaced or
    // dismissed while its tick was on its way) does nothing.
    private void Expire(int ticket)
    {
        if (ticket == generation)
        {
            DismissCurrent();
        }
    }

    private void CancelDismissal()
    {
        generation++;
        dismissal?.Dispose();
        dismissal = null;
    }
}
