// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// The main window's content: the three panes (`MainSplitViewController`)
/// with the status bar across the whole bottom edge under them. In GTK the
/// status line sits at the bottom of the sidebar (window.blp
/// `status_button`); here it spans the window so that it stays in sight
/// with the sidebar folded away (the deviation table in macos/README.md).
///
/// The split view is a child view controller and still reaches the top of
/// the window, so the sidebar runs under the unified toolbar and the
/// toolbar's tracking separators follow its dividers as before. The status
/// bar's view controller is installed later by the app (`install(statusBar:)`),
/// like the panes' contents; its slot keeps the bar's height meanwhile.
@MainActor
final class MainContentViewController: NSViewController {
    let split: MainSplitViewController
    /// The bar's slot at the bottom edge, `StatusBarViewController.height`
    /// high.
    private let statusSlot = NSView()
    private(set) var statusBar: NSViewController?

    init(split: MainSplitViewController) {
        self.split = split
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        let v = NSView()
        addChild(split)
        let panes = split.view
        panes.translatesAutoresizingMaskIntoConstraints = false
        statusSlot.translatesAutoresizingMaskIntoConstraints = false
        v.addSubview(panes)
        v.addSubview(statusSlot)
        NSLayoutConstraint.activate([
            panes.topAnchor.constraint(equalTo: v.topAnchor),
            panes.leadingAnchor.constraint(equalTo: v.leadingAnchor),
            panes.trailingAnchor.constraint(equalTo: v.trailingAnchor),
            panes.bottomAnchor.constraint(equalTo: statusSlot.topAnchor),
            statusSlot.leadingAnchor.constraint(equalTo: v.leadingAnchor),
            statusSlot.trailingAnchor.constraint(equalTo: v.trailingAnchor),
            statusSlot.bottomAnchor.constraint(equalTo: v.bottomAnchor),
            statusSlot.heightAnchor.constraint(equalToConstant: StatusBarViewController.height),
        ])
        view = v
    }

    /// Puts the status bar into its slot (replacing an earlier one).
    func install(statusBar vc: NSViewController) {
        _ = view // the slot exists once the view is loaded
        if let old = statusBar {
            old.view.removeFromSuperview()
            old.removeFromParent()
        }
        statusBar = vc
        addChild(vc)
        let bar = vc.view
        bar.translatesAutoresizingMaskIntoConstraints = false
        statusSlot.addSubview(bar)
        NSLayoutConstraint.activate([
            bar.topAnchor.constraint(equalTo: statusSlot.topAnchor),
            bar.bottomAnchor.constraint(equalTo: statusSlot.bottomAnchor),
            bar.leadingAnchor.constraint(equalTo: statusSlot.leadingAnchor),
            bar.trailingAnchor.constraint(equalTo: statusSlot.trailingAnchor),
        ])
    }

    // MARK: Responder chain

    /// The split view's own actions (⌃⌘S `toggleSidebar:`, ⌥⌘L
    /// `toggleMessageList:`) keep working while the keyboard focus is in
    /// the status bar: the bar is not under the split view controller in
    /// the responder chain, but under this one.
    override func supplementalTarget(forAction action: Selector, sender: Any?) -> Any? {
        if let target = Self.splitTarget(split, forAction: action) {
            return target
        }
        return super.supplementalTarget(forAction: action, sender: sender)
    }

    /// `split` when it handles `action` and the action is one of the split
    /// view's, nil otherwise. Shared with the window controller, which the
    /// chain reaches when no view of the window has the focus.
    static func splitTarget(_ split: MainSplitViewController, forAction action: Selector) -> Any? {
        guard action == Action.toggleSidebar || action == Action.toggleMessageList,
              split.responds(to: action) else { return nil }
        return split
    }
}
