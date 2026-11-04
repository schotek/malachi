// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/htmlview/links.go: pure helpers around links, testable
// without a display. Byte semantics throughout, as in Go: a combining
// mark after a separator must not change what a prefix check sees.

import Foundation

/// htmlview.AllowedLink: whether a link target may be handed to the
/// desktop or the composer: http, https or mailto, nothing else. The
/// sanitiser only lets those through, so this is a second look, not the
/// first.
public func allowedLink(_ uri: String) -> Bool {
    let lower = uri.lowercased()
    return bytesHavePrefix(lower, "http://") || bytesHavePrefix(lower, "https://") || bytesHavePrefix(lower, "mailto:")
}

/// htmlview.Masked: whether a link's visible text reads as a web address
/// of a different site than the link really leads to ("https://bank.example"
/// over a link to evil.example), which is the shape of a phishing link.
/// Text that is not an address ("click here") is never masked.
public func isMasked(text: String, href: String) -> Bool {
    let shown = hostOfText(text)
    if shown.isEmpty {
        return false
    }
    guard let target = URLSyntax.hostname(of: href.trimmingCharacters(in: .whitespacesAndNewlines)) else {
        return false
    }
    let real = target.lowercased()
    if real.isEmpty {
        return false
    }
    return !sameSite(shown, real)
}

/// htmlview.hostOfText: the host name the text claims, "" when the text is
/// not an address: a scheme, a www. prefix, or a bare host with a dot and
/// an alphabetic top-level label.
public func hostOfText(_ text: String) -> String {
    var t = text.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    if t.isEmpty || t.unicodeScalars.contains(where: { $0 == " " || $0 == "\t" || $0 == "\n" }) {
        return ""
    }
    if !bytesContain(t, "://") {
        if bytesContain(t, "@") {
            return "" // an e-mail address, not a web address
        }
        t = "http://" + t
    }
    guard let host = URLSyntax.hostname(of: t), looksLikeHost(host) else {
        return ""
    }
    return host
}

/// htmlview.looksLikeHost: at least two non-empty labels of `[a-z0-9-]`,
/// the last one alphabetic and at least two long.
public func looksLikeHost(_ h: String) -> Bool {
    let labels = Array(h.utf8).split(separator: UInt8(ascii: "."), omittingEmptySubsequences: false)
    if labels.count < 2 {
        return false
    }
    for l in labels {
        if l.isEmpty {
            return false
        }
        for c in l where !((c >= UInt8(ascii: "a") && c <= UInt8(ascii: "z")) || (c >= UInt8(ascii: "0") && c <= UInt8(ascii: "9")) || c == UInt8(ascii: "-")) {
            return false
        }
    }
    let tld = labels[labels.count - 1]
    if tld.count < 2 {
        return false
    }
    return tld.allSatisfy { $0 >= UInt8(ascii: "a") && $0 <= UInt8(ascii: "z") }
}

/// htmlview.sameSite: a host and its subdomains are one site, www. aside.
public func sameSite(_ a: String, _ b: String) -> Bool {
    let a = bytesTrimPrefix(a, "www.")
    let b = bytesTrimPrefix(b, "www.")
    return a == b || bytesHaveSuffix(a, "." + b) || bytesHaveSuffix(b, "." + a)
}

private func bytesHavePrefix(_ s: String, _ prefix: String) -> Bool {
    s.utf8.starts(with: prefix.utf8)
}

private func bytesHaveSuffix(_ s: String, _ suffix: String) -> Bool {
    s.utf8.reversed().starts(with: suffix.utf8.reversed())
}

private func bytesTrimPrefix(_ s: String, _ prefix: String) -> String {
    guard bytesHavePrefix(s, prefix) else { return s }
    return String(decoding: s.utf8.dropFirst(prefix.utf8.count), as: UTF8.self)
}

private func bytesContain(_ s: String, _ needle: String) -> Bool {
    let hay = Array(s.utf8)
    let pat = Array(needle.utf8)
    if pat.isEmpty {
        return true
    }
    guard hay.count >= pat.count else { return false }
    for i in 0...(hay.count - pat.count) where hay[i..<(i + pat.count)].elementsEqual(pat) {
        return true
    }
    return false
}
