// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §10, "Background, tray, launch
// at login"): when closing a window ends the app. GTK gets it from
// GApplication (ui/main.go and window.go's close-request): the application
// lives while it holds itself (--gapplication-service, until the first
// activation shows a window) or while any of its windows exists, hidden or
// not; the main window hides instead of closing when "Run in Background"
// is on, and is destroyed otherwise. macOS asks the same of AppKit
// (applicationShouldTerminateAfterLastWindowClosed = !runInBackground,
// read each time). A WinUI app has one main window for the whole process
// (DispatcherShutdownMode.OnExplicitShutdown; the main window only ever
// hides), so this keeps GTK's bookkeeping: whether the main window counts
// as existing, the hold of a background start, and the other windows that
// are open.

using System;
using System.Collections.Generic;

namespace Malachi.Core.Presentation;

/// <summary>
/// Whether the app still has a reason to run once a window closed (the
/// GApplication rule). UI-thread-affine.
/// </summary>
public sealed class WindowLifetime
{
    private readonly HashSet<object> others = new(ReferenceEqualityComparer.Instance);

    /// <summary>A lifetime for an app started with a hold (<paramref name="startHidden"/>: <c>--background</c>) or without.</summary>
    public WindowLifetime(bool startHidden)
    {
        Held = startHidden;
    }

    /// <summary>
    /// The app runs without a window on purpose: started in the background,
    /// until its main window is first shown (GTK's service hold).
    /// </summary>
    public bool Held { get; private set; }

    /// <summary>
    /// The main window counts as existing: it was shown and has not been
    /// closed since, or it was hidden by "Run in Background" (GTK hides it
    /// then instead of destroying it).
    /// </summary>
    public bool MainWindowAlive { get; private set; }

    /// <summary>The other windows that are open (compose, message, preferences, wizard, ...).</summary>
    public int OtherWindows => others.Count;

    /// <summary>The main window was shown: it exists, and a background start's hold ends (GTK releases it on the first activation).</summary>
    public void MainWindowShown()
    {
        MainWindowAlive = true;
        Held = false;
    }

    /// <summary>
    /// The user closed the main window: it hides either way (the app keeps
    /// one); with <paramref name="runInBackground"/> it still counts as
    /// existing, otherwise as destroyed. True when the app should quit now.
    /// </summary>
    public bool MainWindowClosed(bool runInBackground)
    {
        MainWindowAlive = runInBackground;
        return ShouldQuit;
    }

    /// <summary>Another window opened (a window counts once however often it is reported).</summary>
    public void WindowOpened(object window)
    {
        ArgumentNullException.ThrowIfNull(window);
        others.Add(window);
    }

    /// <summary>Another window closed for good. True when the app should quit now.</summary>
    public bool WindowClosed(object window)
    {
        ArgumentNullException.ThrowIfNull(window);
        return others.Remove(window) && ShouldQuit;
    }

    /// <summary>Nothing holds the app any more: no hold, no main window, no other window.</summary>
    public bool ShouldQuit => !Held && !MainWindowAlive && others.Count == 0;
}
