// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import Quartz

/// Quick Look for attachments (attachments.go `previewAttachment`, where
/// GNOME's Sushi does the same): a click on a chip has the part written to
/// a private, quarantined file (`AttachmentActions.preview`) and shown in
/// the shared preview panel. Quick Look renders and never runs anything,
/// so programs and scripts are previewed too.
///
/// The panel takes its data from the first object in the key window's
/// responder chain that accepts control. A chip is a control that never
/// becomes first responder, so the chain rarely passes through the message
/// view; the controllers of the main window and of a message window accept
/// on this object's behalf (their `acceptsPreviewPanelControl`), and it
/// serves the one file. QuickLookUI predates Swift concurrency: the
/// callbacks are `nonisolated` and step onto the main actor they are
/// called on.
@MainActor
final class AttachmentPreview: NSObject, QLPreviewPanelDataSource, QLPreviewPanelDelegate {
    static let shared = AttachmentPreview()

    /// The file on show, and the chip it came from (the panel zooms out of
    /// it and back into it).
    private var url: URL?
    private weak var source: NSView?
    /// Between `begin` and `end`: the panel is ours.
    private var controlling = false

    /// Shows `url` in the panel, opening it or swapping what it shows.
    func show(url: URL, source: NSView?) {
        self.url = url
        self.source = source
        guard let panel = QLPreviewPanel.shared() else { return }
        if panel.isVisible, controlling {
            panel.reloadData()
        } else {
            panel.makeKeyAndOrderFront(nil)
        }
    }

    // MARK: Control, forwarded by the window controllers

    /// Whether there is a file to show; the window controllers answer the
    /// panel's `acceptsPreviewPanelControl` with it.
    var accepts: Bool { url != nil }

    func begin(_ panel: QLPreviewPanel) {
        controlling = true
        panel.dataSource = self
        panel.delegate = self
    }

    func end(_ panel: QLPreviewPanel) {
        controlling = false
    }

    // MARK: QLPreviewPanelDataSource

    nonisolated func numberOfPreviewItems(in panel: QLPreviewPanel!) -> Int {
        MainActor.assumeIsolated { url == nil ? 0 : 1 }
    }

    nonisolated func previewPanel(_ panel: QLPreviewPanel!, previewItemAt index: Int) -> (any QLPreviewItem)! {
        // A URL crosses back (it is Sendable, the item protocol is not);
        // NSURL is a preview item through Quartz's own category.
        guard let url = MainActor.assumeIsolated({ self.url }) else { return nil }
        return url as NSURL
    }

    // MARK: QLPreviewPanelDelegate

    /// The chip's frame on screen, for the zoom; zero (a fade) when the
    /// chip is gone or off screen.
    nonisolated func previewPanel(_ panel: QLPreviewPanel!, sourceFrameOnScreenFor item: (any QLPreviewItem)!) -> NSRect {
        MainActor.assumeIsolated {
            guard let source, let window = source.window, !source.isHiddenOrHasHiddenAncestor else { return .zero }
            return window.convertToScreen(source.convert(source.bounds, to: nil))
        }
    }
}
