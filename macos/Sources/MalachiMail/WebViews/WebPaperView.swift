// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// The body area of a card for HTML and for the wait (a conversation card
/// of Mail, a message card of the board's detail). Under an HTML body it
/// is the white of the reader's page (`viewerDocument` paints a light
/// canvas whatever the appearance), so a card that scrolls into view, whose
/// web view is yet to be made or to paint, does not flash in the card's own
/// background in the dark appearance.
@MainActor
final class WebPaperView: NSView {
    /// The area holds an HTML body.
    var paper = false {
        didSet {
            if paper != oldValue {
                needsDisplay = true
            }
        }
    }

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        wantsLayer = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override var wantsUpdateLayer: Bool { true }

    override func updateLayer() {
        layer?.backgroundColor = paper ? NSColor.white.cgColor : nil
    }
}
