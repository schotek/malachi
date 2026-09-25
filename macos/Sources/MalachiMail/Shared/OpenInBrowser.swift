// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// Opens a provider's sign-in page in the user's browser (widget/uri.go
/// `LaunchURI`, with the OpenURI portal's part played by NSWorkspace). The
/// page comes from the daemon (account.oauthStart, notify.authRequired) and
/// is opened only when `isBrowserURL` accepts it; a failure is reported
/// through `onError` with the GTK sentence (`LaunchErrorText`).
@MainActor
func openInBrowser(_ href: String, onError: @escaping @MainActor (String) -> Void) {
    let log = Logger(subsystem: "io.github.schotek.Malachi", category: "signin")
    guard isBrowserURL(href), let url = URL(string: href) else {
        log.warning("sign-in address refused: not https")
        onError(refusedBrowserURLText())
        return
    }
    Task { @MainActor in
        do {
            _ = try await NSWorkspace.shared.open(url, configuration: NSWorkspace.OpenConfiguration())
        } catch {
            // The URL carries the session's state; it stays out of the log.
            let ns = error as NSError
            log.warning("open sign-in page: \(ns.domain, privacy: .public) \(ns.code, privacy: .public)")
            // TRANSLATORS: %s is a technical error message.
            onError(L10n.T("The link could not be opened: %s", error.localizedDescription))
        }
    }
}
