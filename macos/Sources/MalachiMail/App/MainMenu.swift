// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The menu bar, built in code (no xib). Every item targets the responder
/// chain, so a window handles what it owns and the rest stays disabled
/// (`MalachiActions`). Titles with a GTK counterpart go through `L10n` with
/// the GTK msgid, mnemonic stripped; the standard macOS items have no GTK
/// counterpart and stay English.
///
/// ⌘R is the user's choice (Settings → General → Keyboard, `command-r`):
/// Mail.app's Reply, or the GTK UI's Check for New Mail; `apply(commandR:)`
/// moves the key equivalents of the four affected items.
@MainActor
enum MainMenu {
    /// The items whose key equivalents follow the `command-r` setting.
    enum ItemID {
        static let reply = NSUserInterfaceItemIdentifier("malachi.menu.reply")
        static let replyAll = NSUserInterfaceItemIdentifier("malachi.menu.replyAll")
        static let forward = NSUserInterfaceItemIdentifier("malachi.menu.forward")
        static let checkForNewMail = NSUserInterfaceItemIdentifier("malachi.menu.checkForNewMail")
    }

    struct KeyEquivalent: Equatable {
        var key: String
        var modifiers: NSEvent.ModifierFlags
    }

    /// The key equivalents of Reply, Reply All, Forward and Check for New
    /// Mail for one `command-r` mode.
    struct CommandRKeys: Equatable {
        var reply: KeyEquivalent
        var replyAll: KeyEquivalent
        var forward: KeyEquivalent
        var checkForNewMail: KeyEquivalent

        static func table(_ mode: Settings.CommandR) -> CommandRKeys {
            switch mode {
            case .reply:
                return CommandRKeys(
                    reply: KeyEquivalent(key: "r", modifiers: .command),
                    replyAll: KeyEquivalent(key: "r", modifiers: [.command, .shift]),
                    forward: KeyEquivalent(key: "f", modifiers: [.command, .shift]),
                    checkForNewMail: KeyEquivalent(key: "n", modifiers: [.command, .shift])
                )
            case .refresh:
                return CommandRKeys(
                    reply: KeyEquivalent(key: "r", modifiers: [.command, .option]),
                    replyAll: KeyEquivalent(key: "r", modifiers: [.command, .option, .shift]),
                    forward: KeyEquivalent(key: "f", modifiers: [.command, .option, .shift]),
                    checkForNewMail: KeyEquivalent(key: "r", modifiers: .command)
                )
            }
        }
    }

    /// The application name as it appears in the application menu.
    static let appName = "Malachi Mail"
    /// Help opens the README.
    static let helpURL = URL(string: "https://github.com/schotek/malachi#readme")

    static func build(_ state: AppState) -> NSMenu {
        let bar = NSMenu()
        bar.addItem(submenu(appMenu(), title: appName))
        bar.addItem(submenu(fileMenu(), title: "File")) // macOS-only string
        bar.addItem(submenu(editMenu(), title: "Edit")) // macOS-only string
        bar.addItem(submenu(viewMenu(), title: "View")) // macOS-only string
        bar.addItem(submenu(messageMenu(), title: L10n.T("Message")))
        bar.addItem(submenu(formatMenu(), title: "Format")) // macOS-only string
        let windows = windowMenu()
        bar.addItem(submenu(windows, title: "Window")) // macOS-only string
        NSApp.windowsMenu = windows
        let help = helpMenu()
        bar.addItem(submenu(help, title: "Help")) // macOS-only string
        NSApp.helpMenu = help
        apply(commandR: state.settings.commandR, to: bar)
        return bar
    }

