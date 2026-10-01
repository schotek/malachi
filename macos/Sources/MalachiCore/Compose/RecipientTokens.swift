// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/recipients/tokens.go: the model behind the To/Cc/Bcc
// fields of the compose window. Every finished address is a token (a badge
// with an ×), what is still being typed is the pending text after the last
// token. Pure logic, no AppKit: the view shows `tokens` and `pending` and
// feeds every keystroke to `setPending`. It rests on the list rules of
// `AddressList` (commas and semicolons outside quotes and angle brackets
// separate, one mailbox per entry). Email is hostile input: the text taken
// in is bounded, stripped of control characters and the token count is
// capped. Offsets are Unicode scalars, as Go's are runes.

import Foundation

public struct RecipientTokens: Equatable, Sendable {
    /// Most bytes of UTF-8 of pasted text (pending plus the paste) that are
    /// looked at; the rest of a paste is ignored. Typed and initial text is
    /// never clipped: nothing a user has in a field is lost silently.
    public static let maxInput = 64 << 10
    /// The most tokens text is split into automatically; the rest stays in
    /// `pending` verbatim. `add` and `commit` still append.
    public static let maxTokens = 1000

    /// One finished entry of the field.
    public struct Token: Equatable, Sendable {
        /// The trimmed text the token was made from.
        public let raw: String
        /// The parsed mailbox, nil when `raw` is not one.
        public let address: Address?

        init(raw: String, address: Address?) {
            self.raw = raw
            self.address = address
        }

        public var isValid: Bool { address != nil }

        /// What the badge shows: the display name, else the address; for an
        /// invalid entry its text. Direction override and isolate characters
        /// are removed so that it cannot reorder the surrounding UI.
        public var label: String {
            guard let a = address else { return RecipientTokens.stripBidi(raw) }
            let name = RecipientTokens.trim(a.name ?? "")
            return RecipientTokens.stripBidi(name.isEmpty ? a.address : name)
        }

        /// The full "Name <addr>" form, or the text of an invalid entry.
        public var tooltip: String {
            guard let a = address else { return RecipientTokens.stripBidi(raw) }
            return RecipientTokens.stripBidi(AddressList.format([a]))
        }
    }

    public private(set) var tokens: [Token]
    public private(set) var pending: String

    public init() {
        tokens = []
        pending = ""
    }

    /// Reads a whole field value: every non-empty entry becomes a token,
    /// valid or not. Nothing is pending unless `maxTokens` is reached; the
    /// text from the first entry that no longer fits stays in `pending`
    /// verbatim.
    public init(text: String) {
        self.init()
        let s = Self.sanitize(Array(text.unicodeScalars))
        let spans = Self.split(s, lineBreaks: false)
        if let i = splitInto(s, spans, capped: true) {
            pending = Self.string(Self.trimLeft(Array(s[spans[i].lowerBound...])))
        }
    }

    /// The recipients as they would be if pending were committed now,
    /// without changing the model: the addresses of the valid tokens in
    /// order, the raw text of the invalid ones (the shape of
    /// `AddressList.parse`), the non-blank pending text evaluated as
    /// `commit` would, so it may yield several entries. Unlike re-parsing
    /// `text` it never merges or re-reads tokens.
    public func resolved() -> (addresses: [Address], invalid: [String]) {
        var c = self
        c.commit()
        var addresses: [Address] = []
        var invalid: [String] = []
        for token in c.tokens {
            if let a = token.address {
                addresses.append(a)
            } else {
                invalid.append(token.raw)
            }
        }
        return (addresses, invalid)
    }

    /// Whether any token is not a mailbox.
    public var hasInvalid: Bool {
        tokens.contains { !$0.isValid }
    }

    /// The field value the rest of the app sees: valid tokens as
    /// `AddressList.format` writes them, invalid ones as typed, then the
    /// pending text unless blank, joined by ", ".
    public var text: String {
        var parts: [String] = []
        parts.reserveCapacity(tokens.count + 1)
        for token in tokens {
            if let a = token.address {
                parts.append(AddressList.format([a]))
            } else {
                parts.append(token.raw)
            }
        }
        let p = Self.trim(pending)
        if !p.isEmpty {
            parts.append(p)
        }
        return parts.joined(separator: ", ")
    }

