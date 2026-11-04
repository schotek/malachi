// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit

/// The look of an address capsule (menubutton.address-chip in
/// ui/internal/style), shared by the chips of a message's header
/// (`PillButton`) and the badges of the compose recipient rows
/// (`RecipientBadgeView`): a faint grey tint in a full-round capsule, the
/// title in the chip font. The tint is drawn, not set on a layer, so it
/// follows the appearance.
@MainActor
enum CapsuleStyle {
    static let height: CGFloat = 24
    static let padding: CGFloat = 10
    static var font: NSFont { Typo.chip }

    /// The resting tint of a filled capsule.
    static let restingAlpha: CGFloat = 0.07

    /// The capsule outline in `rect`.
    static func path(in rect: NSRect) -> NSBezierPath {
        let r = rect.height / 2
        return NSBezierPath(roundedRect: rect, xRadius: r, yRadius: r)
    }
}
