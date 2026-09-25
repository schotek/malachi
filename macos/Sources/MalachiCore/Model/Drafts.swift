// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The texts of ui/internal/window/drafts.go: a message of the Drafts
// folder opens in the compose window through draft.open.

import Foundation

/// drafts.go `draftOpenUnsupported`: a daemon that does not offer
/// draft.open (the message then opens in its own window).
public func draftOpenUnsupported(_ error: any Error) -> Bool {
    guard let e = error as? RPCError else { return false }
    return e.code == .methodNotFound || e.code == .notImplemented
}

/// drafts.go `draftOpenErrorText`: a body the syncer has not downloaded
/// yet is a matter of a moment, anything else the usual sentence.
public func draftOpenErrorText(_ error: any Error) -> String {
    if let e = error as? RPCError, e.code == .unavailable {
        return L10n.T("The draft has not been downloaded yet; try again in a moment")
    }
    return rpcErrorText(L10n.T("Opening the draft"), error)
}

/// drafts.go `skippedText`: how many parts of a draft could not be taken
/// along.
public func draftSkippedText(_ n: Int) -> String {
    L10n.N("%d attachment of the draft could not be opened", "%d attachments of the draft could not be opened", n)
}
