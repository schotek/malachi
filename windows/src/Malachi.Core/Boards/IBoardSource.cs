// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardSource.swift (BoardSource and
// its extension); GTK: ui/internal/board/source.go (Handlers, DataSource,
// ArchiveUndoer).
//
// Where the board's cases come from. The board reads a snapshot and is told
// when it changes; what the user decides about a case (its state, done, a
// remind, archive, a discarded draft, a promise ticked off) is case data and
// goes back to the source. The daemon's source (DaemonBoardSource) starts
// empty and publishes when its data arrives; the in-memory one holds
// invented sample cases or none, for the dummy board and the tests. Swift's
// single-observer closures are Action properties, as Go's Handlers; the
// async discard is a Task that faults when the daemon refused. Every member
// runs on the UI thread.

using System;
using System.Threading.Tasks;
using Malachi.Core.Api;

namespace Malachi.Core.Boards;

/// <summary>
/// The board's cases and the writes the user's decisions make. Writes are
/// fire-and-forget: the source calls <see cref="OnChange"/> once its snapshot
/// holds them (at once for both sources: the daemon's writes optimistically
/// and undoes a write the daemon refused, with <see cref="OnError"/>).
/// </summary>
public interface IBoardSource
{
    /// <summary>What the source knows now. The caller does not change it.</summary>
    Board.Snapshot Snapshot { get; }

    /// <summary>How far the data is (<see cref="Board.Snapshot.Phase"/>).</summary>
    Board.Phase Phase => Snapshot.Phase;

    /// <summary>Called after <see cref="Snapshot"/> changed. One observer: the board controller.</summary>
    Action? OnChange { get; set; }

    /// <summary>
    /// Called with a short sentence for a toast when a write or a load failed
    /// (a write is undone by then). One observer: the controller.
    /// </summary>
    Action<string>? OnError { get; set; }

    /// <summary>
    /// Called with a short sentence for a toast about what a write did. One
    /// observer: the controller.
    /// </summary>
    Action<string>? OnNotice { get; set; }

    /// <summary>
    /// Called with what <see cref="Archive"/> did, for a toast with Undo
    /// (Go <c>Handlers.Archived</c>). One observer: the controller.
    /// </summary>
    Action<Board.ArchiveOutcome>? OnArchived { get; set; }

    /// <summary>Moves the case to <paramref name="state"/>; null = back to automatic (the assistant's or the rules' state).</summary>
    void SetState(Board.State? state, BoardCaseId id);

    /// <summary>Done takes the case off the board (and ends a remind); not done puts it back.</summary>
    void SetDone(bool done, BoardCaseId id);

    /// <summary>
    /// Hides the case until <paramref name="until"/> (in the future, within a
    /// year); null puts a snoozed case back on the board. Ends done.
    /// </summary>
    void Remind(DateTimeOffset? until, BoardCaseId id);

    /// <summary>
    /// Moves the case's inbox messages to the archive (where the account can)
    /// and marks it done; <see cref="OnArchived"/> says what it did, with the
    /// moved messages for Undo.
    /// </summary>
    void Archive(BoardCaseId id);

    /// <summary>Ticks a promise off (or reopens it).</summary>
    void SetCommitmentDone(bool done, BoardCommitmentId id);

    /// <summary>Drops the suggested reply (the draft itself, too).</summary>
    void DiscardDraft(BoardCaseId id);

    /// <summary>
    /// Discard of the inline reply editor: deletes draft
    /// <paramref name="draft"/> of case <paramref name="id"/> and completes
    /// when that is done. While the case links it, the link goes with it
    /// (<c>board.discardDraft</c>); a draft the case no longer links is
    /// deleted alone. Faults when the daemon refused (the editor says so and
    /// keeps its text; no <see cref="OnError"/>); a source without drafts
    /// (the samples) just drops the link.
    /// </summary>
    Task DiscardDraftAsync(DraftId draft, AccountId account, BoardCaseId id)
    {
        DiscardDraft(id);
        return Task.CompletedTask;
    }

    /// <summary>
    /// Removes the star from the messages that keep the case hot
    /// (<see cref="Board.Detail.CanUnstar"/>); the rules then decide
    /// where the case goes.
    /// </summary>
    void Unflag(BoardCaseId id);

    /// <summary>
    /// Loads the case's conversation into <see cref="Board.Case.Messages"/>
    /// unless it is there for the case's current version already.
    /// </summary>
    void LoadMessages(BoardCaseId id);

    /// <summary>Asks for the data anew.</summary>
    void Refresh();
}
