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

/// accountwizard.PasswordMissing: account.test failed as a whole with
/// authRequired for an account that signs in with a password (`linked`
/// false): no password is stored and none was typed. The wizard asks for
/// it on the identity page, as for a refused one.
public func passwordMissing(_ error: (any Error)?, linked: Bool) -> Bool {
    guard !linked, let e = error as? RPCError else { return false }
    return e.code == .authRequired
}

/// accountwizard.passwordBannerText: the identity page's banner when the
/// password is what to fix: `authRequired` means none is stored, anything
/// else (`authFailed`) that the server refused it. `imapHost` is the
/// incoming server of the account: Google's refuses the password of the
/// account itself and takes an app password only, which the banner then says.
public func passwordBannerText(_ reason: ErrorCode, imapHost: String) -> String {
    if reason == .authRequired {
        // TRANSLATORS: wizard banner on the identity page
        return L10n.T("No password is stored for this account. Enter it to continue.")
    }
    if googleHost(imapHost) {
        return L10n.T("Google rejected the sign-in. It does not accept the account's password here: create an app password in your Google Account and enter that one. For a work or school account, the administrator has to allow app passwords and IMAP.")
    }
    return L10n.T("The server rejected the user name or password")
}

/// accountwizard.GoogleHost: a mail server of Gmail and Google Workspace, as
/// typed or discovered: the case and the spaces around it do not matter.
public func googleHost(_ host: String) -> Bool {
    var h = host.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    if h.hasSuffix(".") { h.removeLast() }
    return ["imap.gmail.com", "smtp.gmail.com", "imap.googlemail.com", "smtp.googlemail.com"].contains(h)
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
