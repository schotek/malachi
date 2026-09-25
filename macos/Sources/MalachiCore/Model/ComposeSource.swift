// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// What the pane hands to a reply or forward (ui/internal/window/
// compose_open.go), the pure parts. The backend builds the template
// (draft.create); only when it cannot answer does the compose window open
// from what the pane knows. `ComposeSource` and `ComposeKind` are the
// compose port's (Compose/ComposeParams.swift).

/// What the pane knows about the message (compose_open.go
/// `composeSource`): the summary, bettered by the full headers and the text
/// when they were loaded. A body that is not fetched contributes no text;
/// Go's zero date becomes nil.
public func composeSource(summary s: MessageSummary, message m: Message?, body b: MessageBodyResult?) -> ComposeSource {
    var src = ComposeSource(
        id: s.id, accountID: s.accountId, from: s.from, to: s.to ?? [], subject: s.subject,
        date: s.date.isGoZero ? nil : s.date
    )
    if let m {
        src.from = m.summary.from
        src.replyTo = m.replyTo ?? []
        src.to = m.summary.to ?? []
        src.cc = m.cc ?? []
        src.subject = m.summary.subject
        src.date = m.summary.date.isGoZero ? nil : m.summary.date
    }
    if let b, b.bodyState == .fetched {
        src.text = b.text
    }
    return src
}

/// `composeSource` over a cache entry, nil while nothing is loaded.
@MainActor
public func composeSource(summary s: MessageSummary, loaded lm: LoadedMessage?) -> ComposeSource {
    composeSource(summary: s, message: lm?.msg, body: lm?.body)
}

/// Names the action for an error toast (compose_open.go `composeWhat`).
public func composeWhat(_ kind: ComposeKind) -> String {
    if kind == .forward {
        return L10n.T("Preparing the forwarded message")
    }
    return L10n.T("Preparing the reply")
}

/// The toast shown when draft.create failed and the window opens with the
/// plain quote instead (compose_open.go `composeFallbackText`): nothing
/// when there is no backend to ask or it does not offer the call yet (the
/// fallback is the normal course then), the usual sentence otherwise.
public func composeFallbackText(_ what: String, _ error: any Error) -> String {
    if let e = error as? RPCClient.ClientError, e == .notConnected || e == .disconnected {
        return ""
    }
    if let e = error as? RPCError, e.code == .notImplemented {
        return ""
    }
    return rpcErrorText(what, error)
}
