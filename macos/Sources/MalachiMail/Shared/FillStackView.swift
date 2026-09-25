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

    init(fillingViews views: [NSView] = []) {
        super.init(frame: .zero)
        orientation = .vertical
        alignment = .leading
        distribution = .fill
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
        super.removeArrangedSubview(view)
    }

    /// The leading edge comes from the `.leading` alignment; the trailing
    /// one is added here (the alignment's own trailing constraint is only
    /// `≤`, which is what leaves the width open).
    private func pin(_ view: NSView) {
        let key = ObjectIdentifier(view)
        guard trailingConstraints[key] == nil else { return }
        view.translatesAutoresizingMaskIntoConstraints = false
        let c = view.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -edgeInsets.right)
        c.isActive = true
        trailingConstraints[key] = c
    }
}
