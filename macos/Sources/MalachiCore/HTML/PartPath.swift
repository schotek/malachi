// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/htmlview/scheme.go and links.go: the malachi-cid: scheme the
// sanitiser rewrites cid: references to, and the checks every part
// reference passes before it reaches the daemon.

import Foundation

/// htmlview.Scheme: the URL scheme of message parts in a rendered body:
/// malachi-cid:<accountId>/<messageId>/<partId>. The path names the
/// message, so the handler is stateless and switching messages while
/// pictures still load cannot hand one message another one's part.
public let partScheme = "malachi-cid"

/// htmlview.ParsePath: splits the path of a malachi-cid: URL into the
/// account, message and part it names. Every segment is a generated id or
/// a part number; anything else is refused before it reaches the daemon.
public func parsePartPath(_ p: String) -> (accountID: AccountID, messageID: MessageID, partID: String)? {
    let seg = Array(p.utf8).split(separator: UInt8(ascii: "/"), omittingEmptySubsequences: false)
    guard seg.count == 3 else { return nil }
    // Account and message ids are a prefix and hex digits; no dots, so no
    // "..".
    for s in seg[0..<2] {
        if s.isEmpty || s.count > 128 {
            return nil
        }
        for c in s where !isIDByte(c) {
            return nil
        }
    }
    // A part number: digits joined by single dots.
    let part = seg[2]
    if part.count > 64 {
        return nil
    }
    for n in part.split(separator: UInt8(ascii: "."), omittingEmptySubsequences: false) {
        if n.isEmpty {
            return nil
        }
        for c in n where c < UInt8(ascii: "0") || c > UInt8(ascii: "9") {
            return nil
        }
    }
    return (
        AccountID(String(decoding: seg[0], as: UTF8.self)),
        MessageID(String(decoding: seg[1], as: UTF8.self)),
        String(decoding: part, as: UTF8.self)
    )
}

/// htmlview.ImageType: whether a part's media type may be shown as a
/// picture: image/* except SVG, which is a document with scripts of its
/// own.
public func isImageType(_ contentType: String) -> Bool {
    let ct = bareMediaType(contentType)
    return ct.utf8.starts(with: "image/".utf8) && ct != "image/svg+xml"
}

/// The media type without its parameters, trimmed and lower-cased (the
/// normalisation ImageType and checkInline share). The cut is at the first
/// semicolon byte, as in Go.
func bareMediaType(_ contentType: String) -> String {
    var ct = contentType.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    if let semi = ct.utf8.firstIndex(of: UInt8(ascii: ";")) {
        ct = String(decoding: ct.utf8[..<semi], as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
    }
    return ct
}

private func isIDByte(_ c: UInt8) -> Bool {
    (c >= UInt8(ascii: "a") && c <= UInt8(ascii: "z")) || (c >= UInt8(ascii: "A") && c <= UInt8(ascii: "Z"))
        || (c >= UInt8(ascii: "0") && c <= UInt8(ascii: "9")) || c == UInt8(ascii: "_") || c == UInt8(ascii: "-")
}
