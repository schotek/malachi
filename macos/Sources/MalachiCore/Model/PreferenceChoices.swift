// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The choice tables of the preferences (ui/internal/window/preferences.go),
// in the order of the pop-ups, and the mapping of a stored value onto the
// nearest position.

/// Colour scheme entries, in pop-up order.
public let colorSchemeChoices: [Settings.ColorScheme] = [.system, .light, .dark]

/// Density entries, in pop-up order.
public let densityChoices: [Settings.Density] = [.comfortable, .compact]

/// Check for New Mail: Manually, 5, 15, 30 minutes, in seconds.
public let intervalChoices: [Int] = [0, 300, 900, 1800]

/// Load Remote Images, in pop-up order.
public let remoteChoices: [RemoteContentPolicy] = [.block, .knownSenders, .allow]

/// Keep Mail Offline: `Preferences.offlineDays` per row: 1 week, 1 month,
/// 3 months, 1 year, Everything (0).
public let retentionChoices: [Int] = [7, 30, 90, 365, 0]

/// Maps a sync interval in seconds to the closest pop-up position (0 stays
/// "Manually"; preferences.go `nearestInterval`).
public func nearestInterval(_ seconds: Int) -> Int {
    if seconds <= 0 {
        return 0
    }
    var best = 1
    var bestDiff = -1
    for (i, v) in intervalChoices.dropFirst().enumerated() {
        let diff = abs(v - seconds)
        if bestDiff < 0 || diff < bestDiff {
            best = i + 1
            bestDiff = diff
        }
    }
    return best
}

/// Maps `Preferences.offlineDays` to the closest pop-up position; 0 (keep
/// everything) and invalid negative values select "Everything"
/// (preferences.go `indexOfRetention`).
public func indexOfRetention(_ days: Int) -> Int {
    if days <= 0 {
        return retentionChoices.count - 1
    }
    var best = 0
    var bestDiff = -1
    for (i, v) in retentionChoices.enumerated() where v != 0 {
        let diff = abs(v - days)
        if bestDiff < 0 || diff < bestDiff {
            best = i
            bestDiff = diff
        }
    }
    return best
}

/// The pop-up position of a remote-content policy, 0 for an unknown one
/// (preferences.go `indexOfPolicy`).
public func indexOfPolicy(_ p: RemoteContentPolicy) -> Int {
    remoteChoices.firstIndex(of: p) ?? 0
}
