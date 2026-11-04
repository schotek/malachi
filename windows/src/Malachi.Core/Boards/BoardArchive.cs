// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardSource.swift (ArchiveOutcome)
// and DaemonBoardSource.swift (undoArchive); GTK: ui/internal/board/source.go
// (ArchiveOutcome, UndoCalls, UndoArchive, whose moves are
// ui/internal/undo's Calls: Undos/Undo.cs).
//
// Archive moves mail on the server, so its toast offers Undo: the messages
// go back to the folders board.archive moved them from (its result's
// moved), then the case back on the board (board.setDone, done false).

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Undos;

namespace Malachi.Core.Boards;

public static partial class Board
{
    /// <summary>What Archive did: the toast's text and what Undo takes back (<see cref="UndoArchive"/>).</summary>
    public sealed record ArchiveOutcome
    {
        /// <summary>The case archived.</summary>
        public required BoardCaseId Case { get; init; }

        /// <summary>Its account.</summary>
        public required AccountId Account { get; init; }

        /// <summary>
        /// Each message moved to the archive and the folder it came from
        /// (<c>board.archive</c>'s moved); empty when only the case was marked
        /// done.
        /// </summary>
        public IReadOnlyList<BoardMoved> Moved { get; init => field = value ?? []; } = [];

        /// <summary><see cref="Board.Text.Archived"/>: what happened, for the toast.</summary>
        public required string Text { get; init; }

        /// <summary>
        /// The toast's button (<see cref="Board.Text.Undo"/>); null when the
        /// daemon moved nothing it can take back (no <c>moved</c>: an
        /// archive folder it does not sync), and the toast is a plain one.
        /// </summary>
        public string? UndoLabel { get; init; }

        /// <summary>
        /// Delete's outcome (<c>board.trash</c>), not Archive's; Undo takes
        /// it back the same way (Go <c>ArchiveOutcome.Trash</c>).
        /// </summary>
        public bool Trash { get; init; }

        /// <summary>Whether both are the same outcome, the moved messages compared in order.</summary>
        public bool Equals(ArchiveOutcome? other) =>
            other is not null && Case == other.Case && Account == other.Account && SameList(Moved, other.Moved)
            && Text == other.Text && UndoLabel == other.UndoLabel && Trash == other.Trash;

        /// <inheritdoc/>
        public override int GetHashCode() => HashCode.Combine(Case, Account, Moved.Count, Text);
    }

    /// <summary>
    /// The daemon calls that take an archive back, in order: every move back
    /// to its folder (<see cref="Moves"/>), then the case back on the board
    /// (<see cref="Reopen"/>).
    /// </summary>
    /// <param name="Moves">One <c>message.move</c> per source folder.</param>
    /// <param name="Reopen">The <c>board.setDone</c> with done false.</param>
    public sealed record UndoCalls(IReadOnlyList<MessageMoveParams> Moves, BoardSetDoneParams Reopen);

    /// <summary>
    /// The calls that take back an archive of case <paramref name="id"/> in
    /// <paramref name="account"/>: <c>message.move</c> of the moved messages
    /// back to the folders they came from (one call per folder, in the order
    /// the folders first appear, messages in their order, each once), then
    /// <c>board.setDone</c> with done false. Without moved messages only the
    /// reopen; an entry without a message or a folder is skipped.
    /// </summary>
    public static UndoCalls UndoArchive(IReadOnlyList<BoardMoved>? moved, AccountId account, BoardCaseId id)
    {
        // The moves are Undo's (undo.Calls over undo.FromBoard), as Go's
        // source.go UndoArchive takes them.
        IReadOnlyList<MessageMoveParams> moves = Undo.FromBoard(false, account, id, moved) is { } e ? Undo.Calls(e) : [];
        return new UndoCalls(moves, new BoardSetDoneParams { CaseId = id, Done = false });
    }
}
