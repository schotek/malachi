// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/ComposeWindowController.swift
// (writeWithAssistant: the bar's request and what its outcomes show), which
// keeps it in the window; GTK: ui/internal/assistantpanel/writer.go (Writer,
// NewWriter, Request, State, Running, set, Start, Cancel) over oneshot.go's
// answerState (ComposeRewriteController.AnswerState here). Go's OnState is
// the event StateChanged, raised through ControllerEvents
// (docs/windows-port.md §7.5); Dispose (the window closed: Cancel, and no
// report afterwards) is Windows' own, as the rewrite's. Swift's
// `String(describing: ComposeKind)` is Mode.

using System;
using Malachi.Core.Assistants;
using Malachi.Core.Compose;
using Malachi.Core.Controllers.Infrastructure;
using RewriteState = Malachi.Core.Controllers.ComposeRewriteController.RewriteState;

namespace Malachi.Core.Controllers;

/// <summary>
/// The compose window's assistant bar (the In App target) without its
/// widgets: the optional instruction, the subject, the user's own text and
/// the prepared original go to the user's assistant
/// (<see cref="Assistant.WriteMessage"/> under
/// <see cref="Assistant.WriteSystemPrompt"/>, one
/// <see cref="AssistantRequest"/>), and the answer, cleaned
/// (<see cref="Assistant.CleanRewrite"/>), is previewed in the bar for
/// Replace, which the window does as plain text through the editor bridge.
/// </summary>
/// <remarks>
/// It goes through the rewrite's states (<see cref="ComposeRewriteController.RewriteState"/>): the
/// answer so far while running, failures as lines to show (Claude Code not
/// found or not signed in, "The assistant stopped: …" otherwise, an empty
/// answer included), idle when the consent was declined. A new request
/// cancels the one under way; <see cref="Cancel"/> (the bar's Cancel or
/// Discard, a change of provider, the window closed) ends it and goes back
/// to idle, and a late answer is dropped. Call it on the UI thread.
/// </remarks>
public sealed class ComposeWriteController : IDisposable
{
    private readonly ControllerScope scope;

    /// <summary>Writing through <paramref name="request"/> (its consent hook is the window's).</summary>
    /// <param name="request">The request that asks.</param>
    /// <param name="pending">Reports what a handler of <see cref="StateChanged"/> throws; one of its own when null.</param>
    public ComposeWriteController(AssistantRequest request, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(request);
        Request = request;
        scope = new ControllerScope(pending);
    }

    /// <summary>The state changed (Go <c>OnState</c>).</summary>
    public event EventHandler<RewriteState>? StateChanged;

    /// <summary>The writing's request.</summary>
    public AssistantRequest Request { get; }

    /// <summary>Where the writing is.</summary>
    public RewriteState State { get; private set; } = new RewriteState.Idle();

    /// <summary>
    /// The user's profile with the note of the account the window writes
    /// from (<see cref="UserProfile.For"/>); empty by default.
    /// </summary>
    public Func<UserProfile> Profile { get; set; } = () => UserProfile.Empty;

    /// <summary>Whether an answer is awaited.</summary>
    public bool Running => State is RewriteState.Running;

    /// <summary>
    /// The mode of a window opened for <paramref name="kind"/>, as the request
    /// names it (Swift <c>String(describing: ComposeKind)</c>): a draft opened
    /// from the Drafts folder is <see cref="AssistantWriteMode.Edit"/>.
    /// </summary>
    public static AssistantWriteMode Mode(ComposeKind kind) => kind switch
    {
        ComposeKind.Reply => AssistantWriteMode.Reply,
        ComposeKind.ReplyAll => AssistantWriteMode.ReplyAll,
        ComposeKind.Forward => AssistantWriteMode.Forward,
        ComposeKind.Edit => AssistantWriteMode.Edit,
        _ => AssistantWriteMode.New,
    };

    /// <summary>
    /// Asks for the body of a window of <paramref name="mode"/>. A request
    /// that cannot be made (nothing to work from, too long;
    /// <see cref="Assistant.WriteMessage"/>) fails at once with its reason;
    /// nothing is sent then.
    /// </summary>
    /// <param name="mode">The kind of window.</param>
    /// <param name="instruction">The user's instruction (may be empty).</param>
    /// <param name="subject">The subject row.</param>
    /// <param name="own">The user's own text in the editor.</param>
    /// <param name="quote">The prepared original's text ("" without one).</param>
    public void Start(AssistantWriteMode mode, string instruction, string subject, string own, string quote)
    {
        ArgumentNullException.ThrowIfNull(instruction);
        ArgumentNullException.ThrowIfNull(subject);
        ArgumentNullException.ThrowIfNull(own);
        ArgumentNullException.ThrowIfNull(quote);
        string message;
        try
        {
            message = Assistant.WriteMessage(mode, instruction, subject, own, quote);
        }
        catch (AssistantException e)
        {
            Request.Cancel();
            Set(new RewriteState.Failed(Assistant.StoppedText(e.Message)));
            return;
        }
        Set(new RewriteState.Running(""));
        Request.Start(
            Assistant.WithProfile(Assistant.WriteSystemPrompt(), Profile()),
            message,
            outcome => Set(ComposeRewriteController.AnswerState(outcome)),
            onText: text =>
            {
                if (Running)
                {
                    Set(new RewriteState.Running(Assistant.CleanRewrite(text)));
                }
            });
    }

    /// <summary>Ends the writing under way and forgets the answer.</summary>
    public void Cancel()
    {
        Request.Cancel();
        Set(new RewriteState.Idle());
    }

    /// <summary>
    /// Cancels the writing and stops reporting (the window closed); the
    /// request is the window's to close.
    /// </summary>
    public void Dispose()
    {
        Cancel();
        scope.Close();
    }

    private void Set(RewriteState state)
    {
        if (state == State || scope.IsClosed)
        {
            return;
        }
        State = state;
        scope.Raise(StateChanged, this, state);
    }
}
