// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The settings window (preferences.blp `preferences_dialog`): a
/// toolbar-style tab view with the Accounts, General, Appearance and AI
/// pages, 600 pt wide, titled "Settings" as macOS calls it. One instance
/// at a time; `show` brings it to the front. There is no search (deviation
/// D9).
@MainActor
final class PreferencesWindowController: NSWindowController, NSWindowDelegate {
    private static var shared: PreferencesWindowController?

    let accountsPane: AccountsPaneViewController
    let generalPane = GeneralPaneViewController()
    let appearancePane = AppearancePaneViewController()
    let aiPane = AIPaneViewController()
    let settings: Settings
    private let tabs = PrefsTabViewController()
    private let toasts = WizardToastPresenter()

    /// Opens the settings, or brings the open window to the front.
    /// `bridge` is the path of the bundled `malachi-mcp` for the AI page
    /// (`Paths.mcpBridge`; nil when there is none beside the application).
    /// `confirmRemoval` renders the "Remove this account?" alert.
    @discardableResult
    static func show(
        client: RPCClient, settings: Settings, bridge: String? = Paths.resolve().mcpBridge?.path,
        confirmRemoval: @escaping PrefsConfirmRemoval
    ) -> PreferencesWindowController {
        if let open = shared {
            open.showWindow(nil)
            open.window?.makeKeyAndOrderFront(nil)
            return open
        }
        let c = PreferencesWindowController(client: client, settings: settings, bridge: bridge, confirmRemoval: confirmRemoval)
        shared = c
        c.showWindow(nil)
        c.window?.makeKeyAndOrderFront(nil)
        return c
    }

    private init(client: RPCClient, settings: Settings, bridge: String?, confirmRemoval: @escaping PrefsConfirmRemoval) {
        self.settings = settings
        let toasts = toasts
        accountsPane = AccountsPaneViewController(client: client, confirmRemoval: confirmRemoval) { text in toasts.show(text) }
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: PreferencesPaneViewController.paneWidth, height: PreferencesPaneViewController.paneHeight),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered, defer: false
        )
        window.title = PrefsTabViewController.windowTitle
        window.toolbarStyle = .preference
        window.isReleasedWhenClosed = false
        // Never restored by the system at launch; it opens on Accounts.
        window.isRestorable = false
        // Fixed width, free height, as macOS settings windows are.
        window.contentMinSize = NSSize(width: PreferencesPaneViewController.paneWidth, height: 360)
        window.contentMaxSize = NSSize(width: PreferencesPaneViewController.paneWidth, height: 4000)
        window.center()
        window.setFrameAutosaveName("Settings")
        super.init(window: window)

        // The General and Appearance pages bind their controls once they
        // know the settings and the daemon; GTK builds every page at once,
        // so General loads config.get now rather than on its first visit.
        // The AI page asks the bridge for its status whenever it comes up.
        generalPane.configure(settings: settings, client: client) { text in toasts.show(text) }
        appearancePane.configure(settings: settings)
        aiPane.configure(bridge: bridge) { text in toasts.show(text) }
        _ = generalPane.view

        tabs.tabStyle = .toolbar
        tabs.transitionOptions = []
        let pages: [(NSViewController, String, String)] = [
            (accountsPane, L10n.T("Accounts"), "person.2"),
            (generalPane, L10n.T("General"), "gearshape"),
            (appearancePane, L10n.T("Appearance"), "paintpalette"),
            (aiPane, L10n.T("AI"), "sparkles"),
        ]
        for (controller, label, symbol) in pages {
            let item = NSTabViewItem(viewController: controller)
            item.label = label
            item.image = NSImage(systemSymbolName: symbol, accessibilityDescription: label)
            tabs.addTabViewItem(item)
        }
        window.contentViewController = tabs
        tabs.selectedTabViewItemIndex = 0
        window.delegate = self
        if let content = window.contentView {
            toasts.attach(to: content)
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func windowWillClose(_ notification: Foundation.Notification) {
        accountsPane.closed = true
        generalPane.closed = true
        appearancePane.closed = true
        aiPane.closed = true
        if PreferencesWindowController.shared === self {
            PreferencesWindowController.shared = nil
        }
    }
}

/// The tab view controller of the settings; keeps the window title the
/// same on every page and the window at the size the user gave it (a
/// toolbar-style tab view otherwise snaps to the page's preferred size).
@MainActor
final class PrefsTabViewController: NSTabViewController {
    static let windowTitle = "Settings" // macOS-only string

    override func tabView(_ tabView: NSTabView, willSelect tabViewItem: NSTabViewItem?) {
        // Only once the window shows: before that the bounds are the tab
        // view's defaults, not a size the user chose.
        if let vc = tabViewItem?.viewController, let content = view.window?.contentView, content.bounds.width > 0 {
            vc.preferredContentSize = content.bounds.size
        }
        super.tabView(tabView, willSelect: tabViewItem)
    }

    override func tabView(_ tabView: NSTabView, didSelect tabViewItem: NSTabViewItem?) {
        super.tabView(tabView, didSelect: tabViewItem)
        view.window?.title = PrefsTabViewController.windowTitle
    }
}
