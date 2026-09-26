// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// A view painted in the content background colour of lists and the
/// reader (white in the light appearance), GTK's `view` style class under
/// the message list and the message view. It follows the appearance: a
/// layer colour set once would stay light in dark mode.
@MainActor
final class ContentBackgroundView: NSView {
    override init(frame: NSRect) {
        super.init(frame: frame)
        wantsLayer = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        layer?.backgroundColor = NSColor.controlBackgroundColor.cgColor
    }
}