    /// Takes the editor's full text after a keystroke. Entries completed by
    /// a separator become tokens and the rest stays pending; so does a bare
    /// address followed by whitespace ("a@b.cz "). Returns whether the
    /// tokens changed, in which case the editor must be reset to `pending`.
    @discardableResult
    public mutating func setPending(_ text: String) -> Bool {
        let s = Self.sanitize(Array(text.unicodeScalars))
        let spans = Self.split(s, lineBreaks: false)
        if spans.count > 1 {
            if let i = splitInto(s, Array(spans.dropLast()), capped: true) {
                pending = Self.string(Self.trimLeft(Array(s[spans[i].lowerBound...])))
                return true
            }
            pending = Self.string(Self.trimLeft(Array(s[spans[spans.count - 1]])))
            fold()
            return true
        }
        pending = Self.string(s)
        return fold()
    }

    /// Turns a pending bare addr-spec that ends in whitespace into a token.
    @discardableResult
    private mutating func fold() -> Bool {
        guard let last = pending.unicodeScalars.last, Self.isSpace(last), tokens.count < Self.maxTokens else {
            return false
        }
        let raw = Self.trim(pending)
        guard let a = AddressList.parseAddress(raw), (a.name ?? "").isEmpty, !raw.contains("<") else {
            return false
        }
        pending = ""
        addToken(raw, a)
        return true
    }

    /// Turns non-blank pending text into tokens, valid or not (Enter, Tab,
    /// focus loss); blank pending is just cleared. Normally pending is one
    /// entry; the rest left by `maxTokens` is split into entries and
    /// appended past the cap. Returns whether the tokens changed.
    @discardableResult
    public mutating func commit() -> Bool {
        let s = Self.sanitize(Array(pending.unicodeScalars))
        pending = ""
        let n = tokens.count
        splitInto(s, Self.split(s, lineBreaks: false), capped: false)
        return tokens.count != n
    }

    /// Appends a valid token (a suggestion picked) and clears pending.
    public mutating func add(_ address: Address) {
        pending = ""
        var a = address
        a.name = Self.trim(Self.string(Self.sanitize(Array((a.name ?? "").unicodeScalars))))
        if a.name?.isEmpty == true {
            a.name = nil
        }
        addToken(AddressList.format([a]), a)
    }

    /// Drops the token at `index`; out of range does nothing.
    public mutating func remove(at index: Int) {
        guard tokens.indices.contains(index) else { return }
        tokens.remove(at: index)
    }

    /// Commits pending, removes the token at `index` and makes its text the
    /// pending string, which it returns. Out of range changes nothing and
    /// returns the current pending text.
    @discardableResult
    public mutating func edit(at index: Int) -> String {
        guard tokens.indices.contains(index) else { return pending }
        commit()
        let token = tokens[index]
        remove(at: index)
        if let a = token.address {
            pending = AddressList.format([a])
        } else {
            pending = token.raw
        }
        return pending
    }

    /// Adds pasted text to pending. Without any separator (comma,
    /// semicolon, line break, tab) it is typed text and goes through
    /// `setPending`; otherwise every entry, the last one too, becomes a
    /// token.
    public mutating func paste(_ text: String) {
        let own = Array(pending.unicodeScalars)
        let room = max(0, Self.maxInput - Self.byteCount(own))
        let all = own + Self.cut(Array(text.unicodeScalars), room)
        let spans = Self.split(all, lineBreaks: true)
        if spans.count <= 1 {
            setPending(Self.string(all))
            return
        }
        pending = ""
        if let i = splitInto(all, spans, capped: true) {
            // Line breaks and tabs were separators: keep them as commas.
            var rest: [String] = []
            for span in spans[i...] {
                let raw = Self.trim(Self.string(Self.sanitize(Array(all[span]))))
                if !raw.isEmpty {
                    rest.append(raw)
                }
            }
            pending = rest.joined(separator: ", ")
        }
    }

    // MARK: - Internals

