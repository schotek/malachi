// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ComposeRewriteController.swift
// (ComposeRewriteController: state, onState, running, start, cancel,
// finished); GTK: ui/internal/assistantpanel/oneshot.go (Rewriter,
// NewRewriter, State, Running, Start, Cancel, finished). Swift's onState is
// the event StateChanged, raised through ControllerEvents
// (docs/windows-port.md §7.5); Dispose (the window closed: Cancel, and no
// report afterwards) is Windows' own.

using System;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers.Infrastructure;

namespace Malachi.Core.Controllers;

/// <summary>
/// The compose window's rewrite (the In App target) without its popover: a
/// passage of the message, the selection or the user's own text, goes to the
/// user's Claude Code with a preset or the user's own instruction
/// (<see cref="Assistant.RewriteMessage"/> under
/// <see cref="Assistant.RewriteSystemPrompt"/>, one
/// <see cref="AssistantRequest"/>), and the answer, cleaned
/// (<see cref="Assistant.CleanRewrite"/>), is offered for Replace or Insert
/// Below, which the window does as plain text.
/// </summary>
/// <remarks>
/// <see cref="State"/>: <see cref="RewriteState.Idle"/> (nothing asked, or
/// the consent was declined), <see cref="RewriteState.Running"/> with the
/// answer as it streams (cleaned the same way),
/// <see cref="RewriteState.Done"/> with the text to insert,
/// <see cref="RewriteState.Failed"/> with the line to show (Claude Code not
/// found or not signed in, the panel's texts; "The assistant stopped: …"
/// otherwise, an empty answer included). A new rewrite cancels the one under
/// way; <see cref="Cancel"/> (the popover or the window closed) ends it and
/// goes back to idle. Call it on the UI thread.
/// </remarks>
public sealed partial class ComposeRewriteController : IDisposable
{
    private readonly ControllerScope scope;

    /// <summary>A rewrite through <paramref name="request"/> (its consent hook is the window's).</summary>
    /// <param name="request">The request that asks.</param>
    /// <param name="pending">Reports what a handler of <see cref="StateChanged"/> throws; one of its own when null.</param>
    public ComposeRewriteController(AssistantRequest request, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(request);
        Request = request;
        scope = new ControllerScope(pending);
    }

    /// <summary>The state changed (Swift <c>onState</c>).</summary>
    public event EventHandler<RewriteState>? StateChanged;

    /// <summary>The rewrite's request.</summary>
    public AssistantRequest Request { get; }

    /// <summary>Where the rewrite is.</summary>
    public RewriteState State { get; private set; } = new RewriteState.Idle();

    /// <summary>Whether an answer is awaited.</summary>
    public bool Running => State is RewriteState.Running;

    /// <summary>
    /// Asks for <paramref name="rewrite"/> of <paramref name="passage"/>
    /// (<paramref name="custom"/>: the user's own instruction, for
    /// <see cref="AssistantRewrite.Custom"/>). False, and nothing changes,
    /// when there is nothing to ask: an empty passage, or Custom without
    /// words. A passage that is too long (or a rewrite that does not exist)
    /// fails at once.
    /// </summary>
    public bool Start(AssistantRewrite rewrite, string custom, string passage)
    {
        ArgumentNullException.ThrowIfNull(custom);
        ArgumentNullException.ThrowIfNull(passage);
        string message;
        try
        {
            message = Assistant.RewriteMessage(rewrite, custom, passage);
        }
        catch (AssistantException e) when (e.Kind is AssistantError.NoPassage or AssistantError.NoInstruction)
        {
            return false;
        }
        catch (AssistantException e)
        {
            Request.Cancel();
            Set(new RewriteState.Failed(Assistant.StoppedText(e.Message)));
            return true;
        }
        Set(new RewriteState.Running(""));
        Request.Start(
            Assistant.RewriteSystemPrompt(),
            message,
            Finished,
            onText: text =>
            {
                if (Running)
                {
                    Set(new RewriteState.Running(Assistant.CleanRewrite(text)));
                }
            });
        return true;
    }

    /// <summary>Ends the rewrite under way (the popover or the window closed) and forgets the answer.</summary>
    public void Cancel()
    {
        Request.Cancel();
        Set(new RewriteState.Idle());
    }

    /// <summary>
    /// Cancels the rewrite and stops reporting (the window closed); the
    /// request is the window's to close.
    /// </summary>
    public void Dispose()
    {
        Cancel();
        scope.Close();
    }

    private void Finished(AssistantRequest.Outcome outcome)
    {
        switch (outcome)
        {
            case AssistantRequest.Outcome.Answered answered:
                var clean = Assistant.CleanRewrite(answered.Text);
                Set(clean.Length == 0
                    ? new RewriteState.Failed(Assistant.StoppedText("the answer is empty"))
                    : new RewriteState.Done(clean));
                break;
            case AssistantRequest.Outcome.Failed failed:
                Set(new RewriteState.Failed(failed.Failure.Text));
                break;
            default:
                Set(new RewriteState.Idle());
                break;
        }
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
