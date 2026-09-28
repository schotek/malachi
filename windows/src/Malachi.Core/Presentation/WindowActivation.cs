// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §10, §11.4): which of the app's
// windows is active, and which one was active last. GTK asks the window
// (notify.go: no desktop notification while w.IsActive(), the main window
// being the active one) and macOS asks isKeyWindow; both are false as soon
// as another application is in the foreground. WinUI reports it per window
// (Window.Activated with CodeActivated, PointerActivated or Deactivated),
// so the shell keeps the answer here:
//
// - Focused: the window that is active now; none while another
//   application has the foreground. A deactivation only clears the window
//   it names, so the order in which two of the app's windows report the
//   switch does not matter.
// - LastActive: the window that was activated last, kept while the app is
//   in the background: where the application's toasts go and which window
//   owns a dialog or a launched program.
//
// A window that closes is neither.

using System;

namespace Malachi.Core.Presentation;

/// <summary>The app's active window and its last active one. UI-thread-affine.</summary>
public sealed class WindowActivation
{
    /// <summary>The window that is active now; null while another application is in the foreground.</summary>
    public object? Focused { get; private set; }

    /// <summary>The window that was activated last, also while the app is in the background.</summary>
    public object? LastActive { get; private set; }

    /// <summary>Whether <paramref name="window"/> is the active window now.</summary>
    public bool IsActive(object window)
    {
        ArgumentNullException.ThrowIfNull(window);
        return ReferenceEquals(Focused, window);
    }

    /// <summary>
    /// <paramref name="window"/> became active. True when it was not the
    /// last active window before (the app's toasts move to it).
    /// </summary>
    public bool Activated(object window)
    {
        ArgumentNullException.ThrowIfNull(window);
        Focused = window;
        if (ReferenceEquals(LastActive, window))
        {
            return false;
        }
        LastActive = window;
        return true;
    }

    /// <summary>
    /// <paramref name="window"/> lost the activation (another window or
    /// another application has it); it stays the last active one.
    /// </summary>
    public void Deactivated(object window)
    {
        ArgumentNullException.ThrowIfNull(window);
        if (ReferenceEquals(Focused, window))
        {
            Focused = null;
        }
    }

    /// <summary>
    /// <paramref name="window"/> closed. True when it was the last active
    /// window (which is none now).
    /// </summary>
    public bool Closed(object window)
    {
        ArgumentNullException.ThrowIfNull(window);
        Deactivated(window);
        if (!ReferenceEquals(LastActive, window))
        {
            return false;
        }
        LastActive = null;
        return true;
    }
}
