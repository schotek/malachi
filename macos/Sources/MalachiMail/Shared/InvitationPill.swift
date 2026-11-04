// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The colours of an invitation's pill in the message list and on the
/// invitation card (`Cal.InvitationPill`, docs/calendar.md §5.7): an
/// invitation in blue, an update in orange, a cancellation in red, a reply
/// in the grey of a status to do. A variant of `IssuePill`: the fills are
/// the system colours at low alpha, resolved when the pill draws.
@MainActor
enum InvitationPill {
    static func colours(_ p: Cal.InvitationPill) -> IssuePill.Colours {
        switch p {
        case .invitation:
            return (.systemBlue, NSColor.systemBlue.withAlphaComponent(0.15))
        case .update:
            return (.systemOrange, NSColor.systemOrange.withAlphaComponent(0.15))
        case .cancelled:
            return (.systemRed, NSColor.systemRed.withAlphaComponent(0.12))
        case .reply, .none:
            return (.secondaryLabelColor, Tint.fg(alpha: 0.1))
        }
    }

    /// Shows `p` in `pill` (hidden for none), painted for the selection
    /// state as `IssuePill.paint` does.
    static func show(_ p: Cal.InvitationPill, in pill: PillLabel, emphasized: Bool) {
        let title = Cal.pillTitle(p)
        pill.stringValue = title
        pill.isHidden = title.isEmpty
        IssuePill.paint(pill, colours(p), emphasized: emphasized)
    }
}
