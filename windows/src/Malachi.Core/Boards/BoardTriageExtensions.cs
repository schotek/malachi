// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the computed property TriageFailure.runError of
// macos/Sources/MalachiCore/Board/BoardTriage.swift; Go:
// ui/internal/boardtriage/triage.go (RunError). C# enums carry no members;
// extension members live in a top-level class.

using Malachi.Core.Api;

namespace Malachi.Core.Boards;

/// <summary>The wire value of <see cref="Board.TriageFailure"/>.</summary>
public static class BoardTriageExtensions
{
    extension(Board.TriageFailure f)
    {
        /// <summary>The class <c>board.runEnd</c> records for a run that failed with it.</summary>
        public BoardRunError RunError => f switch
        {
            Board.TriageFailure.Cancelled => BoardRunError.Cancelled,
            Board.TriageFailure.Timeout => BoardRunError.Timeout,
            Board.TriageFailure.NotSignedIn => BoardRunError.SignedOut,
            _ => BoardRunError.Failed,
        };
    }
}
