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
    /// First of all, the board's inline replies (macOS AppDelegate's
    /// BoardReplyEditorHost.finishAll, GTK FinishBoardReplies): they settle
    /// while the connection stands, and one that could not be saved or sent
    /// asks "Quit without saving a reply?"; false (Cancel) abandons the Quit.
    /// </summary>
    public Func<Task<bool>>? BoardReplies { get; init; }

    /// <summary>
    /// A session end's settle of the board's inline replies, without a
    /// question: what can be saved is saved while the connection stands,
    /// and nothing waits for the user. The sequence waits for it at most
    /// <see cref="SettleWait"/>; it runs first at a session end, never on a
    /// user's Quit (that one has <see cref="BoardReplies"/>).
    /// </summary>
    public Func<Task>? SettleBoardReplies { get; init; }

    /// <summary>How long a session end waits for <see cref="SettleBoardReplies"/> at most.</summary>
    public TimeSpan SettleWait { get; init; } = BoardReplyController.EndWait;

    /// <summary>
    /// A user's Quit was abandoned (a question answered Cancel, or the drafts
    /// could not be saved): the app runs on, so the board shows its replies
    /// again (macOS resumeAll, GTK ResumeBoardReplies).
    /// </summary>
    public Action? Abandoned { get; init; }

    /// <summary>
    /// Past the point of no return, beside <see cref="StopTriage"/>: the
    /// suggested reply under way stops and deletes a draft it did not link
    /// yet (<see cref="BoardReplyController.CancelAndCleanUpAsync"/>, which
    /// waits at most <see cref="BoardReplyController.EndWait"/>). Here, not
    /// in <see cref="BoardReplies"/>, so that an abandoned Quit leaves it
    /// running.
    /// </summary>
    public Func<Task>? EndBoardReply { get; init; }

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
    /// Ends the board's triage while the connection still stands: the
    /// schedule stops and a run under way ends, waiting for its
    /// <c>board.runEnd</c> at most <see cref="BoardTriageController.EndWait"/>
    /// (<see cref="BoardTriageController.CancelAndEndAsync"/>).
    /// </summary>
    public Func<Task>? StopTriage { get; init; }

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
