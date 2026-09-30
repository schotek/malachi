// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The rules of Go's unicode, unicode/utf8 and encoding/json packages that
// ui/internal/assistant relies on, so that the port reads hostile text
// exactly as the Go package does: byte offsets into UTF-8, Go's classes
// (unicode.IsSpace is the White_Space property, unicode.IsControl only the
// C0 and C1 controls, never Swift's Cf-including CharacterSet), U+FFFD for
// bad UTF-8, and JSON strings written as encoding/json writes them. No
// texts here.

import Foundation

extension Assistant {
    // MARK: UTF-8

    /// utf8.DecodeRune of b[i..<end]: the scalar and its length in bytes;
    /// (U+FFFD, 1) for an invalid, overlong, surrogate or short sequence.
    static func decodeRune(_ b: [UInt8], _ i: Int, _ end: Int) -> (UInt32, Int) {
        b.withUnsafeBufferPointer { decodeRune($0, i, end) }
    }

    /// utf8.DecodeLastRune of b[lo..<end].
    static func decodeLastRune(_ b: [UInt8], _ lo: Int, _ end: Int) -> (UInt32, Int) {
        b.withUnsafeBufferPointer { decodeLastRune($0, lo, end) }
    }

    /// `decodeRune` over a buffer (the Markdown scanner's hot path).
    static func decodeRune(_ b: UnsafeBufferPointer<UInt8>, _ i: Int, _ end: Int) -> (UInt32, Int) {
        let c0 = b[i]
        if c0 < 0x80 {
            return (UInt32(c0), 1)
        }
        let n = end - i
        if c0 < 0xC2 || c0 > 0xF4 {
            return (0xFFFD, 1)
        }
        if c0 < 0xE0 {
            guard n >= 2, b[i + 1] & 0xC0 == 0x80 else { return (0xFFFD, 1) }
            return (UInt32(c0 & 0x1F) << 6 | UInt32(b[i + 1] & 0x3F), 2)
        }
        if c0 < 0xF0 {
            let lo: UInt8 = c0 == 0xE0 ? 0xA0 : 0x80
            let hi: UInt8 = c0 == 0xED ? 0x9F : 0xBF
            guard n >= 3, b[i + 1] >= lo, b[i + 1] <= hi, b[i + 2] & 0xC0 == 0x80 else { return (0xFFFD, 1) }
            return (UInt32(c0 & 0x0F) << 12 | UInt32(b[i + 1] & 0x3F) << 6 | UInt32(b[i + 2] & 0x3F), 3)
        }
        let lo: UInt8 = c0 == 0xF0 ? 0x90 : 0x80
        let hi: UInt8 = c0 == 0xF4 ? 0x8F : 0xBF
        guard n >= 4, b[i + 1] >= lo, b[i + 1] <= hi, b[i + 2] & 0xC0 == 0x80, b[i + 3] & 0xC0 == 0x80 else {
            return (0xFFFD, 1)
        }
        return (
            UInt32(c0 & 0x07) << 18 | UInt32(b[i + 1] & 0x3F) << 12 | UInt32(b[i + 2] & 0x3F) << 6 | UInt32(b[i + 3] & 0x3F),
            4
        )
    }

    /// utf8.DecodeLastRune of b[lo..<end]: the last scalar and its length;
    /// (U+FFFD, 0) when the range is empty, (U+FFFD, 1) when it does not
    /// end with a whole character.
    static func decodeLastRune(_ b: UnsafeBufferPointer<UInt8>, _ lo: Int, _ end: Int) -> (UInt32, Int) {
        guard end > lo else { return (0xFFFD, 0) }
        var start = end - 1
        if b[start] < 0x80 {
            return (UInt32(b[start]), 1)
        }
        let lim = max(end - 4, lo)
        start -= 1
        while start >= lim {
            if b[start] & 0xC0 != 0x80 {
                break
            }
            start -= 1
        }
        if start < lo {
            start = lo
        }
        let (r, size) = decodeRune(b, start, end)
        if start + size != end {
            return (0xFFFD, 1)
        }
        return (r, size)
    }

