// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// An `AdwClamp` for the preferences and the wizard: the child takes the
/// full width up to the tightening threshold, eases towards the maximum
/// size and is centred beyond it (libadwaita `adw-clamp-layout.c`). The
/// child is laid out with Auto Layout; only its width constant is computed
/// here, so the clamp's height follows the child's.
@MainActor
final class PrefsClampView: NSView {
    /// libadwaita's default tightening threshold.
    static let defaultTighteningThreshold: CGFloat = 400

    let maximumSize: CGFloat
    let tighteningThreshold: CGFloat
    let child: NSView
    private let widthConstraint: NSLayoutConstraint

    init(maximum: CGFloat, tight: CGFloat = PrefsClampView.defaultTighteningThreshold, child: NSView) {
        maximumSize = maximum
        tighteningThreshold = min(tight, maximum)
        self.child = child
        widthConstraint = child.widthAnchor.constraint(equalToConstant: maximum)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        child.translatesAutoresizingMaskIntoConstraints = false
        addSubview(child)
        NSLayoutConstraint.activate([
            child.topAnchor.constraint(equalTo: topAnchor),
            child.bottomAnchor.constraint(equalTo: bottomAnchor),
            child.centerXAnchor.constraint(equalTo: centerXAnchor),
            widthConstraint,
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func setFrameSize(_ newSize: NSSize) {
        super.setFrameSize(newSize)
        follow(width: newSize.width)
    }

    override func layout() {
        follow(width: bounds.width)
        super.layout()
    }

    /// The child's width constant follows the clamp's own width.
    private func follow(width: CGFloat) {
        let w = PrefsClampView.clampedWidth(available: width, maximum: maximumSize, tight: tighteningThreshold)
        if widthConstraint.constant != w {
            widthConstraint.constant = w
        }
    }

    /// The child's width for an available width (`adw_clamp_layout`
    /// `clamp_size_for_size`).
    static func clampedWidth(available w: CGFloat, maximum: CGFloat, tight: CGFloat) -> CGFloat {
        let amplitude = maximum - tight
        if w <= tight {
            return max(w, 0)
        }
        if w >= maximum + amplitude {
            return maximum
        }
        let t = (w - tight) / (2 * amplitude)
        return tight + amplitude * (1 - pow(1 - t, 3))
    }
}
