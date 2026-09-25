// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/compose/mailto.go: a mailto: URI as compose parameters.

import Foundation

/// Why `parseMailto` refused a string.
public enum MailtoError: Error, Equatable, Sendable {
    /// url.Parse would refuse it: a control character, a colon before any
    /// scheme, a malformed escape in the path or the fragment.
    case invalidURI
    /// The scheme is not mailto.
    case notMailto
}

/// compose.ParseMailto: turns a mailto: URI into compose parameters. Only
/// the standard fields are honoured (to, cc, bcc, subject, body; keys
/// compared case-insensitively, the first value of each); the body is
/// escaped for the editor. This is the "split the query string" the
/// desktop file promises: no interpretation beyond that.
public func parseMailto(_ uri: String) throws -> ComposeParams {
    let bytes = Array(uri.utf8)[...]
    let (u, fragment, _) = URLSyntax.cut(bytes, UInt8(ascii: "#"))
    guard !URLSyntax.hasControlByte(u), let (scheme, rest) = URLSyntax.scheme(of: u),
          let parsed = URLSyntax.parseRest(rest, scheme: scheme) else {
        throw MailtoError.invalidURI
    }
    if !fragment.isEmpty, URLSyntax.unescape(fragment, mode: .path) == nil {
        throw MailtoError.invalidURI
    }
    guard scheme == "mailto" else {
        throw MailtoError.notMailto
    }
    var p = ComposeParams(kind: .new)
    if let to = URLSyntax.unescape(parsed.opaque, mode: .path) {
        p.to = AddressList.parse(to).addresses
    }
    for (key, value) in firstValues(URLSyntax.parseQuery(parsed.rawQuery)) {
        switch key.lowercased() {
        case "to":
            p.to += AddressList.parse(value).addresses
        case "cc":
            p.cc = AddressList.parse(value).addresses
        case "bcc":
            p.bcc = AddressList.parse(value).addresses
        case "subject":
            p.subject = value.trimmingCharacters(in: .whitespacesAndNewlines)
        case "body":
            p.bodyHTML = escapeText(value)
        default:
            break
        }
    }
    return p
}

/// url.Values as ParseMailto reads it: the first value of every key, keys
/// in the order of their first appearance.
private func firstValues(_ pairs: [(key: String, value: String)]) -> [(key: String, value: String)] {
    var seen = Set<String>()
    var out: [(key: String, value: String)] = []
    for (key, value) in pairs where seen.insert(key).inserted {
        out.append((key, value))
    }
    return out
}
