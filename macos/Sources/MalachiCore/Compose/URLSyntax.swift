// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The slice of Go's net/url that ui/internal/compose/mailto.go and
// ui/internal/htmlview/links.go rely on, reproduced byte for byte so that
// `parseMailto` and `isMasked` read a URI exactly as the GTK UI does.
// Foundation's URL is stricter in places (spaces, non-ASCII) and looser in
// others (percent escapes), and the phishing check must not drift.

import Foundation

enum URLSyntax {
    private static let percent = UInt8(ascii: "%")
    private static let plus = UInt8(ascii: "+")
    private static let space = UInt8(ascii: " ")
    private static let slash = UInt8(ascii: "/")
    private static let question = UInt8(ascii: "?")
    private static let hash = UInt8(ascii: "#")
    private static let colon = UInt8(ascii: ":")
    private static let at = UInt8(ascii: "@")
    private static let lbracket = UInt8(ascii: "[")
    private static let rbracket = UInt8(ascii: "]")
    private static let ampersand = UInt8(ascii: "&")
    private static let equals = UInt8(ascii: "=")
    private static let semicolon = UInt8(ascii: ";")

    /// Which component is being unescaped (net/url's `encoding`); only the
    /// host and a query component have rules of their own.
    enum Mode {
        /// A path, a path segment, a fragment or userinfo.
        case path
        case queryComponent
        case host
    }

    /// strings.Cut on bytes: the part before the first `sep`, the part
    /// after it, and whether it was found.
    static func cut(_ b: ArraySlice<UInt8>, _ sep: UInt8) -> (before: ArraySlice<UInt8>, after: ArraySlice<UInt8>, found: Bool) {
        guard let i = b.firstIndex(of: sep) else {
            return (b, b[b.endIndex...], false)
        }
        return (b[..<i], b[(i + 1)...], true)
    }

    /// stringContainsCTLByte: url.Parse refuses these outright.
    static func hasControlByte(_ b: ArraySlice<UInt8>) -> Bool {
        b.contains { $0 < 0x20 || $0 == 0x7f }
    }

    /// getScheme: the scheme (lower-cased, as url.Parse stores it) and the
    /// rest; an empty scheme when the string has none. nil when a colon
    /// comes first, which url.Parse reports as an error.
    static func scheme(of raw: ArraySlice<UInt8>) -> (scheme: String, rest: ArraySlice<UInt8>)? {
        var i = raw.startIndex
        while i < raw.endIndex {
            let c = raw[i]
            switch c {
            case UInt8(ascii: "a")...UInt8(ascii: "z"), UInt8(ascii: "A")...UInt8(ascii: "Z"):
                break
            case UInt8(ascii: "0")...UInt8(ascii: "9"), UInt8(ascii: "+"), UInt8(ascii: "-"), UInt8(ascii: "."):
                if i == raw.startIndex {
                    return ("", raw)
                }
            case colon:
                if i == raw.startIndex {
                    return nil
                }
                return (String(decoding: raw[..<i], as: UTF8.self).lowercased(), raw[(i + 1)...])
            default:
                // An invalid character: there is no valid scheme.
                return ("", raw)
            }
            i += 1
        }
        return ("", raw)
    }

    /// unescape: percent-decoding with net/url's checks. nil on a malformed
    /// escape, on an escape a host may not carry (only bytes beyond ASCII,
    /// and "%25"), and on a byte a host may not contain. `+` becomes a
    /// space only in a query component. Decoded bytes that are not UTF-8
    /// are replaced, the way Swift reads them.
    static func unescape(_ s: ArraySlice<UInt8>, mode: Mode) -> String? {
        var n = 0
        var hasPlus = false
        var i = s.startIndex
        while i < s.endIndex {
            switch s[i] {
            case percent:
                n += 1
                guard i + 2 < s.endIndex, isHex(s[i + 1]), isHex(s[i + 2]) else { return nil }
                if mode == .host, unhex(s[i + 1]) < 8, !(s[i + 1] == UInt8(ascii: "2") && s[i + 2] == UInt8(ascii: "5")) {
                    return nil
                }
                i += 3
            case plus:
                hasPlus = mode == .queryComponent
                i += 1
            default:
                if mode == .host, s[i] < 0x80, hostShouldEscape(s[i]) {
                    return nil
                }
                i += 1
            }
        }
        if n == 0 && !hasPlus {
            return String(decoding: s, as: UTF8.self)
        }
        var out: [UInt8] = []
        out.reserveCapacity(s.count - 2 * n)
        i = s.startIndex
        while i < s.endIndex {
            switch s[i] {
            case percent:
                out.append(unhex(s[i + 1]) << 4 | unhex(s[i + 2]))
                i += 3
            case plus:
                out.append(mode == .queryComponent ? space : plus)
                i += 1
            default:
                out.append(s[i])
                i += 1
            }
        }
        return String(decoding: out, as: UTF8.self)
    }

