// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The address-list helpers of ui/internal/compose/address.go: what the
// recipient rows parse and show. The backend validates authoritatively; this
// is only immediate feedback. Foundation has no RFC 5322 parser, so the
// subset net/mail.ParseAddress accepts for one mailbox is written out here.

import Foundation

public enum AddressList {
    /// compose.ParseAddressList: splits "Name <a@b>, c@d; e@f" into
    /// addresses. Tokens that do not parse are returned in `invalid` so the
    /// row can be flagged.
    public static func parse(_ s: String) -> (addresses: [Address], invalid: [String]) {
        var addresses: [Address] = []
        var invalid: [String] = []
        for range in splitRanges(s) {
            let token = String(s.unicodeScalars[range]).trimmingCharacters(in: .whitespacesAndNewlines)
            if token.isEmpty {
                continue
            }
            if let a = parseAddress(token) {
                addresses.append(a)
            } else {
                invalid.append(token)
            }
        }
        return (addresses, invalid)
    }

    /// compose.splitAddressRanges: one range per token, split on commas and
    /// semicolons that are outside quotes and angle brackets (a backslash
    /// escapes only inside quotes), separators excluded, the last range
    /// running to the end of `s`. The completion uses it to find the token
    /// under the caret with the same rules the parser applies. The indices
    /// are Unicode scalar boundaries, as Go's are rune boundaries.
    public static func splitRanges(_ s: String) -> [Range<String.Index>] {
        var out: [Range<String.Index>] = []
        let scalars = s.unicodeScalars
        var start = scalars.startIndex
        var quoted = false
        var angled = false
        var escaped = false
        var i = scalars.startIndex
        while i < scalars.endIndex {
            let r = scalars[i]
            if escaped {
                escaped = false
            } else if r == "\\" && quoted {
                escaped = true
            } else if r == "\"" {
                quoted.toggle()
            } else if quoted {
                // Inside quotes nothing separates.
            } else if r == "<" {
                angled = true
            } else if r == ">" {
                angled = false
            } else if (r == "," || r == ";") && !angled {
                out.append(start..<i)
                start = scalars.index(after: i)
            }
            i = scalars.index(after: i)
        }
        out.append(start..<scalars.endIndex)
        return out
    }

    /// compose.FormatAddressList: the inverse of `parse` for prefilled rows,
    /// "Name <addr>, addr". Names containing separators or quotes are
    /// quoted; non-ASCII names are left readable (this is UI text, not a
    /// header).
    public static func format(_ list: [Address]) -> String {
        var parts: [String] = []
        parts.reserveCapacity(list.count)
        for a in list {
            let name = (a.name ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            if name.isEmpty {
                parts.append(a.address)
            } else if name.unicodeScalars.contains(where: { quotedNameSpecials.contains($0) }) {
                let escaped = name
                    .replacingOccurrences(of: "\\", with: "\\\\")
                    .replacingOccurrences(of: "\"", with: "\\\"")
                parts.append("\"" + escaped + "\" <" + a.address + ">")
            } else {
                parts.append(name + " <" + a.address + ">")
            }
        }
        return parts.joined(separator: ", ")
    }

    /// The characters of a display name that `format` quotes: `,;<>"\`.
    private static let quotedNameSpecials: Set<Unicode.Scalar> = [",", ";", "<", ">", "\"", "\\"]

    /// One mailbox as net/mail.ParseAddress reads it: an optional
    /// display name (atoms, or a quoted-string with backslash escapes) and
    /// an angle-addr, or a bare addr-spec; addr-spec is local@domain with
    /// dot-atoms (or a quoted local part). Only whitespace may follow.
    /// Comments, groups, domain literals and RFC 2047 encoded words are not
    /// supported and read as invalid. A missing display name is nil.
    public static func parseAddress(_ s: String) -> Address? {
        var p = MailboxParser(s)
        return p.parseSingle()
    }
}

/// The relevant part of net/mail's addrParser over Unicode scalars (Go
/// iterates runes). Positions are restored on failure the way the Go
/// parser restores `p.s`.
private struct MailboxParser {
    private let s: [Unicode.Scalar]
    private var i = 0

    init(_ text: String) {
        s = Array(text.unicodeScalars)
    }

    private var isEmpty: Bool { i >= s.count }

    private func peek() -> Unicode.Scalar? {
        i < s.count ? s[i] : nil
    }

    private mutating func consume(_ c: Unicode.Scalar) -> Bool {
        guard peek() == c else { return false }
        i += 1
        return true
    }

    private mutating func skipSpace() {
        while let c = peek(), c == " " || c == "\t" {
            i += 1
        }
    }

