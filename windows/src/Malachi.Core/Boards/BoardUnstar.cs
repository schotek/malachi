// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Board/BoardUnstar.swift; GTK:
// ui/internal/board/unstar.go (CanUnstar).
//
// When a case offers Unstar, for the places that have a case rather than
// its detail (the context menu of a row or a card).

using System;
using Malachi.Core.Api;

namespace Malachi.Core.Boards;

public static partial class Board
{
    /// <summary>
    /// Unstar is offered: the case is on the board because of a star (rule
    /// reason <c>hot.flagged</c>, whatever state the user gave it) and is not
    /// done. <see cref="Detail.CanUnstar"/> says the same for the selected case.
    /// </summary>
    public static bool CanUnstar(Case c)
    {
        ArgumentNullException.ThrowIfNull(c);
        return c.RuleReason.Value == BoardReason.HotFlagged && !c.Visibility.IsDone;
    }
}