    /// url.ParseQuery as `Query()` exposes it: the pairs in order, malformed
    /// ones (a bad escape, a semicolon) skipped.
    static func parseQuery(_ query: ArraySlice<UInt8>) -> [(key: String, value: String)] {
        var out: [(key: String, value: String)] = []
        var rest = query
        while !rest.isEmpty {
            let (pair, after, _) = cut(rest, ampersand)
            rest = after
            if pair.contains(semicolon) || pair.isEmpty {
                continue
            }
            let (rawKey, rawValue, _) = cut(pair, equals)
            guard let key = unescape(rawKey, mode: .queryComponent),
                  let value = unescape(rawValue, mode: .queryComponent) else { continue }
            out.append((key, value))
        }
        return out
    }

    /// What url.Parse makes of the part after the scheme: the host (`""`
    /// without one; the whole rest is opaque when it has a scheme and no
    /// leading slash) and the query. nil where url.Parse reports an error.
    static func parseRest(_ rest0: ArraySlice<UInt8>, scheme: String) -> (host: String, opaque: ArraySlice<UInt8>, rawQuery: ArraySlice<UInt8>)? {
        var rest = rest0
        var rawQuery = rest[rest.endIndex...]
        if rest.last == question, rest.filter({ $0 == question }).count == 1 {
            rest = rest.dropLast()
        } else {
            (rest, rawQuery, _) = cut(rest, question)
        }
        if rest.first != slash {
            if !scheme.isEmpty {
                // A rootless path with a scheme is opaque.
                return ("", rest, rawQuery)
            }
            let segment = cut(rest, slash).before
            if segment.contains(colon) {
                return nil
            }
        }
        var host = ""
        if (!scheme.isEmpty || !rest.starts(with: [slash, slash, slash])), rest.starts(with: [slash, slash]) {
            var authority = rest.dropFirst(2)
            rest = authority[authority.endIndex...]
            if let i = authority.firstIndex(of: slash) {
                rest = authority[i...]
                authority = authority[..<i]
            }
            guard let h = parseAuthority(authority) else { return nil }
            host = h
        }
        // setPath: the path's escapes must be well-formed.
        guard unescape(rest, mode: .path) != nil else { return nil }
        return (host, rest[rest.endIndex...], rawQuery)
    }

    /// url.Parse(raw).Hostname(): nil when Parse fails, "" when the URL has
    /// no host, otherwise the host without its port and brackets.
    static func hostname(of raw: String) -> String? {
        let bytes = Array(raw.utf8)[...]
        let (u, fragment, _) = cut(bytes, hash)
        guard !hasControlByte(u), let (scheme, rest) = scheme(of: u),
              let parsed = parseRest(rest, scheme: scheme) else { return nil }
        // setFragment: its escapes must be well-formed.
        if !fragment.isEmpty, unescape(fragment, mode: .path) == nil {
            return nil
        }
        return hostnameWithoutPort(parsed.host)
    }

    /// parseAuthority: the host after the last "@"; the userinfo before it
    /// must be valid.
    private static func parseAuthority(_ authority: ArraySlice<UInt8>) -> String? {
        let atIndex = authority.lastIndex(of: at)
        let hostPart = atIndex.map { authority[($0 + 1)...] } ?? authority
        guard let host = parseHost(hostPart) else { return nil }
        if let atIndex {
            let userinfo = authority[..<atIndex]
            guard validUserinfo(userinfo), unescape(userinfo, mode: .path) != nil else { return nil }
        }
        return host
    }

