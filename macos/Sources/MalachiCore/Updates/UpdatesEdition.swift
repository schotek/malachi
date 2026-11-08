// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

extension Updates {
    /// updates.Edition: where this copy came from, as the About window
    /// says under the update line (ui/internal/updates/edition.go).
    public enum Edition: Sendable, Equatable {
        case website, macAppStore, microsoftStore

        /// This copy's: the Mac App Store build from Info.plist
        /// (`Distribution`), else the website's DMG.
        public static func of(_ d: Distribution) -> Edition {
            d == .appStore ? .macAppStore : .website
        }
    }

    /// updates.EditionLine: the About window's line for `e`.
    public static func editionLine(_ e: Edition) -> String {
        switch e {
        case .macAppStore:
            // TRANSLATORS: In the About window: this copy came from Apple's Mac App Store.
            return L10n.T("Version from the Mac App Store")
        case .microsoftStore:
            // TRANSLATORS: In the About window: this copy came from the Microsoft Store.
            return L10n.T("Version from the Microsoft Store")
        case .website:
            // TRANSLATORS: In the About window: this copy came from the website; malachimail.app stays as it is.
            return L10n.T("Version from malachimail.app")
        }
    }
}