    /// utf8.AppendRune: a scalar's UTF-8; U+FFFD for a surrogate or a
    /// value past U+10FFFF.
    static func appendUTF8(_ r: UInt32, to out: inout [UInt8]) {
        switch r {
        case 0..<0x80:
            out.append(UInt8(r))
        case 0x80..<0x800:
            out.append(0xC0 | UInt8(r >> 6))
            out.append(0x80 | UInt8(r & 0x3F))
        case 0xD800...0xDFFF, 0x110000...:
            out.append(contentsOf: [0xEF, 0xBF, 0xBD])
        case 0x800..<0x10000:
            out.append(0xE0 | UInt8(r >> 12))
            out.append(0x80 | UInt8(r >> 6 & 0x3F))
            out.append(0x80 | UInt8(r & 0x3F))
        default:
            out.append(0xF0 | UInt8(r >> 18))
            out.append(0x80 | UInt8(r >> 12 & 0x3F))
            out.append(0x80 | UInt8(r >> 6 & 0x3F))
            out.append(0x80 | UInt8(r & 0x3F))
        }
    }

    // MARK: Character classes

    /// unicode.IsSpace: the White_Space property.
    static func isSpace(_ r: UInt32) -> Bool {
        switch r {
        case 0x09...0x0D, 0x20, 0x85, 0xA0, 0x1680, 0x2000...0x200A, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000:
            return true
        default:
            return false
        }
    }

    /// unicode.IsControl: the C0 and C1 controls and DEL (category Cc),
    /// nothing else.
    static func isControl(_ r: UInt32) -> Bool {
        r < 0x20 || (0x7F...0x9F).contains(r)
    }

    /// unicode.IsLetter: category L.
    static func isLetter(_ r: UInt32) -> Bool {
        if r < 0x80 {
            return (0x41...0x5A).contains(r) || (0x61...0x7A).contains(r)
        }
        guard let s = Unicode.Scalar(r) else { return false }
        switch s.properties.generalCategory {
        case .uppercaseLetter, .lowercaseLetter, .titlecaseLetter, .modifierLetter, .otherLetter:
            return true
        default:
            return false
        }
    }

    /// unicode.IsDigit: category Nd.
    static func isDigit(_ r: UInt32) -> Bool {
        if r < 0x80 {
            return (0x30...0x39).contains(r)
        }
        return Unicode.Scalar(r)?.properties.generalCategory == .decimalNumber
    }

    /// Lower-cases the ASCII letters and nothing else, so no other
    /// character can turn into one of them (the KELVIN SIGN stays).
    static func asciiLower(_ b: some Sequence<UInt8>) -> [UInt8] {
        b.map { (0x41...0x5A).contains($0) ? $0 + 0x20 : $0 }
    }

    /// strings.TrimSpace of b[lo..<hi]: the range without the leading and
    /// trailing White_Space characters.
    static func trimSpace(_ b: [UInt8], _ lo: Int, _ hi: Int) -> Range<Int> {
        var lo = lo
        var hi = hi
        while lo < hi {
            let (r, w) = decodeRune(b, lo, hi)
            guard isSpace(r) else { break }
            lo += w
        }
        while hi > lo {
            let (r, w) = decodeLastRune(b, lo, hi)
            guard isSpace(r) else { break }
            hi -= w
        }
        return lo..<hi
    }

    /// Go's firstLine (assistant.go): the first line of `s` that has more
    /// than spaces, without control characters (tabs included) and
    /// trimmed, cut to at most `limit` bytes at a character boundary;
    /// "unknown" when there is none. Lines end at "\n" only (a "\r" is a
    /// control character).
    static func firstLine(_ s: String, limit: Int) -> String {
        let b = Array(s.utf8)
        var start = 0
        while start <= b.count {
            var end = start
            while end < b.count, b[end] != 0x0A {
                end += 1
            }
            var kept: [UInt8] = []
            var i = start
            while i < end {
                let (r, w) = decodeRune(b, i, end)
                if !isControl(r) {
                    kept.append(contentsOf: b[i..<(i + w)])
                }
                i += w
            }
            var t = Array(kept[trimSpace(kept, 0, kept.count)])
            if !t.isEmpty {
                if t.count > limit {
                    var cut = limit
                    while cut > 0, t[cut] & 0xC0 == 0x80 {
                        cut -= 1
                    }
                    t = Array(t[trimSpace(t, 0, cut)])
                }
                return String(decoding: t, as: UTF8.self)
            }
            start = end + 1
        }
        return "unknown"
    }

