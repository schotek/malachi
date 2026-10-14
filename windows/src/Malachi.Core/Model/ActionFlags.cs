// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/ActionRules.swift (ActionFlags);
// GTK: ui/internal/window/actions.go (setMessageActionsSensitive),
// action_rules.go (actionState) and ui/internal/selection (Flags: Count,
// Open).

namespace Malachi.Core.Model;

/// <summary>
/// What the per-message header buttons and actions allow for the selected
/// row, or the selected rows combined (actions.go
/// <c>setMessageActionsSensitive</c>, <c>Selection.Combine</c>). The default
/// value is everything off (nothing selected, <see cref="None"/>).
/// </summary>
public readonly record struct ActionFlags
{
    /// <summary>Everything off (nothing selected).</summary>
    public static ActionFlags None => default;

    /// <summary>
    /// How many rows are selected: 1 for a row's own flags, two or more for
    /// several (<c>Selection.Combine</c>), 0 with nothing selected.
    /// </summary>
    public int Count { get; init; }

    /// <summary>
    /// Open in New Window can open them: one row always, several up to
    /// <c>Selection.MaxOpen</c>.
    /// </summary>
    public bool Open { get; init; }

    /// <summary>Something is selected and the actions are on at all.</summary>
    public bool On { get; init; }

    /// <summary>
    /// The row is a queued outgoing message: the trash button cancels the
    /// send (<see cref="Outbox.TrashTooltip"/>), and flags and moves are
    /// refused.
    /// </summary>
    public bool Outbox { get; init; }

    /// <summary>
    /// The state the star button shows: a conversation row is flagged when
    /// any member is.
    /// </summary>
    public bool Flagged { get; init; }

    /// <summary>Reply.</summary>
    public bool Reply { get; init; }

    /// <summary>Reply All.</summary>
    public bool ReplyAll { get; init; }

    /// <summary>Forward.</summary>
    public bool Forward { get; init; }

    /// <summary>The star button.</summary>
    public bool Star { get; init; }

    /// <summary>Move to Trash (Cancel Sending for an outbox message).</summary>
    public bool Trash { get; init; }

    /// <summary>Archive.</summary>
    public bool Archive { get; init; }

    /// <summary>Junk.</summary>
    public bool Junk { get; init; }

    /// <summary>
    /// The row can go to another folder of its account (Move To in its
    /// context menu; action_rules.go <c>actionState.move</c>).
    /// </summary>
    public bool Move { get; init; }

    /// <summary>Mark as Read.</summary>
    public bool MarkRead { get; init; }

    /// <summary>Mark as Unread.</summary>
    public bool MarkUnread { get; init; }

    /// <summary>Toggle the flag.</summary>
    public bool ToggleFlag { get; init; }

    /// <summary>Load Images.</summary>
    public bool LoadImages { get; init; }

    /// <summary>Always From This Sender.</summary>
    public bool TrustSender { get; init; }

    /// <summary>
    /// Reply writes a comment on the issue (the account has the comment
    /// capability): the client labels it "Comment" (Jira.ReplyLabel).
    /// </summary>
    public bool Comment { get; init; }

    /// <summary>
    /// The row's account changes the status of the row's issue (GTK's
    /// actionState.changeStatus: Jira.CanTransition, a message of an issue).
    /// </summary>
    public bool ChangeStatus { get; init; }

    /// <summary>
    /// The actions the account does not offer at all, whatever is selected
    /// (Capabilities.Supported); with nothing selected, those of the listed
    /// folder's account. None for a mail account.
    /// </summary>
    public MessageActionKind Unsupported { get; init; }
}
