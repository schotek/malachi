// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The colours of an issue's pills in the message list and on the issue
/// card (`Jira.StatusStyle`, the GTK classes status-todo,
/// status-in-progress, status-done): a status to do, or of no known
/// category, in grey, one in progress in blue, a done one in green; the
/// Internal badge of a service-desk comment in orange. The fills are the
/// system colours at low alpha, resolved when the pill draws, so they
/// follow the appearance.
@MainActor
enum IssuePill {
    typealias Colours = (text: NSColor, fill: NSColor)

    static func colours(_ style: Jira.StatusStyle) -> Colours {
        switch style {
        case .inProgress:
            return (.systemBlue, NSColor.systemBlue.withAlphaComponent(0.15))
        case .done:
            return (.systemGreen, NSColor.systemGreen.withAlphaComponent(0.15))
        default:
            return (.secondaryLabelColor, Tint.fg(alpha: 0.1))
        }
    }

    static var internalColours: Colours {
        (.systemOrange, NSColor.systemOrange.withAlphaComponent(0.15))
    }

    /// Paints `pill` in `c`; on a selected row (`emphasized`) in the
    /// selection's text colour on a translucent fill, as the count badge.
    static func paint(_ pill: PillLabel, _ c: Colours, emphasized: Bool) {
        if emphasized {
            pill.textColor = .alternateSelectedControlTextColor
            pill.fill = NSColor.alternateSelectedControlTextColor.withAlphaComponent(0.2)
        } else {
            pill.textColor = c.text
            pill.fill = c.fill
        }
    }
}
