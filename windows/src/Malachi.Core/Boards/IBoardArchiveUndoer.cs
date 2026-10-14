// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardSource.swift (BoardArchiveUndoer);
// GTK: ui/internal/board/source.go
// (ArchiveUndoer).

namespace Malachi.Core.Boards;

/// <summary>
/// A source that can take an archive back with the daemon's calls
/// (<see cref="Board.UndoArchive"/>); <c>BoardController.UndoArchive</c>
/// uses it when the source is one (Go <c>ArchiveUndoer</c>).
/// </summary>
public interface IBoardArchiveUndoer
{
    /// <summary>Moves the messages of <paramref name="outcome"/> back to their folders, then puts the case back on the board.</summary>
    void UndoArchive(Board.ArchiveOutcome outcome);
}
