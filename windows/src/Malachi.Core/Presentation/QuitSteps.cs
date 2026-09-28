// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: the steps of QuitSequence, handed in by the app's
// composition root (and by the tests), so that the order is Core's and
// tested while the windows, the daemon and the exit are the app's.

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Controllers;

namespace Malachi.Core.Presentation;

/// <summary>What <see cref="QuitSequence"/> does, step by step; a step left out does nothing.</summary>
public sealed record QuitSteps
{
    /// <summary>
    /// Saves every compose window's unsaved changes as drafts and returns
    /// the windows whose save failed (<see cref="ComposeController.SaveForQuitAsync"/>).
    /// </summary>
    public Func<Task<IReadOnlyList<IComposeWindowHandle>>>? SaveDrafts { get; init; }

    /// <summary>
    /// Asks a window whose save failed its close question and closes it
    /// when allowed; false keeps it and abandons the Quit
    /// (<see cref="IComposeWindowHandle.CloseForQuitAsync"/> by default).
    /// </summary>
    public Func<IComposeWindowHandle, Task<bool>>? AskClose { get; init; }

    /// <summary>
    /// The point of no return: the windows hide, the notification-area icon
    /// goes, the supervisor starts no daemon any more.
    /// </summary>
    public Action? BeginStopping { get; init; }

    /// <summary>
    /// Closes the connection and stops the daemon this app started
    /// (<see cref="ConnectionController.StopAsync"/>).
    /// </summary>
    public Func<Task>? StopDaemon { get; init; }

    /// <summary>
    /// Lets go of what the app holds: the controllers, the attachments
    /// written for opening, the settings.
    /// </summary>
    public Action? Release { get; init; }

    /// <summary>Ends the application (Application.Exit).</summary>
    public Action? Exit { get; init; }
}
