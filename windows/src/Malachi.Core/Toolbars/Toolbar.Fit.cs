// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/toolbar/fit.go (FitResult, Fit): which entries of a
// strip show in the room it has (T5). The client measures, Fit decides.

using System;
using System.Collections.Generic;

namespace Malachi.Core.Toolbars;

public static partial class Toolbar
{
    /// <summary>
    /// toolbar.Fit: which of <paramref name="shown"/> (a run of
    /// <see cref="PresentFor"/> that one strip draws) fit into
    /// <paramref name="available"/>, <paramref name="widths"/>[i] being the
    /// width entry i takes with its gap (a flexible space its minimum). While
    /// the total is too wide, the item with the lowest visibility priority
    /// goes, of equal ones the rightmost; separators, spaces, fixed items and
    /// items of <see cref="PriorityUser"/> and above never go, nor does an
    /// item of no width. Missing or negative widths count as 0; an entry
    /// hidden by Present takes no room and is not in the overflow.
    /// </summary>
    public static FitResult Fit(ToolbarVariant v, Bar bar, IReadOnlyList<Shown> shown, IReadOnlyList<double> widths, double available)
    {
        ArgumentNullException.ThrowIfNull(v);
        ArgumentNullException.ThrowIfNull(shown);
        ArgumentNullException.ThrowIfNull(widths);
        var n = shown.Count;
        var visible = new bool[n];
        double Width(int i) => i < widths.Count && widths[i] > 0 ? widths[i] : 0;
        double total = 0;
        for (var i = 0; i < n; i++)
        {
            if (shown[i].Hidden)
            {
                continue;
            }
            visible[i] = true;
            total += Width(i);
        }
        var gone = new bool[n];
        while (total > available)
        {
            var pick = -1;
            var best = 0;
            for (var i = n - 1; i >= 0; i--)
            {
                if (!visible[i] || Width(i) == 0 || Priority(v, bar, shown[i].Id) is not { } p)
                {
                    continue;
                }
                if (pick < 0 || p < best)
                {
                    pick = i;
                    best = p;
                }
            }
            if (pick < 0)
            {
                break;
            }
            visible[pick] = false;
            gone[pick] = true;
            total -= Width(pick);
        }
        var overflow = new List<string>();
        for (var i = 0; i < n; i++)
        {
            if (gone[i])
            {
                overflow.Add(shown[i].Id);
            }
        }
        return new FitResult(visible, overflow);
    }

    // The priority of an entry that may give way, null for one that never
    // does; an unknown id is standard.
    private static int? Priority(ToolbarVariant v, Bar bar, string id)
    {
        if (IsSpace(id) || IsSeparator(id))
        {
            return null;
        }
        if (LookupFor(v, bar, id) is not { } it)
        {
            return PriorityStandard;
        }
        if (it.Fixed || it.Priority >= PriorityUser)
        {
            return null;
        }
        return it.Priority;
    }

    /// <summary>toolbar.FitResult: per entry whether it shows, and the items that did not fit in the layout's order.</summary>
    public sealed record FitResult(IReadOnlyList<bool> Visible, IReadOnlyList<string> Overflow);
}
