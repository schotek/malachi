// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// Applies the colour-scheme setting to the application (ui/internal/style
/// `Apply`, the libadwaita part): follow the system, or force light or
/// dark. Every window follows `NSApp.appearance`.
@MainActor
final class AppearanceController {
    private let settings: Settings
    private var token: Settings.ChangeToken?

    init(settings: Settings) {
        self.settings = settings
        apply()
        token = settings.onChange(.colorScheme) { [weak self] in
            self?.apply()
        }
    }

    func apply() {
        NSApp.appearance = Self.appearance(for: settings.colorScheme)
    }

    static func appearance(for scheme: Settings.ColorScheme) -> NSAppearance? {
        switch scheme {
        case .system: return nil
        case .light: return NSAppearance(named: .aqua)
        case .dark: return NSAppearance(named: .darkAqua)
        }
    }
}