    /// Go's oneLine (assistant.go): mail text (a subject, written by a
    /// third party) as one line of a label: every run of White_Space (line
    /// breaks, tabs, U+2028 and U+2029 among them) one space, the other
    /// control characters and the bidirectional formatting characters
    /// (which would reorder what follows them) left out, trimmed and cut to
    /// at most `limit` bytes at a character boundary.
    static func oneLine(_ s: String, limit: Int) -> String {
        var out: [UInt8] = []
        var space = false
        for u in s.unicodeScalars {
            let r = u.value
            if isSpace(r) {
                space = !out.isEmpty
                continue
            }
            if isControl(r) || isBidiControl(r) {
                continue
            }
            if space {
                out.append(0x20)
                space = false
            }
            appendUTF8(r, to: &out)
        }
        if out.count > limit {
            var cut = limit
            while cut > 0, out[cut] & 0xC0 == 0x80 {
                cut -= 1
            }
            out.removeSubrange(cut...)
            while out.last == 0x20 {
                out.removeLast()
            }
        }
        return String(decoding: out, as: UTF8.self)
    }

    /// Go's bidiControl: the Arabic letter mark, the left-to-right and
    /// right-to-left marks, the embeddings and overrides and the isolates.
    static func isBidiControl(_ r: UInt32) -> Bool {
        r == 0x061C || r == 0x200E || r == 0x200F || (0x202A...0x202E).contains(r) || (0x2066...0x2069).contains(r)
    }

    /// The first position of `needle` in hay[from..<to], nil when none.
    static func firstIndex(of needle: [UInt8], in hay: [UInt8], from: Int, to: Int) -> Int? {
        guard !needle.isEmpty else { return from }
        guard to - from >= needle.count else { return nil }
        var i = from
        let last = to - needle.count
        while i <= last {
            if hay[i] == needle[0] {
                var k = 1
                while k < needle.count, hay[i + k] == needle[k] {
                    k += 1
                }
                if k == needle.count {
                    return i
                }
            }
            i += 1
        }
        return nil
    }

    // MARK: JSON strings

    /// Appends `s` as encoding/json writes a string: quoted, `"` and `\`
    /// escaped, the control characters as \b \f \n \r \t or \u00XX, and,
    /// as Go escapes them for HTML, `<` `>` `&` as \u003c \u003e \u0026
    /// and U+2028 U+2029 as \u2028 \u2029. The result is one line.
    static func appendJSONString(_ s: String, to out: inout [UInt8]) {
        let hex = Array("0123456789abcdef".utf8)
        out.append(0x22)
        let b = Array(s.utf8)
        var i = 0
        while i < b.count {
            let c = b[i]
            if c < 0x80 {
                switch c {
                case 0x22, 0x5C:
                    out.append(0x5C)
                    out.append(c)
                case 0x08:
                    out.append(contentsOf: [0x5C, 0x62])
                case 0x0C:
                    out.append(contentsOf: [0x5C, 0x66])
                case 0x0A:
                    out.append(contentsOf: [0x5C, 0x6E])
                case 0x0D:
                    out.append(contentsOf: [0x5C, 0x72])
                case 0x09:
                    out.append(contentsOf: [0x5C, 0x74])
                case 0x3C, 0x3E, 0x26, 0x00..<0x20:
                    out.append(contentsOf: [0x5C, 0x75, 0x30, 0x30, hex[Int(c >> 4)], hex[Int(c & 0x0F)]])
                default:
                    out.append(c)
                }
                i += 1
                continue
            }
            let (r, w) = decodeRune(b, i, b.count)
            if r == 0x2028 || r == 0x2029 {
                out.append(contentsOf: Array("\\u202".utf8))
                out.append(hex[Int(r & 0x0F)])
            } else if r == 0xFFFD, w == 1 {
                out.append(contentsOf: Array("\\ufffd".utf8))
            } else {
                out.append(contentsOf: b[i..<(i + w)])
            }
            i += w
        }
        out.append(0x22)
    }
}
