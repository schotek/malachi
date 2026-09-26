// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// The fonts of the UI, the AppKit side of libadwaita's style classes
/// (ui/internal/style/style.go and the Blueprints' `styles [...]`).
@MainActor
enum Typo {
    /// The body text of libadwaita (13 pt).
    static var body: NSFont { .systemFont(ofSize: 13) }
    /// `.heading`: bold body.
    static var heading: NSFont { .systemFont(ofSize: 13, weight: .bold) }
    /// `.title-2`: the subject of a message.
    static var title2Bold: NSFont {
        .systemFont(ofSize: NSFont.preferredFont(forTextStyle: .title2).pointSize, weight: .bold)
    }
    /// `.title-1`: the title of a status page.
    static var title1: NSFont { .systemFont(ofSize: 20, weight: .bold) }
    /// `.caption` (10 pt).
    static var caption: NSFont { .preferredFont(forTextStyle: .caption1) }
    /// The caption in monospaced digits (unread badges of the sidebar).
    static var captionNumeric: NSFont {
        .monospacedDigitSystemFont(ofSize: NSFont.preferredFont(forTextStyle: .caption1).pointSize, weight: .regular)
    }
    /// The name in an attachment chip (90 %).
    static var chip: NSFont { .systemFont(ofSize: 11.5) }

    /// The plain-text body of a message at the text-zoom setting, in the
    /// monospaced face when the user asked for it (`.message-body`).
    static func body(zoom: Int, monospace: Bool) -> NSFont {
        let size = 13 * CGFloat(zoom) / 100
        return monospace ? .monospacedSystemFont(ofSize: size, weight: .regular) : .systemFont(ofSize: size)
    }
}

/// The colours of the UI, the AppKit side of libadwaita's named colours.
@MainActor
enum Tint {
    /// `dim-label`.
    static var secondary: NSColor { .secondaryLabelColor }
    /// `accent`.
    static var accent: NSColor { .controlAccentColor }
    /// `alpha(@window_fg_color, x)`.
    static func fg(alpha: CGFloat) -> NSColor { NSColor.labelColor.withAlphaComponent(alpha) }
    /// `card`, `boxed-list`, `view`.
    static var cardFill: NSColor { .controlBackgroundColor }
    static var cardBorder: NSColor { .separatorColor }
}
