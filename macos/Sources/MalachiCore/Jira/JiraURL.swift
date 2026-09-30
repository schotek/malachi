// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// What ui/internal/jira reads of Go's url.Parse (jira.go SiteHost and
// IsIssueURL, wizard.go CheckSiteInput and DefaultAccountName): the scheme,
// whether the URL is opaque or carries user info, the host and its port.
// Built on URLSyntax, the byte-for-byte port of net/url that mailto and the
// link checks use, the same way Mailto.swift calls it, so a URL reads here
// as it does in the GTK UI.

import Foundation

extension Jira {
    /// The fields of a url.URL the jira package looks at.
    struct ParsedURL: Sendable, Equatable {
        /// URL.Scheme, lower case; "" for a relative reference.
        var scheme: String
        /// URL.Opaque != "": a scheme followed by a rootless path.
        var opaque: Bool
        /// URL.User != nil: the authority has an "@".
        var hasUser: Bool
        /// URL.Host: host[:port] with its escapes decoded; "" without an
        /// authority.
        var host: String

        /// URL.Hostname: the host without its port and without the
        /// brackets of an IPv6 literal.
        var hostname: String { Jira.splitHostPort(host).host }

        /// URL.Port: the digits after the last colon; "" without them.
        var port: String { Jira.splitHostPort(host).port }
    }

    /// url.Parse: nil where it reports an error (a control character, a
    /// colon before any slash in a relative reference, a malformed escape,
    /// a bad port, a host with a character or escape a host may not have,
    /// invalid user info).
    static func parseURL(_ raw: String) -> ParsedURL? {
        let bytes = Array(raw.utf8)[...]
        let (u, fragment, _) = URLSyntax.cut(bytes, UInt8(ascii: "#"))
        guard !URLSyntax.hasControlByte(u), let (scheme, rest) = URLSyntax.scheme(of: u),
              let parsed = URLSyntax.parseRest(rest, scheme: scheme) else { return nil }
        // setFragment: its escapes must be well-formed.
        if !fragment.isEmpty, URLSyntax.unescape(fragment, mode: .path) == nil {
            return nil
        }
        let user = authority(rest, scheme: scheme)?.contains(UInt8(ascii: "@")) ?? false
        return ParsedURL(scheme: scheme, opaque: !parsed.opaque.isEmpty, hasUser: user, host: parsed.host)
    }

    /// The authority url.Parse reads the user and the host from: after
    /// "//" up to the first "/", the query cut off first; nil when the URL
    /// has none (a relative reference starting with "///" has none either).
    private static func authority(_ rest: ArraySlice<UInt8>, scheme: String) -> ArraySlice<UInt8>? {
        let slash = UInt8(ascii: "/")
        let path = URLSyntax.cut(rest, UInt8(ascii: "?")).before
        guard !scheme.isEmpty || !path.starts(with: [slash, slash, slash]), path.starts(with: [slash, slash]) else {
            return nil
        }
        let after = path.dropFirst(2)
        guard let end = after.firstIndex(of: slash) else { return after }
        return after[..<end]
    }

    /// url.splitHostPort: the port after the last colon when only digits
    /// follow it, and the host without an IPv6 literal's brackets.
    static func splitHostPort(_ hostPort: String) -> (host: String, port: String) {
        var host = Array(hostPort.utf8)[...]
        var port = host[host.endIndex...]
        if let colon = host.lastIndex(of: UInt8(ascii: ":")),
           host[(colon + 1)...].allSatisfy({ $0 >= UInt8(ascii: "0") && $0 <= UInt8(ascii: "9") }) {
            port = host[(colon + 1)...]
            host = host[..<colon]
        }
        if host.count >= 2, host.first == UInt8(ascii: "["), host.last == UInt8(ascii: "]") {
            host = host.dropFirst().dropLast()
        }
        return (String(decoding: host, as: UTF8.self), String(decoding: port, as: UTF8.self))
    }
}
