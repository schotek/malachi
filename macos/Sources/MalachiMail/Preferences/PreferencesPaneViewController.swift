// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// The document view of a settings or wizard scroll view: flipped, so that
/// content shorter than the view sits at the top instead of the bottom,
/// and as wide as the clip view (it never scrolls sideways).
@MainActor
final class PrefsDocumentView: NSView {
    override var isFlipped: Bool { true }
}

/// Builds the scroll view → document → `PrefsClampView` → `content` chain
/// of an `AdwPreferencesPage`: vertical scrolling only, transparent over
/// the window, the content clamped to `clampMaximum` and centred.
@MainActor
func prefsScrollView(clampMaximum: CGFloat, content: NSView) -> NSScrollView {
    let clamp = PrefsClampView(maximum: clampMaximum, child: content)
    let doc = PrefsDocumentView()
    doc.translatesAutoresizingMaskIntoConstraints = false
    doc.addSubview(clamp)
    let scroll = NSScrollView()
    scroll.hasVerticalScroller = true
    scroll.hasHorizontalScroller = false
    scroll.autohidesScrollers = true
    scroll.drawsBackground = false
    scroll.documentView = doc
    let clip = scroll.contentView
    NSLayoutConstraint.activate([
        clamp.topAnchor.constraint(equalTo: doc.topAnchor),
        clamp.bottomAnchor.constraint(equalTo: doc.bottomAnchor),
        clamp.leadingAnchor.constraint(equalTo: doc.leadingAnchor),
        clamp.trailingAnchor.constraint(equalTo: doc.trailingAnchor),
        doc.leadingAnchor.constraint(equalTo: clip.leadingAnchor),
        doc.trailingAnchor.constraint(equalTo: clip.trailingAnchor),
        doc.topAnchor.constraint(equalTo: clip.topAnchor),
    ])
    return scroll
}

/// A vertical stack whose arranged views are meant to span its width: add
/// them with `prefsAddFilling`. (A vertical stack's `.width` alignment does
/// not stretch its views; an explicit constraint does.)
@MainActor
func prefsColumn(spacing: CGFloat, insets: NSEdgeInsets = NSEdgeInsets()) -> NSStackView {
    let stack = NSStackView()
    stack.orientation = .vertical
    stack.alignment = .leading
    stack.distribution = .fill
    stack.spacing = spacing
    stack.edgeInsets = insets
    stack.translatesAutoresizingMaskIntoConstraints = false
    return stack
}

/// Appends `view` to a vertical `stack` and makes it span the stack's
/// width inside the edge insets.
@MainActor
func prefsAddFilling(_ view: NSView, to stack: NSStackView) {
    stack.addArrangedSubview(view)
    prefsFillWidth(view, in: stack)
}

/// Pins an arranged view's width to the vertical stack's width minus its
/// horizontal edge insets.
@MainActor
func prefsFillWidth(_ view: NSView, in stack: NSStackView) {
    view.translatesAutoresizingMaskIntoConstraints = false
    let c = view.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -(stack.edgeInsets.left + stack.edgeInsets.right))
    c.priority = .required
    c.isActive = true
}

/// An `AdwPreferencesPage`: a scroll view around a clamp of 580 pt holding
/// the groups in a vertical stack, top-anchored. Subclasses add their
/// groups in `viewDidLoad`.
@MainActor
class PreferencesPaneViewController: NSViewController {
    /// The width of the settings window and its panes.
    static let paneWidth: CGFloat = 600
    static let paneHeight: CGFloat = 560

    let contentStack = prefsColumn(spacing: 24, insets: NSEdgeInsets(top: 24, left: 12, bottom: 24, right: 12))
    private(set) var scrollView = NSScrollView()

    override func loadView() {
        scrollView = prefsScrollView(clampMaximum: 580, content: contentStack)
        scrollView.frame = NSRect(x: 0, y: 0, width: PreferencesPaneViewController.paneWidth, height: PreferencesPaneViewController.paneHeight)
        view = scrollView
        preferredContentSize = NSSize(width: PreferencesPaneViewController.paneWidth, height: PreferencesPaneViewController.paneHeight)
    }

    func addGroup(_ group: PreferencesGroupView) {
        prefsAddFilling(group, to: contentStack)
    }
}