    /// parseHost: a bracketed literal must close and carry a valid port;
    /// otherwise anything after the last colon must be a valid port. An
    /// IPv6 zone ("%25…") is not given its own rules: it decodes as any
    /// other host escape does.
    private static func parseHost(_ host: ArraySlice<UInt8>) -> String? {
        if host.first == lbracket {
            guard let i = host.lastIndex(of: rbracket), validOptionalPort(host[(i + 1)...]) else { return nil }
        } else if let i = host.lastIndex(of: colon), !validOptionalPort(host[i...]) {
            return nil
        }
        return unescape(host, mode: .host)
    }

    /// URL.Hostname: splitHostPort, then the brackets of an IPv6 literal.
    private static func hostnameWithoutPort(_ host: String) -> String {
        var h = Array(host.utf8)[...]
        if let i = h.lastIndex(of: colon), validOptionalPort(h[i...]) {
            h = h[..<i]
        }
        if h.count >= 2, h.first == lbracket, h.last == rbracket {
            h = h.dropFirst().dropLast()
        }
        return String(decoding: h, as: UTF8.self)
    }

    /// validOptionalPort: empty, or a colon followed by digits only.
    private static func validOptionalPort(_ port: ArraySlice<UInt8>) -> Bool {
        guard let first = port.first else { return true }
        guard first == colon else { return false }
        return port.dropFirst().allSatisfy { $0 >= UInt8(ascii: "0") && $0 <= UInt8(ascii: "9") }
    }

    private static func validUserinfo(_ s: ArraySlice<UInt8>) -> Bool {
        s.allSatisfy { c in
            if isAlnum(c) {
                return true
            }
            switch c {
            case UInt8(ascii: "-"), UInt8(ascii: "."), UInt8(ascii: "_"), UInt8(ascii: ":"), UInt8(ascii: "~"),
                 UInt8(ascii: "!"), UInt8(ascii: "$"), UInt8(ascii: "&"), UInt8(ascii: "'"), UInt8(ascii: "("),
                 UInt8(ascii: ")"), UInt8(ascii: "*"), UInt8(ascii: "+"), UInt8(ascii: ","), UInt8(ascii: ";"),
                 UInt8(ascii: "="), UInt8(ascii: "%"), UInt8(ascii: "@"):
                return true
            default:
                return false
            }
        }
    }

    /// shouldEscape for a host: alphanumerics, the sub-delims plus
    /// `:[]<>"`, and `-_.~` may appear; everything else may not.
    private static func hostShouldEscape(_ c: UInt8) -> Bool {
        if isAlnum(c) {
            return false
        }
        switch c {
        case UInt8(ascii: "!"), UInt8(ascii: "$"), UInt8(ascii: "&"), UInt8(ascii: "'"), UInt8(ascii: "("),
             UInt8(ascii: ")"), UInt8(ascii: "*"), UInt8(ascii: "+"), UInt8(ascii: ","), UInt8(ascii: ";"),
             UInt8(ascii: "="), UInt8(ascii: ":"), UInt8(ascii: "["), UInt8(ascii: "]"), UInt8(ascii: "<"),
             UInt8(ascii: ">"), UInt8(ascii: "\""):
            return false
        case UInt8(ascii: "-"), UInt8(ascii: "_"), UInt8(ascii: "."), UInt8(ascii: "~"):
            return false
        default:
            return true
        }
    }

    private static func isAlnum(_ c: UInt8) -> Bool {
        (c >= UInt8(ascii: "a") && c <= UInt8(ascii: "z")) || (c >= UInt8(ascii: "A") && c <= UInt8(ascii: "Z"))
            || (c >= UInt8(ascii: "0") && c <= UInt8(ascii: "9"))
    }

    private static func isHex(_ c: UInt8) -> Bool {
        (c >= UInt8(ascii: "0") && c <= UInt8(ascii: "9")) || (c >= UInt8(ascii: "a") && c <= UInt8(ascii: "f"))
            || (c >= UInt8(ascii: "A") && c <= UInt8(ascii: "F"))
    }

    private static func unhex(_ c: UInt8) -> UInt8 {
        switch c {
        case UInt8(ascii: "0")...UInt8(ascii: "9"):
            return c - UInt8(ascii: "0")
        case UInt8(ascii: "a")...UInt8(ascii: "f"):
            return c - UInt8(ascii: "a") + 10
        case UInt8(ascii: "A")...UInt8(ascii: "F"):
            return c - UInt8(ascii: "A") + 10
        default:
            return 0
        }
    }
}
