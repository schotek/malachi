// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import os

/// The symbol sizes of the UI. GTK symbolic icons are 16 px; the sidebar's
/// are 14 px (ui/internal/style). SF Symbols are sized in points of their
/// font, which comes out slightly larger than the point size, hence the
/// mapping below.
enum IconSize: Sendable {
    /// 12 pt, about 14 px: sidebar rows, chips, the star of a list row.
    case small
    /// 14 pt, about 16 px: buttons, banners, the footer.
    case regular
    /// The toolbar's own size (no configuration).
    case toolbar
    /// 96 pt: the illustration of a status page.
    case status

    var pointSize: CGFloat? {
        switch self {
        case .small: return 12
        case .regular: return 14
        case .toolbar: return nil
        case .status: return 96
        }
    }
}

/// SF Symbols for the GTK icon names of the Blueprints and the Go UI, so
/// that the code keeps one key per GTK icon (`roleIcon` of the model hands
/// out GTK names). The table is the one of the port plan.
@MainActor
enum Icon {
    /// GTK icon name (with or without `-symbolic`) → SF Symbol name.
    static let symbols: [String: String] = [
        "mail-message-new": "square.and.pencil",
        "open-menu": "line.3.horizontal",
        "view-refresh": "arrow.clockwise",
        "edit-find": "magnifyingglass",
        "mail-reply-sender": "arrowshape.turn.up.left",
        "mail-reply-all": "arrowshape.turn.up.left.2",
        "mail-forward": "arrowshape.turn.up.right",
        "user-trash": "trash",
        "mail-mark-junk": "xmark.bin",
        "folder-download": "archivebox",
        "non-starred": "star",
        "starred": "star.fill",
        "view-more": "ellipsis.circle",
        "mail-unread": "envelope",
        "mail-read": "envelope.open",
        "system-users": "person.2",
        "folder": "folder",
        "document-edit": "doc",
        "mail-send": "paperplane",
        "mail-attachment": "paperclip",
        "pan-end": "chevron.right",
        "go-next": "chevron.right",
        "pan-down": "chevron.down",
        "network-offline": "network.slash",
        "network-idle": "arrow.triangle.2.circlepath",
        "network-transmit-receive": "network",
        "dialog-warning": "exclamationmark.triangle",
        "dialog-error": "xmark.octagon",
        "dialog-question": "questionmark.circle",
        "emblem-ok": "checkmark.circle",
        "list-add": "plus",
        "list-drag-handle": "line.3.horizontal",
        "document-save": "square.and.arrow.down",
        "window-close": "xmark",
        "format-text-bold": "bold",
        "format-text-italic": "italic",
        "format-text-underline": "underline",
        "format-justify-left": "text.alignleft",
        "format-justify-center": "text.aligncenter",
        "format-justify-right": "text.alignright",
        "view-list-bullet": "list.bullet",
        "view-list-ordered": "list.number",
        "format-indent-more": "text.quote",
        "insert-link": "link",
        "insert-image": "photo",
        "edit-clear": "eraser",
        "x-office-address-book": "person.text.rectangle",
        "document-open-recent": "clock.arrow.circlepath",
        "image-x-generic": "photo",
        "goa-account": "envelope",
        "avatar-default": "person.crop.circle.fill",
        "sidebar-show": "sidebar.leading",
        "preferences-system": "gearshape",
        "applications-graphics": "paintpalette",
    ]

    private static let log = Logger(subsystem: "io.github.schotek.Malachi", category: "icons")

    /// The SF Symbol name for a GTK icon name; `questionmark.circle` for
    /// one the table does not know (logged once per name).
    static func symbolName(_ gtkName: String) -> String {
        var key = gtkName
        if key.hasSuffix("-symbolic") {
            key = String(key.dropLast("-symbolic".count))
        }
        if let s = symbols[key] {
            return s
        }
        // goa-account-google, goa-account-msn, … all map to the envelope.
        if key.hasPrefix("goa-account") {
            return symbols["goa-account"] ?? "envelope"
        }
        log.info("no symbol for GTK icon \(gtkName, privacy: .public)")
        return "questionmark.circle"
    }

    /// The image for a GTK icon name at `size`. The accessibility
    /// description is the GTK name; callers set a better one where it
    /// matters. Template images take the tint of their view.
    static func image(_ gtkName: String, size: IconSize = .regular) -> NSImage {
        symbol(symbolName(gtkName), size: size, description: gtkName)
    }

    /// The image for an SF Symbol name at `size`.
    static func symbol(_ name: String, size: IconSize = .regular, description: String? = nil) -> NSImage {
        let base = NSImage(systemSymbolName: name, accessibilityDescription: description ?? name)
            ?? NSImage(systemSymbolName: "questionmark.circle", accessibilityDescription: description ?? name)
            ?? NSImage()
        guard let pt = size.pointSize else {
            return base
        }
        let config = NSImage.SymbolConfiguration(pointSize: pt, weight: .regular)
        return base.withSymbolConfiguration(config) ?? base
    }

    // Named accessors of the shell.

    static var newMessage: NSImage { image("mail-message-new", size: .toolbar) }
    static var refresh: NSImage { image("view-refresh", size: .toolbar) }
    static var reply: NSImage { image("mail-reply-sender", size: .toolbar) }
    static var replyAll: NSImage { image("mail-reply-all", size: .toolbar) }
    static var forward: NSImage { image("mail-forward", size: .toolbar) }
    static var trash: NSImage { image("user-trash", size: .toolbar) }
    static var junk: NSImage { image("mail-mark-junk", size: .toolbar) }
    static var archive: NSImage { image("folder-download", size: .toolbar) }
    static var star: NSImage { image("non-starred", size: .toolbar) }
    static var starFilled: NSImage { image("starred", size: .toolbar) }
    static var moreActions: NSImage { image("view-more", size: .toolbar) }
}
