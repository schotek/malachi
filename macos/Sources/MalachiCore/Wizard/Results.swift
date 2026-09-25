// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/accountwizard/results.go: what the test page makes of an
// account.test result.

import Foundation

/// accountwizard.Outcome: what the test page shows and which buttons it
/// offers.
public enum Outcome: Sendable, Hashable {
    /// Every endpoint answered.
    case ok
    /// Credentials rejected somewhere: back to the identity page.
    case authFailed
    /// Anything else: retry, edit, or add anyway.
    case failed
}

/// accountwizard.Classify: reduces a test result to an outcome over the
/// endpoints the backend reported (imap and smtp, or graph). A result
/// without any endpoint is a failure.
public func classify(_ res: AccountTestResult) -> Outcome {
    let endpoints = [res.imap, res.smtp, res.graph].compactMap { $0 }
    if endpoints.isEmpty {
        return .failed
    }
    var ok = true
    for r in endpoints {
        if !r.ok {
            ok = false
        }
        if let e = r.error, e.code == .authFailed {
            return .authFailed
        }
    }
    return ok ? .ok : .failed
}

/// accountwizard.EndpointSummary: the row icon (a GTK icon name) and
/// subtitle for one endpoint; nil is an endpoint the backend did not test.
/// The server's capabilities are hostile data and deliberately not shown.
public func endpointSummary(_ r: EndpointTestResult?) -> (icon: String, text: String) {
    guard let r else {
        return ("dialog-question-symbolic", L10n.T("Not tested"))
    }
    if r.ok {
        // TRANSLATORS: %d is the connection latency in milliseconds.
        return ("emblem-ok-symbolic", L10n.T("Connected in %d ms", r.latencyMs))
    }
    return ("dialog-error-symbolic", endpointErrorText(r.error))
}
