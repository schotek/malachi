// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The fonts of the UI, the AppKit side of libadwaita's style classes
/// (ui/internal/style/style.go and the Blueprints' `styles [...]`), in two
/// sizes (`Settings.TextSize`, macOS only): `standard` keeps the original
/// port, which maps libadwaita's sizes onto a 13 pt body; `larger` raises
/// the small text, closer to what GTK draws (its points are at 96 DPI, so
/// its 11 pt body is ~14.7 px) and to other Mac mail clients. The size is
/// set once at launch (`AppDelegate`), before any window exists, so every
/// view of the run agrees; the setting says it applies after a restart.
@MainActor
enum Typo {
    /// The size of this run; set by the app delegate at launch only.
    static var size: Settings.TextSize = .larger

    private static var larger: Bool { size == .larger }

    /// The body size: 13 pt (libadwaita's body), 14 pt larger.
    static var bodySize: CGFloat { larger ? 14 : 13 }
    /// `.caption`: 10 pt (`.caption1`), 12 pt larger.
    static var captionSize: CGFloat { larger ? 12 : NSFont.preferredFont(forTextStyle: .caption1).pointSize }

    /// The body text of libadwaita.
    static var body: NSFont { .systemFont(ofSize: bodySize) }
    /// `.heading`: bold body.
    static var heading: NSFont { .systemFont(ofSize: bodySize, weight: .bold) }
    /// A sender's name over a conversation card: medium body.
    static var bodyMedium: NSFont { .systemFont(ofSize: bodySize, weight: .medium) }
    /// `.title-2`: the subject of a message (17 pt, 19 pt larger).
    static var title2Bold: NSFont {
        let base = NSFont.preferredFont(forTextStyle: .title2).pointSize
        return .systemFont(ofSize: larger ? base + 2 : base, weight: .bold)
    }
    /// `.title-1`: the title of a status page.
    static var title1: NSFont { .systemFont(ofSize: larger ? 22 : 20, weight: .bold) }
    /// `.caption`.
    static var caption: NSFont { .systemFont(ofSize: captionSize) }
    /// The caption in monospaced digits (unread badges of the sidebar).
    static var captionNumeric: NSFont { .monospacedDigitSystemFont(ofSize: captionSize, weight: .regular) }
    /// The name in an attachment chip (90 %).
    static var chip: NSFont { .systemFont(ofSize: larger ? 13 : 11.5) }
    /// Small secondary text of the assistant panel (notes, the pending line).
    static var small: CGFloat { larger ? 12 : 11 }

    /// The plain-text body of a message at the text-zoom setting, in the
    /// monospaced face when the user asked for it (`.message-body`).
    static func body(zoom: Int, monospace: Bool) -> NSFont {
        let size = bodySize * CGFloat(zoom) / 100
        return monospace ? .monospacedSystemFont(ofSize: size, weight: .regular) : .systemFont(ofSize: size)
    }

    // MARK: Sidebar

    /// The folder name, at 88 % of the body.
    static var sidebarTitleSize: CGFloat { larger ? 13 : 11.5 }
    /// The account under a pinned folder and the unread count.
    static var sidebarCaptionSize: CGFloat { larger ? 11 : 10 }
    /// An account's heading.
    static var sidebarHeading: NSFont { .systemFont(ofSize: larger ? 12 : 11, weight: .bold) }
    /// The kind capsule after an account's name (JIRA, IMAP, …).
    static var sidebarKindBadge: NSFont { .systemFont(ofSize: larger ? 10 : 9, weight: .semibold) }
    /// The status page of the sidebar: its title and its details.
    static var sidebarStatusTitle: NSFont { .systemFont(ofSize: larger ? 16 : 15, weight: .bold) }
    static var sidebarStatusDetails: NSFont { .systemFont(ofSize: larger ? 13 : 12) }
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