    /// Makes a token of every non-empty entry of `s` in `spans`. When
    /// `capped` and `maxTokens` is reached it stops and returns the index of
    /// the first entry left, whose text and everything after it the caller
    /// keeps; nil when all were taken.
    @discardableResult
    private mutating func splitInto(_ s: [Unicode.Scalar], _ spans: [Range<Int>], capped: Bool) -> Int? {
        for (i, span) in spans.enumerated() {
            let raw = Self.trim(Self.string(Self.sanitize(Array(s[span]))))
            if raw.isEmpty {
                continue
            }
            if capped && tokens.count >= Self.maxTokens {
                return i
            }
            addEntry(raw)
        }
        return nil
    }

    private mutating func addEntry(_ raw: String) {
        addToken(raw, AddressList.parseAddress(raw))
    }

    private mutating func addToken(_ raw: String, _ a: Address?) {
        tokens.append(Token(raw: raw, address: a))
    }

    /// compose.split (recipients.split): AddressList.splitRanges over scalar
    /// offsets; with `lineBreaks`, CR, LF and tab outside quotes and angle
    /// brackets separate as well.
    static func split(_ s: [Unicode.Scalar], lineBreaks: Bool) -> [Range<Int>] {
        var out: [Range<Int>] = []
        var start = 0
        var quoted = false
        var angled = false
        var escaped = false
        for (i, r) in s.enumerated() {
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
            } else if (r == "," || r == ";" || (lineBreaks && (r == "\n" || r == "\r" || r == "\t"))) && !angled {
                out.append(start..<i)
                start = i + 1
            }
        }
        out.append(start..<s.count)
        return out
    }

    private static func byteCount(_ s: [Unicode.Scalar]) -> Int {
        s.reduce(0) { $0 + UTF8.width($1) }
    }

    /// `s` limited to `n` bytes of UTF-8, on a scalar boundary.
    private static func cut(_ s: [Unicode.Scalar], _ n: Int) -> [Unicode.Scalar] {
        var used = 0
        for (i, r) in s.enumerated() {
            used += UTF8.width(r)
            if used > n {
                return Array(s[..<i])
            }
        }
        return s
    }

    /// Removes the direction override, embedding and isolate marks.
    static func stripBidi(_ s: String) -> String {
        func bidi(_ r: Unicode.Scalar) -> Bool {
            switch r.value {
            case 0x202a...0x202e, 0x2066...0x2069, 0x200e, 0x200f, 0x061c: return true
            default: return false
            }
        }
        if !s.unicodeScalars.contains(where: bidi) {
            return s
        }
        return string(s.unicodeScalars.filter { !bidi($0) })
    }

    /// Tab and line breaks become spaces; the other C0 controls, DEL and
    /// the Unicode line and paragraph separators are dropped.
    private static func sanitize(_ s: [Unicode.Scalar]) -> [Unicode.Scalar] {
        var out: [Unicode.Scalar] = []
        out.reserveCapacity(s.count)
        for r in s {
            switch r.value {
            case 0x09, 0x0a, 0x0d:
                out.append(" ")
            case 0x00..<0x20, 0x7f, 0x2028, 0x2029:
                continue
            default:
                out.append(r)
            }
        }
        return out
    }

    /// Unicode white space, listed so that the Go reference agrees.
    static func isSpace(_ r: Unicode.Scalar) -> Bool {
        switch r.value {
        case 9...13, 32, 0x85, 0xa0, 0x1680, 0x2000...0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000:
            return true
        default:
            return false
        }
    }

    static func trim(_ s: String) -> String {
        string(trimLeft(trimRight(Array(s.unicodeScalars))))
    }

    private static func trimLeft(_ s: [Unicode.Scalar]) -> [Unicode.Scalar] {
        Array(s.drop(while: isSpace))
    }

    private static func trimRight(_ s: [Unicode.Scalar]) -> [Unicode.Scalar] {
        var end = s.count
        while end > 0, isSpace(s[end - 1]) {
            end -= 1
        }
        return Array(s[..<end])
    }

    private static func string(_ s: [Unicode.Scalar]) -> String {
        var view = String.UnicodeScalarView()
        view.append(contentsOf: s)
        return String(view)
    }
}
