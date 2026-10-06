// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/board/columns_key.go (SidewaysTarget); macOS keeps the
// rule inline in macos/Sources/MalachiMail/Board/BoardColumnsViewController.swift
// (sideways). Where Left or Right leads in the Columns style.

using System;
using System.Collections.Generic;

namespace Malachi.Core.Boards;

public static partial class Board
{
    /// <summary>
    /// Where Left (<paramref name="delta"/> -1) or Right (+1) leads in the
    /// Columns style: the neighbouring column's selectable row at the same
    /// position as the current one, the last one when that column is shorter;
    /// an empty column is passed over. <paramref name="selectable"/> holds each
    /// column's count of selectable rows (cards and commitments, not headings
    /// or placeholders), <paramref name="from"/> is the current column and
    /// <paramref name="position"/> the current row among its selectable rows
    /// (a negative position, nothing selected, counts as the first). False
    /// when nothing moves (the edge, or only empty columns beyond it); the key
    /// is consumed all the same.
    /// </summary>
    public static bool SidewaysTarget(IReadOnlyList<int> selectable, int from, int position, int delta, out int column, out int row)
    {
        column = 0;
        row = 0;
        if (selectable is null || (delta != -1 && delta != 1) || from < 0 || from >= selectable.Count)
        {
            return false;
        }
        position = Math.Max(position, 0);
        for (var i = from + delta; i >= 0 && i < selectable.Count; i += delta)
        {
            var n = selectable[i];
            if (n > 0)
            {
                column = i;
                row = Math.Min(position, n - 1);
                return true;
            }
        }
        return false;
    }
}
