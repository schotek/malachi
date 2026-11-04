// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// Adw.Spinner: an indeterminate progress indicator of a fixed size,
/// invisible while stopped.
@MainActor
final class Spinner: NSProgressIndicator {
    /// - Parameter size: 16 (inline, next to a label) or 32 (a pane's
    ///   loading state); anything else is treated as the nearer of the two.
    init(size: CGFloat = 16) {
        super.init(frame: NSRect(x: 0, y: 0, width: size, height: size))
        style = .spinning
        controlSize = size >= 24 ? .regular : .small
        isIndeterminate = true
        isDisplayedWhenStopped = false
        translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            widthAnchor.constraint(equalToConstant: size),
            heightAnchor.constraint(equalToConstant: size),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func start() {
        startAnimation(nil)
    }

    func stop() {
        stopAnimation(nil)
    }
}
