// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// What Malachi Mail from malachimail.app adds to the App Store build
/// (`Assistant.websiteFeaturesTexts`, docs/plans/mac-app-store.md §3.3):
/// opened from the group in Settings → AI and from the status bar's link.
/// Its button only opens the website in the browser, never a download
/// (App Review Guidelines 2.4.5).
@MainActor
enum WebsiteFeaturesDialog {
    /// Whether this build shows the pointer at all: only the App Store
    /// build, where the local agents are off.
    static var shown: Bool { !Distribution.current.allowsLocalAgents }

    /// A sheet on `window`, else a modal alert.
    static func show(for window: NSWindow?) {
        let t = Assistant.websiteFeaturesTexts()
        let alert = NSAlert()
        alert.alertStyle = .informational
        alert.messageText = t.dialogTitle
        alert.informativeText = ([t.dialogIntro, t.features.map { "• " + $0 }.joined(separator: "\n")]
            + [t.free, t.data]).joined(separator: "\n\n")
        alert.addButton(withTitle: t.open)
        alert.addButton(withTitle: t.close).keyEquivalent = "\u{1b}"
        let finish: (NSApplication.ModalResponse) -> Void = { response in
            if response == .alertFirstButtonReturn {
                NSWorkspace.shared.open(AppLinks.website)
            }
        }
        if let window, window.isVisible {
            alert.beginSheetModal(for: window, completionHandler: finish)
        } else {
            finish(alert.runModal())
        }
    }
}
