// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// Adw.Clamp: one child, centred, no wider than `maximum`, and eased
/// towards it from `tight` on (libadwaita adw-clamp-layout.c). The child's
/// height is the clamp's height; only the width is arbitrated here, through
/// a constraint whose constant follows the clamp's own width.
@MainActor
final class ClampView: NSView {
    let maximum: CGFloat
    let tight: CGFloat
    private(set) var child: NSView?
    private var childWidth: NSLayoutConstraint?

    /// - Parameters:
    ///   - maximum: `maximum-size`.
    ///   - tight: `tightening-threshold`; the child is as wide as the clamp
    ///     up to here.
    /// Top-left origin, like GTK. As a scroll view's document (the plain
    /// text body) an unflipped clamp shorter than the visible area sat at
    /// its bottom, and scrolling to the origin went to the end; the layout
    /// is all constraints, so nothing else changes.
    override var isFlipped: Bool { true }

    init(maximum: CGFloat, tight: CGFloat, child: NSView? = nil) {
        self.maximum = maximum
        self.tight = min(tight, maximum)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        if let child {
            setChild(child)
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Replaces the clamped child.
    func setChild(_ view: NSView?) {
        child?.removeFromSuperview()
        childWidth = nil
        child = view
        guard let view else { return }
        view.translatesAutoresizingMaskIntoConstraints = false
        addSubview(view)
        let width = view.widthAnchor.constraint(equalToConstant: Self.clampedWidth(available: bounds.width, maximum: maximum, tight: tight))
        childWidth = width
        NSLayoutConstraint.activate([
            width,
            view.centerXAnchor.constraint(equalTo: centerXAnchor),
            view.topAnchor.constraint(equalTo: topAnchor),
            view.bottomAnchor.constraint(equalTo: bottomAnchor),
        ])
    }

    override func layout() {
        let w = Self.clampedWidth(available: bounds.width, maximum: maximum, tight: tight)
        if let c = childWidth, c.constant != w {
            c.constant = w
        }
        super.layout()
    }

    /// The width the child gets for `available` (adw-clamp-layout.c
    /// `clamp_size_from_child` inverted): all of it up to `tight`, then an
    /// ease-out cubic from `tight` to `maximum` over twice the difference,
    /// then `maximum`.
    static func clampedWidth(available: CGFloat, maximum: CGFloat, tight: CGFloat) -> CGFloat {
        let lower = min(tight, maximum)
        let amplitude = maximum - lower
        if available <= lower {
            return max(available, 0)
        }
        if amplitude <= 0 || available >= maximum + amplitude {
            return maximum
        }
        let t = (available - lower) / (2 * amplitude)
        let eased = 1 - pow(1 - t, 3)
        return lower + amplitude * eased
    }
}
