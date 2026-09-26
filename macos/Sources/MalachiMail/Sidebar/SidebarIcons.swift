// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// The GTK icon names the sidebar receives from the core (`roleIcon`, the
/// status pages) mapped to SF Symbols, per the plan's icon table. Private
/// to the sidebar; the app shell's shared `Icons` may replace it.
enum SidebarIcons {
    /// The symbol for a GTK icon name; nil for an empty name (no icon) or
    /// one the sidebar never shows.
    static func symbol(_ gtkName: String) -> String? {
        switch gtkName {
        case "mail-unread-symbolic": return "envelope"
        case "document-edit-symbolic": return "doc"
        case "mail-send-symbolic": return "paperplane"
        case "user-trash-symbolic": return "trash"
        case "mail-mark-junk-symbolic": return "xmark.bin"
        case "folder-download-symbolic": return "archivebox"
        case "folder-symbolic": return "folder"
        case "system-users-symbolic": return "person.2"
        case "dialog-warning-symbolic": return "exclamationmark.triangle"
        case "non-starred-symbolic": return "star"
        case "starred-symbolic": return "star.fill"
        default: return nil
        }
    }

    /// A symbol image at the given point size, nil when the name maps to
    /// nothing.
    static func image(_ gtkName: String, pointSize: CGFloat, weight: NSFont.Weight = .regular) -> NSImage? {
        guard let name = symbol(gtkName) else { return nil }
        return symbolImage(name, pointSize: pointSize, weight: weight)
    }

    /// An SF Symbol image at the given point size.
    static func symbolImage(_ name: String, pointSize: CGFloat, weight: NSFont.Weight = .regular) -> NSImage? {
        let image = NSImage(systemSymbolName: name, accessibilityDescription: nil)
        return image?.withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: pointSize, weight: weight))
    }
}

/// The sidebar's sizes, from window.blp and ui/internal/style/style.go
/// (`.folder-list`, `.folder-row`, `.folder-twisty`, `.folder-star`).
enum SidebarMetrics {
    /// `row.folder-row { min-height: 24px }`.
    static let rowHeight: CGFloat = 24
    /// A pinned folder's row with the account under its name.
    static let subtitleRowHeight: CGFloat = 34
    /// A heading: a row plus the gap above it (folders.go `folderHeadingGap`).
    static let headerRowHeight: CGFloat = 27
    /// folders.go `folderIndent`.
    static let indentPerLevel: CGFloat = 12
    /// `list.folder-list image { -gtk-icon-size: 14px }`: a 12 pt symbol.
    static let iconPointSize: CGFloat = 12
    /// The title box at 88 % of the 13 pt body.
    static let titleFontSize: CGFloat = 11.5
    /// `.caption` for the subtitle and the badge.
    static let captionFontSize: CGFloat = 10
    /// `button.folder-star { min-width: 18px; min-height: 18px }`.
    static let starSize: CGFloat = 18
    /// The horizontal spacing inside a row.
    static let cellSpacing: CGFloat = 6
    /// `transition: opacity 150ms` of the star.
    static let starFade: TimeInterval = 0.15
}
