// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Waits for a view's own document to finish loading after a load step, or
// not at all when none started (the viewer does not reload the body it
// shows, as macOS's loadedBody; the previewer's panel loads nothing).

using System;
using System.Threading.Tasks;
using Microsoft.Web.WebView2.Core;

namespace Malachi.App.Canary.Host;

/// <summary>Watches one view for the navigation a load step starts.</summary>
internal sealed class LoadWatch : IDisposable
{
    private static readonly TimeSpan StartTimeout = TimeSpan.FromSeconds(1);
    private static readonly TimeSpan LoadTimeout = TimeSpan.FromSeconds(8);

    private readonly CoreWebView2 core;
    private readonly TaskCompletionSource started = new(TaskCreationOptions.RunContinuationsAsynchronously);
    private readonly TaskCompletionSource completed = new(TaskCreationOptions.RunContinuationsAsynchronously);

    /// <summary>Starts watching <paramref name="core"/> (before the load is asked for).</summary>
    public LoadWatch(CoreWebView2 core)
    {
        this.core = core;
        core.NavigationStarting += OnStarting;
        core.NavigationCompleted += OnCompleted;
    }

    /// <summary>Returns once the started navigation completed, or when none started within a second.</summary>
    public async Task WaitAsync()
    {
        if (await Task.WhenAny(started.Task, Task.Delay(StartTimeout)) != started.Task)
        {
            return;
        }
        await Task.WhenAny(completed.Task, Task.Delay(LoadTimeout));
    }

    public void Dispose()
    {
        core.NavigationStarting -= OnStarting;
        core.NavigationCompleted -= OnCompleted;
    }

    private void OnStarting(CoreWebView2 sender, CoreWebView2NavigationStartingEventArgs args)
    {
        if (!args.Cancel)
        {
            started.TrySetResult();
        }
    }

    private void OnCompleted(CoreWebView2 sender, CoreWebView2NavigationCompletedEventArgs args) => completed.TrySetResult();
}
