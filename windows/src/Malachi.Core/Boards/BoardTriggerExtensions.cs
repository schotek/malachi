// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the computed property TriageTrigger.wire of
// macos/Sources/MalachiCore/Board/BoardAutoTriage.swift; Go:
// ui/internal/boardtriage/triage.go (Wire). C# enums carry no members;
// extension members live in a top-level class.

using Malachi.Core.Api;

namespace Malachi.Core.Boards;

/// <summary>The wire value of <see cref="Board.TriageTrigger"/>.</summary>
public static class BoardTriggerExtensions
{
    extension(Board.TriageTrigger t)
    {
        /// <summary><c>board.runStart</c>'s trigger.</summary>
        public BoardTrigger Wire => t == Board.TriageTrigger.Manual ? BoardTrigger.Manual : BoardTrigger.Auto;
    }
}
