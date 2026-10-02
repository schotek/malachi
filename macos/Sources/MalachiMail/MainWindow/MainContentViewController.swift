// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

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
/// In the Board mode a page takes the split view's place above the bar
/// (`setMode`), and the split view leaves the window meanwhile.
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
        statusSlot.translatesAutoresizingMaskIntoConstraints = false
        v.addSubview(statusSlot)
        view = v
        attachPanes()
        NSLayoutConstraint.activate([
            statusSlot.leadingAnchor.constraint(equalTo: v.leadingAnchor),
            statusSlot.trailingAnchor.constraint(equalTo: v.trailingAnchor),
            statusSlot.bottomAnchor.constraint(equalTo: v.bottomAnchor),
            statusSlot.heightAnchor.constraint(equalToConstant: StatusBarViewController.height),
        ])
    }

    /// Puts the split view into the content above the status bar; nothing
    /// when it is there.
    private func attachPanes() {
        let panes = split.view
        guard panes.superview !== view else { return }
        panes.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(panes, positioned: .below, relativeTo: nil)
        NSLayoutConstraint.activate([
            panes.topAnchor.constraint(equalTo: view.topAnchor),
            panes.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            panes.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            panes.bottomAnchor.constraint(equalTo: statusSlot.topAnchor),
        ])
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

    // MARK: Board

    /// The window's mode, as the window controller last set it.
    private(set) var mode: Board.Mode = .mail
    /// The page shown in place of the split view in the Board mode, nil
    /// while the panes show.
    private(set) var page: NSViewController?

    /// Shows the board's `page` in place of the split view (`.board`), from
    /// the top of the window down to the status bar, or the split view
    /// again (`.mail`, `page` ignored).
    ///
    /// In the Board mode the split view leaves the window: hidden, it would
    /// still be the window's sidebar split view, and the unified toolbar
    /// would keep its sidebar section reserved for it (and track its
    /// divider instead of the board's own split view). Its controller stays
    /// this one's child with all its state; nothing in the panes reacts to
    /// leaving the window (no pane controller overrides `viewWillAppear` /
    /// `viewDidDisappear`; `MainSplitViewController.viewDidAppear` applies
    /// the first layout once), and the views keep their frames, selection,
    /// scroll positions and documents for the way back.
    func setMode(_ newMode: Board.Mode, page newPage: NSViewController? = nil) {
        _ = view // the slot exists once the view is loaded
        mode = newMode
        let shown = newMode == .board ? newPage : nil
        if shown !== page, let old = page {
            old.view.removeFromSuperview()
            old.removeFromParent()
            page = nil
        }
        if newMode == .board {
            split.view.removeFromSuperview()
        } else {
            if split.view.superview !== view {
                // The window may have changed its size meanwhile: the
                // panes take the slot above the status bar first.
                split.prepare(for: NSSize(
                    width: view.bounds.width, height: view.bounds.height - StatusBarViewController.height))
            }
            attachPanes()
        }
        guard shown !== page, let shown else { return }
        page = shown
        addChild(shown)
        let v = shown.view
        v.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(v, positioned: .below, relativeTo: statusSlot)
        NSLayoutConstraint.activate([
            v.topAnchor.constraint(equalTo: view.topAnchor),
            v.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            v.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            v.bottomAnchor.constraint(equalTo: statusSlot.topAnchor),
        ])
    }

    // MARK: Responder chain

    /// The split view's own actions (⌃⌘S `toggleSidebar:`, ⌥⌘L
    /// `toggleMessageList:`, the assistant panel's `toggleInspector:`) keep
    /// working while the keyboard focus is in the status bar: the bar is
    /// not under the split view controller in the responder chain, but
    /// under this one.
    override func supplementalTarget(forAction action: Selector, sender: Any?) -> Any? {
        if let target = splitTarget(forAction: action) {
            return target
        }
        return super.supplementalTarget(forAction: action, sender: sender)
    }

    /// `split` when it handles `action` and the action is one of the split
    /// view's, nil otherwise, and nil where the mode does not allow the
    /// mail's views (`Board.allows`; the panes out of the window take no
    /// action, so the menu items disable themselves); in the board's List
    /// the sidebar toggle goes to the List's own split view. Shared
    /// with the window controller, which the chain reaches when no view of
    /// the window has the focus.
    func splitTarget(forAction action: Selector) -> Any? {
        if mode == .board, action == Action.toggleSidebar {
            // The board's List folds its own navigation column.
            return (page as? BoardPageViewController)?.sidebarTarget
        }
        guard Board.allows(.mailView, in: mode),
              action == Action.toggleSidebar || action == Action.toggleMessageList || action == Action.toggleInspector,
              split.responds(to: action) else { return nil }
        return split
    }
}
