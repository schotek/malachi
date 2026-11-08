// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// What the App Store build says when a local integration (Claude Code,
/// Claude Desktop, ChatGPT) is to be added there (docs/plans/ai-api.md
/// §7.6, D17; docs/plans/mac-app-store.md §3.3): this version runs in the
/// sandbox and cannot start Claude Code or Codex, so it points at the
/// version from the website and offers an AI provider through its API
/// instead (`Assistant.apiText().storeDialogBody`). Opened by Add… of a
/// local entry of the Integrations catalogue. The website button only
/// opens the browser, never a download (App Review Guidelines 2.4.5); Add
/// Anthropic API… opens the account dialog (`AppState.Hooks.addAIAccount`).
@MainActor
enum WebsiteFeaturesDialog {
    /// Whether this build shows the pointer at all: only the App Store
    /// build, where the local agents are off.
    static var shown: Bool { !Distribution.current.allowsLocalAgents }

    /// A sheet on `window`, else a modal alert; `state` gives the hook that
    /// adds the account (without it the button does nothing).
    static func show(for window: NSWindow?, state: AppState? = nil) {
        let t = Assistant.websiteFeaturesTexts()
        let x = Assistant.apiText()
        let alert = NSAlert()
        alert.alertStyle = .informational
        alert.messageText = t.dialogTitle
        alert.informativeText = x.storeDialogBody
        alert.addButton(withTitle: t.open)
        alert.addButton(withTitle: x.storeAddAPI)
        alert.addButton(withTitle: t.close).keyEquivalent = "\u{1b}"
        let finish: (NSApplication.ModalResponse) -> Void = { [weak state] response in
            switch response {
            case .alertFirstButtonReturn:
                NSWorkspace.shared.open(AppLinks.website)
            case .alertSecondButtonReturn:
                state?.hooks.addAIAccount?(.anthropic)
            default:
                break
            }
        }
        if let window, window.isVisible {
            alert.beginSheetModal(for: window, completionHandler: finish)
        } else {
            finish(alert.runModal())
        }
    }
}