    /// parseSingleAddress: one address, then nothing but whitespace.
    mutating func parseSingle() -> Address? {
        guard let a = parseAddress() else { return nil }
        skipSpace()
        guard isEmpty else { return nil }
        return a
    }

    /// parseAddress: addr-spec has a more restricted grammar than name-addr,
    /// so it is tried first; then display-name? angle-addr.
    private mutating func parseAddress() -> Address? {
        skipSpace()
        guard !isEmpty else { return nil }
        let save = i
        if let spec = consumeAddrSpec() {
            return Address(name: nil, address: spec)
        }
        i = save
        var displayName: String?
        if peek() != "<" {
            guard let phrase = consumePhrase() else { return nil }
            displayName = phrase.isEmpty ? nil : phrase
        }
        skipSpace()
        guard consume("<") else { return nil }
        guard let spec = consumeAddrSpec() else { return nil }
        guard consume(">") else { return nil }
        return Address(name: displayName, address: spec)
    }

    /// consumeAddrSpec: local-part "@" domain.
    private mutating func consumeAddrSpec() -> String? {
        let save = i
        guard let spec = addrSpec() else {
            i = save
            return nil
        }
        return spec
    }

    private mutating func addrSpec() -> String? {
        skipSpace()
        guard !isEmpty else { return nil }
        let localPart: String
        if peek() == "\"" {
            guard let q = consumeQuotedString(), !q.isEmpty else { return nil }
            localPart = q
        } else {
            guard let a = consumeAtom(dot: true, permissive: false) else { return nil }
            localPart = a
        }
        guard consume("@") else { return nil }
        skipSpace()
        guard !isEmpty else { return nil }
        guard let domain = consumeAtom(dot: true, permissive: false) else { return nil }
        return localPart + "@" + domain
    }

    /// consumePhrase: words (atoms or quoted strings) joined by one space.
    /// A word that fails to parse ends the phrase; the phrase fails only
    /// when it has no word at all.
    private mutating func consumePhrase() -> String? {
        var words: [String] = []
        while true {
            skipSpace()
            guard !isEmpty else { break }
            let word: String?
            if peek() == "\"" {
                word = consumeQuotedString()
            } else {
                word = consumeAtom(dot: true, permissive: true)
            }
            guard let w = word else { break }
            words.append(w)
        }
        guard !words.isEmpty else { return nil }
        return words.joined(separator: " ")
    }

    /// consumeQuotedString: the content between the quotes with
    /// quoted-pairs resolved; the opening quote is at the position.
    private mutating func consumeQuotedString() -> String? {
        var j = i + 1
        var out = String.UnicodeScalarView()
        var escaped = false
        while true {
            guard j < s.count else { return nil }
            let r = s[j]
            if escaped {
                guard isVchar(r) || isWSP(r) else { return nil }
                out.append(r)
                escaped = false
            } else if isQtext(r) || isWSP(r) {
                out.append(r)
            } else if r == "\"" {
                break
            } else if r == "\\" {
                escaped = true
            } else {
                return nil
            }
            j += 1
        }
        i = j + 1
        return String(out)
    }

    /// consumeAtom: the longest run of atext; in strict mode (an addr-spec)
    /// no leading, trailing or doubled dot.
    private mutating func consumeAtom(dot: Bool, permissive: Bool) -> String? {
        var j = i
        while j < s.count, isAtext(s[j], dot: dot, permissive: permissive) {
            j += 1
        }
        guard j > i else { return nil }
        var view = String.UnicodeScalarView()
        view.append(contentsOf: s[i..<j])
        let atom = String(view)
        i = j
        if !permissive, atom.hasPrefix(".") || atom.contains("..") || atom.hasSuffix(".") {
            return nil
        }
        return atom
    }

    private func isAtext(_ r: Unicode.Scalar, dot: Bool, permissive: Bool) -> Bool {
        switch r {
        case ".":
            return dot
        // RFC 5322 3.2.3 specials, tolerated in a display name.
        case "(", ")", "[", "]", ";", "@", "\\", ",":
            return permissive
        case "<", ">", "\"", ":":
            return false
        default:
            return isVchar(r)
        }
    }

    /// isVchar: printable US-ASCII, or anything beyond ASCII (RFC 6532).
    private func isVchar(_ r: Unicode.Scalar) -> Bool {
        (r.value >= 0x21 && r.value <= 0x7e) || r.value >= 0x80
    }

    private func isQtext(_ r: Unicode.Scalar) -> Bool {
        if r == "\\" || r == "\"" {
            return false
        }
        return isVchar(r)
    }

    private func isWSP(_ r: Unicode.Scalar) -> Bool {
        r == " " || r == "\t"
    }
}