    /// Moves the ⌘R-related key equivalents to the ones of `mode`.
    static func apply(commandR mode: Settings.CommandR, to bar: NSMenu? = NSApp.mainMenu) {
        guard let bar else { return }
        let keys = CommandRKeys.table(mode)
        set(keys.reply, on: ItemID.reply, in: bar)
        set(keys.replyAll, on: ItemID.replyAll, in: bar)
        set(keys.forward, on: ItemID.forward, in: bar)
        set(keys.checkForNewMail, on: ItemID.checkForNewMail, in: bar)
    }

    // MARK: Menus

    private static func appMenu() -> NSMenu {
        let m = NSMenu(title: appName)
        m.addItem(item(mn(L10n.T("_About Malachi Mail")), Action.showAbout))
        m.addItem(.separator())
        m.addItem(item("Settings…", Action.showPreferences, key: ",")) // macOS-only string
        m.addItem(.separator())
        let services = NSMenu()
        NSApp.servicesMenu = services
        m.addItem(submenu(services, title: "Services")) // macOS-only string
        m.addItem(.separator())
        m.addItem(item("Hide Malachi Mail", #selector(NSApplication.hide(_:)), key: "h")) // macOS-only string
        m.addItem(item("Hide Others", #selector(NSApplication.hideOtherApplications(_:)), key: "h", mods: [.command, .option])) // macOS-only string
        m.addItem(item("Show All", #selector(NSApplication.unhideAllApplications(_:)))) // macOS-only string
        m.addItem(.separator())
        m.addItem(item("Quit Malachi Mail", #selector(NSApplication.terminate(_:)), key: "q")) // macOS-only string
        return m
    }

    private static func fileMenu() -> NSMenu {
        let m = NSMenu()
        m.addItem(item(mn(L10n.T("_New Message")), Action.newMessage, key: "n"))
        m.addItem(item(mn(L10n.T("_Add Account…")), Action.addAccount))
        m.addItem(.separator())
        m.addItem(item("Close", #selector(NSWindow.performClose(_:)), key: "w")) // macOS-only string
        m.addItem(item(mn(L10n.T("_Save Draft")), Action.saveDraft, key: "s"))
        m.addItem(item(mn(L10n.T("Attach _Files…")), Action.attachFiles))
        m.addItem(item(mn(L10n.T("Insert _Image…")), Action.insertImage))
        m.addItem(.separator())
        m.addItem(item(mn(L10n.T("_Discard")), Action.discardDraft))
        return m
    }

    private static func editMenu() -> NSMenu {
        // All macOS-only strings: the standard Edit menu, which AppKit does
        // not build for a programmatic menu bar.
        let m = NSMenu()
        m.addItem(item("Undo", Selector(("undo:")), key: "z"))
        m.addItem(item("Redo", Selector(("redo:")), key: "z", mods: [.command, .shift]))
        m.addItem(.separator())
        m.addItem(item("Cut", #selector(NSText.cut(_:)), key: "x"))
        m.addItem(item("Copy", #selector(NSText.copy(_:)), key: "c"))
        m.addItem(item("Paste", #selector(NSText.paste(_:)), key: "v"))
        m.addItem(item("Paste and Match Style", #selector(NSTextView.pasteAsPlainText(_:)), key: "v", mods: [.command, .option, .shift]))
        m.addItem(item("Delete", #selector(NSText.delete(_:))))
        m.addItem(item("Select All", #selector(NSText.selectAll(_:)), key: "a"))
        m.addItem(.separator())
        // The main window's search field (MainToolbar `focusSearch`);
        // disabled where no window has one.
        m.addItem(item("Find…", Action.findMessages, key: "f"))
        m.addItem(.separator())

        let spelling = NSMenu()
        spelling.addItem(item("Show Spelling and Grammar", #selector(NSText.showGuessPanel(_:)), key: ":"))
        spelling.addItem(item("Check Document Now", #selector(NSText.checkSpelling(_:)), key: ";"))
        spelling.addItem(.separator())
        spelling.addItem(item("Check Spelling While Typing", #selector(NSTextView.toggleContinuousSpellChecking(_:))))
        spelling.addItem(item("Check Grammar With Spelling", #selector(NSTextView.toggleGrammarChecking(_:))))
        spelling.addItem(item("Correct Spelling Automatically", #selector(NSTextView.toggleAutomaticSpellingCorrection(_:))))
        m.addItem(submenu(spelling, title: "Spelling and Grammar"))

        let substitutions = NSMenu()
        substitutions.addItem(item("Show Substitutions", #selector(NSTextView.orderFrontSubstitutionsPanel(_:))))
        substitutions.addItem(.separator())
        substitutions.addItem(item("Smart Copy/Paste", #selector(NSTextView.toggleSmartInsertDelete(_:))))
        substitutions.addItem(item("Smart Quotes", #selector(NSTextView.toggleAutomaticQuoteSubstitution(_:))))
        substitutions.addItem(item("Smart Dashes", #selector(NSTextView.toggleAutomaticDashSubstitution(_:))))
        substitutions.addItem(item("Smart Links", #selector(NSTextView.toggleAutomaticLinkDetection(_:))))
        substitutions.addItem(item("Data Detectors", #selector(NSTextView.toggleAutomaticDataDetection(_:))))
        substitutions.addItem(item("Text Replacement", #selector(NSTextView.toggleAutomaticTextReplacement(_:))))
        m.addItem(submenu(substitutions, title: "Substitutions"))

        let speech = NSMenu()
        speech.addItem(item("Start Speaking", #selector(NSTextView.startSpeaking(_:))))
        speech.addItem(item("Stop Speaking", #selector(NSTextView.stopSpeaking(_:))))
        m.addItem(submenu(speech, title: "Speech"))
        return m
    }

    private static func viewMenu() -> NSMenu {
        let m = NSMenu()
        // Key equivalent set by apply(commandR:).
        m.addItem(item(L10n.T("Check for New Mail"), Action.checkForNewMail, id: ItemID.checkForNewMail))
        m.addItem(.separator())
        // Titles alternate between Show and Hide in validation.
        m.addItem(item("Hide Sidebar", Action.toggleSidebar, key: "s", mods: [.command, .control])) // macOS-only string
        m.addItem(item("Hide Message List", Action.toggleMessageList, key: "l", mods: [.command, .option])) // macOS-only string
        m.addItem(.separator())
        // The list's filter, the toolbar's filter menu too.
        FilterMenu.items().forEach { m.addItem($0) }
        m.addItem(.separator())
        m.addItem(item(mn(L10n.T("Load _Images")), Action.loadImages))
        m.addItem(item(mn(L10n.T("Always Load Images From This _Sender")), Action.trustSender))
        return m
    }

    private static func messageMenu() -> NSMenu {
        let m = NSMenu()
        m.addItem(item(mn(L10n.T("_Send")), Action.sendMessage, key: "\r"))
        m.addItem(.separator())
        // Key equivalents set by apply(commandR:).
        m.addItem(item(L10n.T("Reply"), Action.reply, id: ItemID.reply))
        m.addItem(item(L10n.T("Reply All"), Action.replyAll, id: ItemID.replyAll))
        m.addItem(item(L10n.T("Forward"), Action.forward, id: ItemID.forward))
        m.addItem(.separator())
        m.addItem(item(mn(L10n.T("Mark as _Read")), Action.markAsRead))
        // The bare letters of the GTK accelerators (a, j, u, s, Delete);
        // the window refuses them while a text view has the keyboard.
        m.addItem(item(mn(L10n.T("Mark as _Unread")), Action.markAsUnread, key: "u", mods: []))
        m.addItem(item(L10n.T("Star"), Action.toggleFlag, key: "s", mods: []))
        m.addItem(.separator())
        m.addItem(item(L10n.T("Archive"), Action.archive, key: "a", mods: []))
        m.addItem(item(L10n.T("Mark as Junk"), Action.markAsJunk, key: "j", mods: []))
        m.addItem(item(L10n.T("Move to Trash"), Action.moveToTrash, key: "\u{8}", mods: []))
        return m
    }

    private static func formatMenu() -> NSMenu {
        let m = NSMenu()
        m.addItem(item("Bold", Action.formatBold, key: "b")) // macOS-only string
        m.addItem(item("Italic", Action.formatItalic, key: "i")) // macOS-only string
        m.addItem(item("Underline", Action.formatUnderline, key: "u")) // macOS-only string
        m.addItem(.separator())
        // The two menus of the GTK format toolbar, with their msgids.
        let paragraph = NSMenu()
        paragraph.addItem(item(L10n.T("Paragraph"), Action.formatParagraph))
        paragraph.addItem(item(L10n.T("Heading 1"), Action.formatHeading1))
        paragraph.addItem(item(L10n.T("Heading 2"), Action.formatHeading2))
        paragraph.addItem(item(L10n.T("Heading 3"), Action.formatHeading3))
        m.addItem(submenu(paragraph, title: L10n.T("Paragraph Style")))
        let alignment = NSMenu()
        alignment.addItem(item(L10n.T("Left"), Action.alignLeft))
        alignment.addItem(item(L10n.T("Center"), Action.alignCenter))
        alignment.addItem(item(L10n.T("Right"), Action.alignRight))
        m.addItem(submenu(alignment, title: L10n.T("Alignment")))
        m.addItem(item(L10n.T("Bulleted List"), Action.bulletedList))
        m.addItem(item(L10n.T("Numbered List"), Action.numberedList))
        m.addItem(item(L10n.T("Quote"), Action.quoteBlock))
        m.addItem(.separator())
        // The GTK toolbar button says "Insert Link"; the menu item opens a
        // popover, hence the ellipsis. macOS-only string
        m.addItem(item(String(format: "%@…", L10n.T("Insert Link")), Action.insertLink))
        m.addItem(item(L10n.T("Clear Formatting"), Action.clearFormatting))
        return m
    }

    private static func windowMenu() -> NSMenu {
        // macOS-only strings.
        let m = NSMenu()
        m.addItem(item("Minimize", #selector(NSWindow.performMiniaturize(_:)), key: "m"))
        m.addItem(item("Zoom", #selector(NSWindow.performZoom(_:))))
        m.addItem(.separator())
        m.addItem(item("Bring All to Front", #selector(NSApplication.arrangeInFront(_:))))
        return m
    }

    private static func helpMenu() -> NSMenu {
        let m = NSMenu()
        m.addItem(item("Malachi Mail Help", Action.openHelp)) // macOS-only string
        return m
    }

    // MARK: Helpers

    private static func item(
        _ title: String, _ action: Selector?, key: String = "", mods: NSEvent.ModifierFlags = .command,
        id: NSUserInterfaceItemIdentifier? = nil
    ) -> NSMenuItem {
        let it = NSMenuItem(title: title, action: action, keyEquivalent: key)
        it.keyEquivalentModifierMask = key.isEmpty ? [] : mods
        it.identifier = id
        return it
    }

    private static func submenu(_ menu: NSMenu, title: String) -> NSMenuItem {
        let it = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        menu.title = title
        it.submenu = menu
        return it
    }

    private static func set(_ k: KeyEquivalent, on id: NSUserInterfaceItemIdentifier, in menu: NSMenu) {
        guard let it = find(id, in: menu) else { return }
        it.keyEquivalent = k.key
        it.keyEquivalentModifierMask = k.modifiers
    }

    /// Depth-first search for an item by identifier.
    static func find(_ id: NSUserInterfaceItemIdentifier, in menu: NSMenu) -> NSMenuItem? {
        for it in menu.items {
            if it.identifier == id {
                return it
            }
            if let sub = it.submenu, let found = find(id, in: sub) {
                return found
            }
        }
        return nil
    }
}
