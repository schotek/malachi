// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The strip above a bulk message (window/bulk.go `showBulk`) as the Mac's
/// banner: the symbol of its kind, the text and the one button, with the
/// warning colour in the junk folder. Every decision is `Bulk.stripFor`'s;
/// this only shows it. The text comes from the mail and is a plain string
/// value, never markup.
extension BannerView {
    /// The SF Symbol of each kind of strip (window/bulk.go `bulkIcons`).
    static func bulkSymbol(_ kind: Bulk.StripKind) -> String {
        switch kind {
        case .newsletter: return "megaphone"
        case .list: return "person.3"
        case .automated: return "gearshape"
        case .junk: return "exclamationmark.triangle"
        case .unsubscribed: return "checkmark.circle"
        case .none: return "megaphone"
        }
    }

    /// Puts `strip` in the banner and reveals it, or hides it for a strip
    /// that is not visible. `busy`: a request is on its way and the button
    /// waits for it.
    func showBulk(_ strip: Bulk.Strip, busy: Bool, animated: Bool = false) {
        if strip.visible {
            setSymbol(Self.bulkSymbol(strip.kind), strip.warning ? .warning : .info)
            title = strip.text
            // The mnemonic of the GTK label is not a thing on a Mac button.
            buttonTitle = strip.action.isEmpty ? nil : mn(strip.action)
            buttonEnabled = !busy
        }
        reveal(strip.visible, animated: animated)
    }
}
