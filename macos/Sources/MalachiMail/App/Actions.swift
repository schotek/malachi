// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The application's own actions, as selectors on the responder chain. The
/// menu bar and the toolbars send them with a nil target; whichever
/// responder of the key window implements one handles it, and the same
/// responder answers `validateUserInterfaceItem` for it. The window
/// controllers, the compose window and the application delegate each
/// implement the subset they own; nothing has to implement all of them.
///
/// Names follow the GTK actions (`app.compose` → `newMessage:`, `win.trash`
/// → `moveToTrash:`, …). Standard AppKit selectors (`performClose:`,
/// `toggleSidebar:`, `hide:`, `terminate:`, the Edit menu) are not repeated
/// here. `openHelp:` is deliberately not `showHelp:`, which NSApplication
/// implements itself (it would open a non-existent help book first).
@MainActor
@objc protocol MalachiActions {
    // Application (ui/main.go `addActions`).
    @objc optional func newMessage(_ sender: Any?)
    @objc optional func addAccount(_ sender: Any?)
    @objc optional func showPreferences(_ sender: Any?)
    @objc optional func showAbout(_ sender: Any?)
    @objc optional func openHelp(_ sender: Any?)

    // Main window (window/actions.go `registerActions`).
    @objc optional func checkForNewMail(_ sender: Any?)
    /// The list's filter; the sender's tag is the filter (FilterTag).
    @objc optional func setMessageFilter(_ sender: Any?)
    @objc optional func toggleMessageList(_ sender: Any?)
    @objc optional func reply(_ sender: Any?)
    @objc optional func replyAll(_ sender: Any?)
    @objc optional func forward(_ sender: Any?)
    @objc optional func markAsRead(_ sender: Any?)
    @objc optional func markAsUnread(_ sender: Any?)
    @objc optional func toggleFlag(_ sender: Any?)
    @objc optional func archive(_ sender: Any?)
    @objc optional func markAsJunk(_ sender: Any?)
    @objc optional func moveToTrash(_ sender: Any?)
    @objc optional func loadImages(_ sender: Any?)
    @objc optional func trustSender(_ sender: Any?)

    // Compose window (compose.blp).
    @objc optional func sendMessage(_ sender: Any?)
    @objc optional func saveDraft(_ sender: Any?)
    @objc optional func attachFiles(_ sender: Any?)
    @objc optional func insertImage(_ sender: Any?)
    @objc optional func discardDraft(_ sender: Any?)

    // Format menu (compose.blp `format_toolbar`).
    @objc optional func formatBold(_ sender: Any?)
    @objc optional func formatItalic(_ sender: Any?)
    @objc optional func formatUnderline(_ sender: Any?)
    @objc optional func formatParagraph(_ sender: Any?)
    @objc optional func formatHeading1(_ sender: Any?)
    @objc optional func formatHeading2(_ sender: Any?)
    @objc optional func formatHeading3(_ sender: Any?)
    @objc optional func alignLeft(_ sender: Any?)
    @objc optional func alignCenter(_ sender: Any?)
    @objc optional func alignRight(_ sender: Any?)
    @objc optional func bulletedList(_ sender: Any?)
    @objc optional func numberedList(_ sender: Any?)
    @objc optional func quoteBlock(_ sender: Any?)
    @objc optional func insertLink(_ sender: Any?)
    @objc optional func clearFormatting(_ sender: Any?)
}

/// The selectors of `MalachiActions`, for menu items, toolbar items and
/// `validateUserInterfaceItem` switches.
enum Action {
    static let newMessage = #selector(MalachiActions.newMessage(_:))
    static let setMessageFilter = #selector(MalachiActions.setMessageFilter(_:))
    static let addAccount = #selector(MalachiActions.addAccount(_:))
    static let showPreferences = #selector(MalachiActions.showPreferences(_:))
    static let showAbout = #selector(MalachiActions.showAbout(_:))
    static let openHelp = #selector(MalachiActions.openHelp(_:))

