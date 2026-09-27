// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: what the shell keeps for every window it tracks
// (WindowTracker), the counterpart of what an NSWindowController carries on
// macOS (its responder-chain actions, its toast presenter) and of a GTK
// window's action group.

using Malachi.App.Commands;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;

namespace Malachi.App.Shell;

/// <summary>A window of the app with its commands, its keys and its toasts.</summary>
public sealed class TrackedWindow
{
    internal TrackedWindow(Window window, WindowKind kind, WindowCommands commands, IToasts? toasts)
    {
        Window = window;
        Kind = kind;
        Commands = commands;
        Toasts = toasts;
    }

    /// <summary>The window.</summary>
    public Window Window { get; }

    /// <summary>What kind of window it is (its keys).</summary>
    public WindowKind Kind { get; }

    /// <summary>Its commands: bind buttons to them, set the handlers of the window's own.</summary>
    public WindowCommands Commands { get; }

    /// <summary>Its toast overlay, when it has one.</summary>
    public IToasts? Toasts { get; }

    /// <summary>Its keyboard (set once its root is known).</summary>
    internal CommandRouter? Router { get; set; }
}
