// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// Small helpers shared by the wizard and the preferences: GTK labels with
// mnemonics, and the GTK icon names the core reports mapped to symbols.

/// Translates a Blueprint label and strips its mnemonic marker:
/// `T("_Next")` → "Next", `__` → `_`.
func wizardLabel(_ msgid: String) -> String {
    let s = L10n.T(msgid)
    var out = ""
    var iterator = s.makeIterator()
    while let c = iterator.next() {
        if c == "_" {
            if let n = iterator.next() {
                out.append(n)
            }
            continue
        }
        out.append(c)
    }
    return out
}

/// The SF Symbol for a GTK icon name the core uses (accountwizard/results.go,
/// wizard.go, provider.go).
func wizardSymbolName(_ gtkName: String) -> String {
    switch gtkName {
    case "emblem-ok-symbolic": return "checkmark.circle"
    case "dialog-error-symbolic": return "xmark.octagon"
    case "dialog-warning-symbolic": return "exclamationmark.triangle"
    case "dialog-question-symbolic": return "questionmark.circle"
    case "system-users-symbolic": return "person.2"
    case "web-browser-symbolic": return "globe"
    case "mail-unread-symbolic", "goa-account-ms365-symbolic", "goa-account-google-symbolic": return "envelope"
    default: return "questionmark.circle"
    }
}

/// A symbol image at a point size, or an empty image when the symbol is
/// unknown to this macOS (never for the names used here).
@MainActor
func wizardSymbol(_ name: String, pointSize: CGFloat, weight: NSFont.Weight = .regular) -> NSImage {
    let image = NSImage(systemSymbolName: name, accessibilityDescription: nil) ?? NSImage()
    return image.withSymbolConfiguration(NSImage.SymbolConfiguration(pointSize: pointSize, weight: weight)) ?? image
}
