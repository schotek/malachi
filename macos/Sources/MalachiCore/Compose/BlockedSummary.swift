// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/compose/draft.go blockedSummary and skippedSummary: the lines
// the compose window shows once about what the sanitiser removed from a
// quoted original and what of the original could not be attached.

import Foundation

/// compose.blockedSummary: describes what the backend's sanitiser removed;
/// empty when nothing was.
public func blockedSummary(_ b: BlockedContent) -> String {
    let n = b.remoteImages + b.remoteStyles + b.remoteFonts + b.scripts + b.forms
        + b.eventHandlers + b.dangerousUrls + b.embeddedFrames + b.trackingPixels
    if n == 0 {
        return ""
    }
    return L10n.N("%d unsafe element was removed from the message",
                  "%d unsafe elements were removed from the message", n)
}

/// compose.skippedSummary: how many parts of the original draft.create did
/// not import (`ComposeParams.skipped`); empty when none.
public func skippedSummary(_ n: Int) -> String {
    if n <= 0 {
        return ""
    }
    return L10n.N("%d attachment of the original could not be attached",
                  "%d attachments of the original could not be attached", n)
}
