// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: whether this copy of the app writes its registrations
// into the user's registry by itself at start (docs/windows-port.md §1,
// §10): the mailto: registration when it is missing or stale, and a Run
// value of a moved app folder pointed at this executable. Both follow the
// executable, so a copy started from a temporary folder (a worktree or dev
// build that a test, an agent or a developer runs) would point the user's
// real mailto: handler and Run value at a path that is deleted later,
// breaking mailto: for a user who chose Malachi Mail as the default mail
// app. MALACHI_DATA_DIR, a Windows-only variable, is what tests and
// agents set to keep such a copy off the user's data; with it set the copy
// also leaves those registrations alone at start. What the user asks for in
// Preferences (launch at login, the Default apps page) is still done: that
// is a request, not a side effect of starting. An empty value counts as
// unset, as it does for the data directory (Malachi.Core.Daemon.Paths).

using System;

namespace Malachi.Platform.Windows.Registration;

/// <summary>Whether the app keeps its <c>mailto:</c> registration and Run value current at start.</summary>
public static class SelfRegistration
{
    /// <summary>The variable that moves the data directory for tests and agents.</summary>
    public const string DataDirVariable = "MALACHI_DATA_DIR";

    /// <summary>
    /// Whether this process may write its registrations at start: false
    /// while <c>MALACHI_DATA_DIR</c> is set.
    /// </summary>
    public static bool IsAllowed() => IsAllowed(Environment.GetEnvironmentVariable(DataDirVariable));

    /// <summary>
    /// Whether a process whose <c>MALACHI_DATA_DIR</c> is
    /// <paramref name="dataDirOverride"/> may write its registrations at
    /// start: only when it is unset or empty.
    /// </summary>
    public static bool IsAllowed(string? dataDirOverride) => string.IsNullOrEmpty(dataDirOverride);
}
