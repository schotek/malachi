// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The entry point, in place of the one the XAML compiler generates
// (DISABLE_XAML_GENERATED_MAIN): docs/windows-port.md §10 needs work before
// the application starts (registering for notifications, the single
// instance, redirecting a second launch). Scaffold (phase B): it only starts
// the application, as the generated Main does.

using System;
using System.Threading;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;

namespace Malachi.App;

/// <summary>Starts the WinUI application.</summary>
public static class Program
{
    [STAThread]
    private static void Main()
    {
        WinRT.ComWrappersSupport.InitializeComWrappers();
        Application.Start(callback =>
        {
            var context = new DispatcherQueueSynchronizationContext(DispatcherQueue.GetForCurrentThread());
            SynchronizationContext.SetSynchronizationContext(context);
            _ = new App();
        });
    }
}
