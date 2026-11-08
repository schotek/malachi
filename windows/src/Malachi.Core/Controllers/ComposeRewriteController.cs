// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/ComposeRewriteController.swift
// (ComposeRewriteController: state, onState, running, start, cancel,
// finished); GTK: ui/internal/assistantpanel/oneshot.go (Rewriter,
// NewRewriter, State, Running, Start, Cancel, finished, and answerState,
// which the compose window's ComposeWriteController shares; account, the
// note of the window's account). Swift's onState is
// the event StateChanged, raised through ControllerEvents
// (docs/windows-port.md §7.5); Dispose (the window closed: Cancel, and no
// report afterwards) is Windows' own.

using System;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers.Infrastructure;

namespace Malachi.Core.Controllers;

/// <summary>
/// The compose window's rewrite (the In App target) without its popover: a
/// passage of the message, the selection or the user's own text, goes to the
/// user's assistant with a preset or the user's own instruction (the
/// daemon's prompt of purpose rewrite, <see cref="RewriteInput"/>, one
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
/// otherwise, a passage the daemon refuses as too long and an empty answer
/// included). A new rewrite cancels the one under
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

    /// <summary>
    /// The account the window writes from, whose profile note the daemon
    /// appends to the system prompt (read when the rewrite starts); null:
    /// none.
    /// </summary>
    public Func<AccountId?> Account { get; set; } = () => null;

    /// <summary>Whether an answer is awaited.</summary>
    public bool Running => State is RewriteState.Running;

    /// <summary>
    /// Asks for <paramref name="rewrite"/> of <paramref name="passage"/>
    /// (<paramref name="custom"/>: the user's own instruction, for
    /// <see cref="AssistantRewrite.Custom"/>). False, and nothing changes,
    /// when there is nothing to ask: an empty passage, or Custom without
    /// words. A passage the daemon refuses (too long, a rewrite that does
    /// not exist) fails with its reason.
    /// </summary>
    public bool Start(AssistantRewrite rewrite, string custom, string passage)
    {
        ArgumentNullException.ThrowIfNull(custom);
        ArgumentNullException.ThrowIfNull(passage);
        if (Assistant.TrimmedString(passage).Length == 0
            || (rewrite == AssistantRewrite.Custom && Assistant.TrimmedString(custom).Length == 0))
        {
            return false;
        }
        Set(new RewriteState.Running(""));
        Request.Start(
            PromptPurpose.Rewrite,
            new PromptInput
            {
                Rewrite = new RewriteInput { Preset = Assistant.RewritePresetOf(rewrite), Instruction = custom, Passage = passage },
            },
            Finished,
            noteAccountId: Account(),
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

    /// <summary>
    /// oneshot.go <c>answerState</c>: the state a rewrite or the compose
    /// window's writing (<see cref="ComposeWriteController"/>) ends in: the
    /// answer cleaned (<see cref="Assistant.CleanRewrite"/>), an empty one a
    /// failure; a failure's line; idle when the consent was declined.
    /// </summary>
    internal static RewriteState AnswerState(AssistantRequest.Outcome outcome)
    {
        switch (outcome)
        {
            case AssistantRequest.Outcome.Answered answered:
                var clean = Assistant.CleanRewrite(answered.Text);
                return clean.Length == 0
                    ? new RewriteState.Failed(Assistant.StoppedText("the answer is empty"))
                    : new RewriteState.Done(clean);
            case AssistantRequest.Outcome.Failed failed:
                return new RewriteState.Failed(failed.Failure.Text);
            default:
                return new RewriteState.Idle();
        }
    }

    private void Finished(AssistantRequest.Outcome outcome) => Set(AnswerState(outcome));

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
