// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/PreferenceChoices.swift; GTK:
// ui/internal/window/preferences.go (the choice tables, nearestInterval,
// indexOfRetention, indexOfPolicy).

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Settings;

namespace Malachi.Core.Model;

/// <summary>
/// The choice tables of the preferences, in the order of their pop-ups, and
/// the mapping of a stored value onto the nearest position.
/// </summary>
public static class PreferenceChoices
{
    /// <summary>Colour scheme entries, in pop-up order.</summary>
    public static IReadOnlyList<ColorScheme> ColorSchemeChoices { get; } = [ColorScheme.System, ColorScheme.Light, ColorScheme.Dark];

    /// <summary>Density entries, in pop-up order.</summary>
    public static IReadOnlyList<Density> DensityChoices { get; } = [Density.Comfortable, Density.Compact];

    /// <summary>Check for New Mail: Manually, 5, 15, 30 minutes, in seconds.</summary>
    public static IReadOnlyList<int> IntervalChoices { get; } = [0, 300, 900, 1800];

    /// <summary>Load Remote Images, in pop-up order.</summary>
    public static IReadOnlyList<RemoteContentPolicy> RemoteChoices { get; } =
        [RemoteContentPolicy.Block, RemoteContentPolicy.KnownSenders, RemoteContentPolicy.Allow];

    /// <summary>
    /// Keep Mail Offline: <c>Preferences.offlineDays</c> per row: 1 week,
    /// 1 month, 3 months, 1 year, Everything (0).
    /// </summary>
    public static IReadOnlyList<int> RetentionChoices { get; } = [7, 30, 90, 365, 0];

    /// <summary>
    /// Maps a sync interval in seconds to the closest pop-up position (0
    /// stays "Manually"; preferences.go <c>nearestInterval</c>).
    /// </summary>
    public static int NearestInterval(int seconds)
    {
        if (seconds <= 0)
        {
            return 0;
        }
        var best = 1;
        var bestDiff = -1L;
        for (var i = 1; i < IntervalChoices.Count; i++)
        {
            var diff = Math.Abs((long)IntervalChoices[i] - seconds);
            if (bestDiff < 0 || diff < bestDiff)
            {
                best = i;
                bestDiff = diff;
            }
        }
        return best;
    }

    /// <summary>
    /// Maps <c>Preferences.offlineDays</c> to the closest pop-up position; 0
    /// (keep everything) and invalid negative values select "Everything"
    /// (preferences.go <c>indexOfRetention</c>).
    /// </summary>
    public static int IndexOfRetention(int days)
    {
        if (days <= 0)
        {
            return RetentionChoices.Count - 1;
        }
        var best = 0;
        var bestDiff = -1L;
        for (var i = 0; i < RetentionChoices.Count; i++)
        {
            var v = RetentionChoices[i];
            if (v == 0)
            {
                continue;
            }
            var diff = Math.Abs((long)v - days);
            if (bestDiff < 0 || diff < bestDiff)
            {
                best = i;
                bestDiff = diff;
            }
        }
        return best;
    }

    /// <summary>
    /// The pop-up position of a remote-content policy, 0 for an unknown one
    /// (preferences.go <c>indexOfPolicy</c>).
    /// </summary>
    public static int IndexOfPolicy(RemoteContentPolicy p)
    {
        for (var i = 0; i < RemoteChoices.Count; i++)
        {
            if (RemoteChoices[i] == p)
            {
                return i;
            }
        }
        return 0;
    }
}
