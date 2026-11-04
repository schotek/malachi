// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// A vertical stack whose arranged views span its width (a GTK vertical
/// box with `hexpand` children). `NSStackView`'s `.width` alignment leaves
/// the views at their natural width on current macOS and the horizontal
/// layout ambiguous — rows came out leading- or trailing-aligned at
/// random — so both edges are pinned explicitly, edge insets included,
/// for every view added at any time.
@MainActor
final class FillStackView: NSStackView {
    private var trailingConstraints: [ObjectIdentifier: NSLayoutConstraint] = [:]
    private var leadingConstraints: [ObjectIdentifier: NSLayoutConstraint] = [:]

    init(fillingViews views: [NSView] = []) {
        super.init(frame: .zero)
        orientation = .vertical
        alignment = .leading
        distribution = .fill
        // Takes the width its parent offers (a sibling stack with the
        // default hugging would otherwise compete for it).
        setHuggingPriority(.defaultLow, for: .horizontal)
        translatesAutoresizingMaskIntoConstraints = false
        for v in views {
            addArrangedSubview(v)
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var edgeInsets: NSEdgeInsets {
        didSet {
            for c in trailingConstraints.values {
                c.constant = -edgeInsets.right
            }
            for c in leadingConstraints.values {
                c.constant = edgeInsets.left
            }
        }
    }

    override func addArrangedSubview(_ view: NSView) {
        super.addArrangedSubview(view)
        pin(view)
    }

    override func insertArrangedSubview(_ view: NSView, at index: Int) {
        super.insertArrangedSubview(view, at: index)
        pin(view)
    }

    override func removeArrangedSubview(_ view: NSView) {
        if let c = trailingConstraints.removeValue(forKey: ObjectIdentifier(view)) {
            c.isActive = false
        }
        if let c = leadingConstraints.removeValue(forKey: ObjectIdentifier(view)) {
            c.isActive = false
        }
        super.removeArrangedSubview(view)
    }

    /// Both edges are pinned at required priority: the `.leading`
    /// alignment's own leading constraint has priority 260 and its trailing
    /// one is only `≤`, which is what leaves the width open.
    private func pin(_ view: NSView) {
        let key = ObjectIdentifier(view)
        guard trailingConstraints[key] == nil else { return }
        view.translatesAutoresizingMaskIntoConstraints = false
        let trailing = view.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -edgeInsets.right)
        let leading = view.leadingAnchor.constraint(equalTo: leadingAnchor, constant: edgeInsets.left)
        NSLayoutConstraint.activate([leading, trailing])
        trailingConstraints[key] = trailing
        leadingConstraints[key] = leading
    }
}
