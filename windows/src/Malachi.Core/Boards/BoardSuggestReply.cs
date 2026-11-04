// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardSuggestReply.swift; Go:
// ui/internal/board/suggest_reply.go (SuggestReplyState, SuggestReplyOffered,
// SuggestReplyInputs, SuggestReplyView, SuggestReplyViewOf).
//
// The rules and the view model of the case detail's Suggest Reply control
// (the reply controller runs the request): when it is offered, when it can
// run, and what it shows. Pure; the detail only shows the view model. The
// ChatGPT branches are Swift's (Go's view has no provider). Swift's
// suggestReplyView and SuggestReplyView share a name: the function is Go's
// SuggestReplyViewOf. A run for another case disables the control before
// anything else, as both do.

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.I18n;

namespace Malachi.Core.Boards;

public static partial class Board
{
    /// <summary>Why a suggested reply brought no draft.</summary>
    public enum SuggestReplyFailure
    {
        /// <summary>Claude Code was not found.</summary>
        NotFound,

        /// <summary>Claude Code is signed out.</summary>
        NotSignedIn,

        /// <summary>The bridge is missing.</summary>
        ToolsMissing,

        /// <summary>It took too long.</summary>
        Timeout,

        /// <summary>The user pressed Stop.</summary>
        Cancelled,

        /// <summary>Claude Code ended badly.</summary>
        Stopped,

        /// <summary>The daemon did not answer (board.get, board.setDraft).</summary>
        Backend,

        /// <summary>The model finished without creating a draft.</summary>
        NoDraft,

        /// <summary>The assistant's usage limit was reached.</summary>
        Limit,
    }

    /// <summary>Every <see cref="SuggestReplyFailure"/> (Swift <c>allCases</c>, Go <c>SuggestReplyFailures</c>).</summary>
    public static ReadOnlySpan<SuggestReplyFailure> SuggestReplyFailures =>
    [
        SuggestReplyFailure.NotFound, SuggestReplyFailure.NotSignedIn, SuggestReplyFailure.ToolsMissing,
        SuggestReplyFailure.Timeout, SuggestReplyFailure.Cancelled, SuggestReplyFailure.Stopped,
        SuggestReplyFailure.Backend, SuggestReplyFailure.NoDraft, SuggestReplyFailure.Limit,
    ];

    /// <summary>What the application's one suggested reply is doing.</summary>
    public abstract record SuggestReplyState
    {
        // Only the cases below derive from it.
        private SuggestReplyState()
        {
        }

        /// <summary>A request runs.</summary>
        public bool IsRunning => this is Running;

        /// <summary>Nothing runs.</summary>
        public sealed record Idle : SuggestReplyState;

        /// <summary>A request for case <paramref name="Case"/> runs (asking, writing, linking).</summary>
        /// <param name="Case">The case.</param>
        public sealed record Running(BoardCaseId Case) : SuggestReplyState;

        /// <summary>
        /// The last request, for case <paramref name="Case"/>, failed; shown on
        /// that case until another request starts.
        /// </summary>
        /// <param name="Case">The case.</param>
        /// <param name="Failure">Why.</param>
        public sealed record Failed(BoardCaseId Case, SuggestReplyFailure Failure) : SuggestReplyState;
    }

    /// <summary>What <see cref="SuggestReplyViewOf"/> looks at.</summary>
    public sealed record SuggestReplyInputs
    {
        /// <summary>The in-app provider.</summary>
        public AssistantProviderID Provider { get; init; } = AssistantProviderID.Claude;

        /// <summary><see cref="SuggestReplyOffered"/> for the case shown.</summary>
        public required bool Offered { get; init; }

        /// <summary>
        /// The feature can exist: the assistant shown with the In App target
        /// (as the compose rewrite) and the bridge beside the application.
        /// </summary>
        public required bool Available { get; init; }

        /// <summary>Claude Code (or Codex) was found.</summary>
        public required bool ClaudeFound { get; init; }

        /// <summary>As last asked; null when not known (counts as signed in).</summary>
        public required bool? SignedIn { get; init; }

        /// <summary>The application's suggested reply.</summary>
        public required SuggestReplyState State { get; init; }

        /// <summary>The case shown.</summary>
        public required BoardCaseId CaseId { get; init; }

        /// <summary><see cref="IsFollowUp"/> for the case shown: the button reads <see cref="Text.SuggestFollowUp"/>.</summary>
        public bool FollowUp { get; init; }
    }

    /// <summary>The control in the place of the Suggested Reply block. Fixed texts; nothing from mail or from the model.</summary>
    public sealed record SuggestReplyView
    {
        /// <summary>The control when it is not there.</summary>
        public static SuggestReplyView Hidden { get; } = new();

        /// <summary>The control is there at all.</summary>
        public bool Shown { get; init; }

        /// <summary>The field and the button take input.</summary>
        public bool Enabled { get; init; }

        /// <summary>The request runs for this case: the spinner, the progress and Stop instead of the button.</summary>
        public bool Running { get; init; }

        /// <summary>The line under the field: why it is disabled, or how the last request for this case failed; "" for none.</summary>
        public string Note { get; init; } = "";

        /// <summary>The note is a failure (shown as an error line).</summary>
        public bool NoteIsFailure { get; init; }

