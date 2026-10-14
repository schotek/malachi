// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// How tall the board's inline reply editor is: as tall as its content,
// never shorter than a few lines, never taller than most of what the
// detail shows (above that it scrolls inside). Swift-first, like the board.

import CoreGraphics

/// The inline reply editor's height for a content height and the height
/// of the detail's visible part.
public struct EditorHeight: Sendable, Equatable {
    /// Never shorter: a few lines to type into, even when empty.
    public static let minimum: CGFloat = 160
    /// Never taller, however tall the detail.
    public static let maximum: CGFloat = 480
    /// Never taller than this share of the detail's visible height (but
    /// never below `minimum`).
    public static let visibleShare: CGFloat = 0.6

    /// The editor's height.
    public let height: CGFloat
    /// The content is taller than `height`: the editor scrolls inside.
    public let scrolls: Bool

    /// The content height clamped to `[minimum, cap(visible:)]`. A content
    /// height that is not a finite number counts as empty.
    public init(content: CGFloat, visible: CGFloat) {
        let cap = Self.cap(visible: visible)
        let c = content.isFinite ? content : 0
        height = Swift.min(Swift.max(c, Self.minimum), cap)
        scrolls = c > height
    }

    /// `min(maximum, visibleShare × visible)`, never below `minimum`; a
    /// visible height that is not a finite positive number gives `maximum`.
    public static func cap(visible: CGFloat) -> CGFloat {
        guard visible.isFinite, visible > 0 else { return maximum }
        return Swift.max(minimum, Swift.min(maximum, visibleShare * visible))
    }
}
