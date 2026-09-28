// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the kinds of AppInstance activation the app tells
// apart (Microsoft.Windows.AppLifecycle.ExtendedActivationKind), without
// the Windows App SDK in Core. GTK: the activate and open signals of
// ui/main.go; macOS: applicationDidFinishLaunching, application(_:open:)
// and applicationShouldHandleReopen of AppDelegate.swift.

namespace Malachi.Core.Presentation;

/// <summary>How the app was started or reached by a second launch.</summary>
public enum ActivationKind
{
    /// <summary>A launch with a command line: the Start menu, a terminal, the Run key, a <c>mailto:</c> link.</summary>
    Launch,

    /// <summary>A click on one of the app's notifications (a cold start through COM, or a redirect).</summary>
    Notification,

    /// <summary>A protocol activation carrying one URI (never registered by the unpackaged app; handled as a launch with it).</summary>
    Protocol,

    /// <summary>Anything else (a file, the share target, ...): the main window.</summary>
    Other,
}