    static let checkForNewMail = #selector(MalachiActions.checkForNewMail(_:))
    static let toggleMessageList = #selector(MalachiActions.toggleMessageList(_:))
    static let reply = #selector(MalachiActions.reply(_:))
    static let replyAll = #selector(MalachiActions.replyAll(_:))
    static let forward = #selector(MalachiActions.forward(_:))
    static let markAsRead = #selector(MalachiActions.markAsRead(_:))
    static let markAsUnread = #selector(MalachiActions.markAsUnread(_:))
    static let toggleFlag = #selector(MalachiActions.toggleFlag(_:))
    static let archive = #selector(MalachiActions.archive(_:))
    static let markAsJunk = #selector(MalachiActions.markAsJunk(_:))
    static let moveToTrash = #selector(MalachiActions.moveToTrash(_:))
    static let loadImages = #selector(MalachiActions.loadImages(_:))
    static let trustSender = #selector(MalachiActions.trustSender(_:))

    static let sendMessage = #selector(MalachiActions.sendMessage(_:))
    static let saveDraft = #selector(MalachiActions.saveDraft(_:))
    static let attachFiles = #selector(MalachiActions.attachFiles(_:))
    static let insertImage = #selector(MalachiActions.insertImage(_:))
    static let discardDraft = #selector(MalachiActions.discardDraft(_:))

    static let formatBold = #selector(MalachiActions.formatBold(_:))
    static let formatItalic = #selector(MalachiActions.formatItalic(_:))
    static let formatUnderline = #selector(MalachiActions.formatUnderline(_:))
    static let formatParagraph = #selector(MalachiActions.formatParagraph(_:))
    static let formatHeading1 = #selector(MalachiActions.formatHeading1(_:))
    static let formatHeading2 = #selector(MalachiActions.formatHeading2(_:))
    static let formatHeading3 = #selector(MalachiActions.formatHeading3(_:))
    static let alignLeft = #selector(MalachiActions.alignLeft(_:))
    static let alignCenter = #selector(MalachiActions.alignCenter(_:))
    static let alignRight = #selector(MalachiActions.alignRight(_:))
    static let bulletedList = #selector(MalachiActions.bulletedList(_:))
    static let numberedList = #selector(MalachiActions.numberedList(_:))
    static let quoteBlock = #selector(MalachiActions.quoteBlock(_:))
    static let insertLink = #selector(MalachiActions.insertLink(_:))
    static let clearFormatting = #selector(MalachiActions.clearFormatting(_:))

    /// The native split-view action, sent by the toolbar's sidebar item and
    /// the View menu.
    static let toggleSidebar = #selector(NSSplitViewController.toggleSidebar(_:))

    /// The single-letter accelerators of the GTK UI (`a`, `j`, `u`, `s`,
    /// Delete): a menu item with one of these must not fire while the user
    /// types in a text view (window controllers refuse them in validation).
    static let bareKeyActions: Set<Selector> = [archive, markAsJunk, markAsUnread, toggleFlag, moveToTrash]
}

/// Strips the GTK mnemonic marker of a msgid for a title without one:
/// the first single `_` before a character goes (`"_New Message"` →
/// `"New Message"`), a doubled `__` is a literal underscore.
func mn(_ s: String) -> String {
    var out = ""
    out.reserveCapacity(s.utf8.count)
    var removed = false
    var i = s.startIndex
    while i < s.endIndex {
        let c = s[i]
        let next = s.index(after: i)
        if c == "_" {
            if next < s.endIndex, s[next] == "_" {
                out.append("_")
                i = s.index(after: next)
                continue
            }
            if !removed, next < s.endIndex {
                removed = true
                i = next
                continue
            }
        }
        out.append(c)
        i = next
    }
    return out
}

/// The message list's filters as menu items (window.blp `message_filter`,
/// in the Mac's form: a toolbar menu and the View menu, as in Mail). An
/// item's tag is the filter's index; validation checks the current one.
@MainActor
enum FilterMenu {
    static let filters: [MessageFilter] = [.all, .unread, .flagged]

    static func tag(_ f: MessageFilter) -> Int { filters.firstIndex(of: f) ?? 0 }

    static func filter(tag: Int) -> MessageFilter { filters.indices.contains(tag) ? filters[tag] : .all }

    /// All, Unread, Flagged, sent through the responder chain.
    static func items() -> [NSMenuItem] {
        // Unread and Flagged are filters, not actions: show only the
        // unread messages, only the flagged ones (the GTK msgids' notes).
        let titles = [L10n.T("All"), L10n.T("Unread"), L10n.T("Flagged")]
        return titles.enumerated().map { i, title in
            let item = NSMenuItem(title: title, action: Action.setMessageFilter, keyEquivalent: "")
            item.tag = i
            return item
        }
    }
}