        /// <summary>The button.</summary>
        public string Title { get; init; } = "";

        /// <summary>The field's placeholder.</summary>
        public string Placeholder { get; init; } = "";

        /// <summary>The progress while it runs.</summary>
        public string Progress { get; init; } = "";

        /// <summary>The Stop button.</summary>
        public string Stop { get; init; } = "";
    }

    /// <summary>
    /// Whether the detail of <paramref name="c"/> offers Suggest Reply: a real
    /// case (not the samples) with a message a reply answers, no suggested
    /// reply yet, not for reading only (its state in effect is not
    /// <see cref="State.Info"/>), not done, in an account that can reply. An
    /// account not listed (yet) counts as a mail account unless the case is
    /// an issue.
    /// </summary>
    public static bool SuggestReplyOffered(Case c, Snapshot s, bool samples)
    {
        ArgumentNullException.ThrowIfNull(c);
        ArgumentNullException.ThrowIfNull(s);
        if (samples || c.Reply is null || c.Draft is not null || c.Visibility.IsDone || StateOf(c, s.Annotated) == State.Info)
        {
            return false;
        }
        var account = s.Accounts.FirstOrDefault(a => a.Id == c.Account);
        return account is not null ? account.CanReply : c.Issue is null;
    }

    /// <summary>
    /// Whether <paramref name="reason"/> is a rule code under which the
    /// case's reply target is the user's own last message (<c>them.*</c>): a
    /// suggested reply there is a follow-up, a nudge on that message, not an
    /// answer.
    /// </summary>
    public static bool IsFollowUpReason(BoardReason reason) =>
        (reason.Value ?? "").StartsWith("them.", StringComparison.Ordinal);

    /// <summary>
    /// Whether the suggested reply of <paramref name="c"/> is a follow-up
    /// (Go <c>IsFollowUp</c>): the state the client shows for it
    /// (<see cref="StateOf"/>: the user's choice, else the assistant's
    /// annotation when <paramref name="annotated"/> and not stale, else the
    /// rules') is Them. The effective state decides, not the rule code: a
    /// them.replied case the user moved to You is no follow-up, a case the
    /// user keeps in Them is one. The control reads
    /// <see cref="Text.SuggestFollowUp"/> and the request tells the assistant
    /// it nudges the user's own message.
    /// </summary>
    public static bool IsFollowUp(Case c, bool annotated)
    {
        ArgumentNullException.ThrowIfNull(c);
        return StateOf(c, annotated) == State.Them;
    }

    /// <summary><see cref="IsFollowUp(Case, bool)"/> for a case as the daemon sent it (Go <c>IsFollowUpWire</c>).</summary>
    public static bool IsFollowUpWire(BoardCase c, bool annotated)
    {
        ArgumentNullException.ThrowIfNull(c);
        return IsFollowUp(DaemonBoardSource.Convert(c), annotated);
    }

    /// <summary>
    /// The control for <paramref name="i"/>: hidden unless offered and
    /// available; while the request runs for this case its progress and
    /// Stop; disabled, with the reason, while it runs for another case,
    /// without Claude Code, or signed out (the rewrite's hint pointing to
    /// Settings → AI); the last failure for this case under the usable
    /// control.
    /// </summary>
    public static SuggestReplyView SuggestReplyViewOf(SuggestReplyInputs i)
    {
        ArgumentNullException.ThrowIfNull(i);
        if (!i.Offered || !i.Available)
        {
            return SuggestReplyView.Hidden;
        }
        var chatGpt = i.Provider == AssistantProviderID.ChatGpt;
        var v = new SuggestReplyView
        {
            Shown = true,
            Enabled = true,
            Title = Text.SuggestReplyTitle(i.FollowUp),
            Placeholder = Text.SuggestReplyPlaceholder,
            Progress = Text.SuggestReplyRunning,
            Stop = Assistant.PanelTexts().Stop,
        };
        if (i.State is SuggestReplyState.Running running)
        {
            return running.Case == i.CaseId
                ? v with { Enabled = false, Running = true }
                : v with { Enabled = false, Note = Text.SuggestReplyElsewhere };
        }
        if (!i.ClaudeFound)
        {
            return v with
            {
                Enabled = false,
                Note = chatGpt ? L10n.T("Codex was not found. Choose a native Codex executable.") : Assistant.PanelTexts().NotFound,
            };
        }
        if (i.SignedIn == false)
        {
            return v with { Enabled = false, Note = chatGpt ? L10n.T("Reconnect to ChatGPT") : Assistant.SignInTexts().Hint };
        }
        if (i.State is SuggestReplyState.Failed failed && failed.Case == i.CaseId)
        {
            var note = chatGpt && failed.Failure is SuggestReplyFailure.NotFound or SuggestReplyFailure.NotSignedIn
                ? L10n.T(
                    "The suggested reply failed: %s.",
                    failed.Failure == SuggestReplyFailure.NotFound
                        ? L10n.T("Codex was not found. Choose a native Codex executable.")
                        : L10n.T("Reconnect to ChatGPT"))
                : Text.SuggestReplyFailed(failed.Failure);
            return v with { Note = note, NoteIsFailure = true };
        }
        return v;
    }
}
