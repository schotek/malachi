// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// ui/internal/recipients/tokens_test.go.
struct RecipientTokensTests {
    private func labels(_ t: RecipientTokens) -> [String] {
        t.tokens.map(\.label)
    }

    struct InitCase: Sendable {
        let name: String
        let input: String
        let labels: [String]
        let text: String
        let invalid: Bool
    }

    static let initCases: [InitCase] = [
        InitCase(name: "empty", input: "", labels: [], text: "", invalid: false),
        InitCase(name: "blank", input: " \t ", labels: [], text: "", invalid: false),
        InitCase(name: "plain", input: "a@b.cz, c@d.cz", labels: ["a@b.cz", "c@d.cz"], text: "a@b.cz, c@d.cz", invalid: false),
        InitCase(name: "semicolons", input: "a@b.cz; c@d.cz;", labels: ["a@b.cz", "c@d.cz"], text: "a@b.cz, c@d.cz", invalid: false),
        InitCase(name: "quoted comma", input: "\"Doe, John\" <j@x.cz>, a@b.cz", labels: ["Doe, John", "a@b.cz"], text: "\"Doe, John\" <j@x.cz>, a@b.cz", invalid: false),
        InitCase(name: "name", input: "Jörg Müller <j@x.cz>", labels: ["Jörg Müller"], text: "Jörg Müller <j@x.cz>", invalid: false),
        InitCase(name: "bare name is invalid", input: "Radek, a@b.cz", labels: ["Radek", "a@b.cz"], text: "Radek, a@b.cz", invalid: true),
        InitCase(name: "unclosed angle swallows the rest", input: "Radek <x@y.cz, a@b.cz", labels: ["Radek <x@y.cz, a@b.cz"], text: "Radek <x@y.cz, a@b.cz", invalid: true),
        InitCase(name: "unclosed quote swallows the rest", input: "\"Radek <x@y.cz>, a@b.cz", labels: ["\"Radek <x@y.cz>, a@b.cz"], text: "\"Radek <x@y.cz>, a@b.cz", invalid: true),
        InitCase(name: "empty entries", input: ", ,a@b.cz,,", labels: ["a@b.cz"], text: "a@b.cz", invalid: false),
        InitCase(name: "controls", input: "a@b.cz\u{0}\u{7}, c@\u{1f}d.cz\u{2028}", labels: ["a@b.cz", "c@d.cz"], text: "a@b.cz, c@d.cz", invalid: false),
        InitCase(name: "rtl override", input: "\u{202e}evil <a@b.cz>", labels: ["evil"], text: "\u{202e}evil <a@b.cz>", invalid: false),
    ]

    @Test(arguments: initCases)
    func initFromText(_ c: InitCase) {
        let tk = RecipientTokens(text: c.input)
        #expect(labels(tk) == c.labels)
        #expect(tk.text == c.text)
        #expect(tk.hasInvalid == c.invalid)
        #expect(tk.pending == "")
    }

    @Test func tokenParts() {
        let tk = RecipientTokens(text: "\"Doe, John\" <j@x.cz>, a@b.cz, nobody")
        #expect(tk.tokens.count == 3)
        #expect(tk.tokens[0].label == "Doe, John")
        #expect(tk.tokens[0].tooltip == "\"Doe, John\" <j@x.cz>")
        #expect(tk.tokens[0].isValid)
        #expect(tk.tokens[1].label == "a@b.cz")
        #expect(tk.tokens[1].tooltip == "a@b.cz")
        #expect(!tk.tokens[2].isValid)
        #expect(tk.tokens[2].label == "nobody")
        #expect(tk.tokens[2].tooltip == "nobody")
        #expect(tk.tokens[2].raw == "nobody")
        #expect(tk.tokens[2].address == nil)
    }

    @Test func roundTrip() {
        let list = [
            Address(name: "Alice", address: "alice@example.invalid"),
            Address(address: "bob@example.invalid"),
            Address(name: "Doe, Jane \"JD\"", address: "jane@example.invalid"),
            Address(name: "Jörg; Müller", address: "j@example.invalid"),
        ]
        let text = AddressList.format(list)
        let tk = RecipientTokens(text: text)
        #expect(tk.text == text)
        #expect(tk.tokens.map(\.address) == list)
        let (back, invalid) = AddressList.parse(tk.text)
        #expect(back == list)
        #expect(invalid.isEmpty)
    }

    struct PendingCase: Sendable {
        let name: String
        let input: String
        let changed: Bool
        let labels: [String]
        let pending: String
    }

    static let pendingCases: [PendingCase] = [
        PendingCase(name: "typing", input: "Rad", changed: false, labels: [], pending: "Rad"),
        PendingCase(name: "comma completes", input: "a@b.cz,", changed: true, labels: ["a@b.cz"], pending: ""),
        PendingCase(name: "comma keeps the rest", input: "a@b.cz, c@d", changed: true, labels: ["a@b.cz"], pending: "c@d"),
        PendingCase(name: "semicolon", input: "a@b.cz; c@d", changed: true, labels: ["a@b.cz"], pending: "c@d"),
        PendingCase(name: "several", input: "a@b.cz, c@d.cz, e", changed: true, labels: ["a@b.cz", "c@d.cz"], pending: "e"),
        PendingCase(name: "quoted comma waits", input: "\"Doe, Jo", changed: false, labels: [], pending: "\"Doe, Jo"),
        PendingCase(name: "quoted comma then address", input: "\"Doe, John\" <j@x.cz>,", changed: true, labels: ["Doe, John"], pending: ""),
        PendingCase(name: "angle comma waits", input: "Radek <x@y.cz, ", changed: false, labels: [], pending: "Radek <x@y.cz, "),
        PendingCase(name: "bare address then space", input: "bohmova@satomar.cz ", changed: true, labels: ["bohmova@satomar.cz"], pending: ""),
        PendingCase(name: "bare address then tab", input: "bohmova@satomar.cz\t", changed: true, labels: ["bohmova@satomar.cz"], pending: ""),
        PendingCase(name: "bare address no space", input: "bohmova@satomar.cz", changed: false, labels: [], pending: "bohmova@satomar.cz"),
        PendingCase(name: "name then space", input: "Radek ", changed: false, labels: [], pending: "Radek "),
        PendingCase(name: "half typed angle", input: "Radek Bábíček <x@y.cz ", changed: false, labels: [], pending: "Radek Bábíček <x@y.cz "),
        PendingCase(name: "closed angle then space is not bare", input: "Radek <x@y.cz> ", changed: false, labels: [], pending: "Radek <x@y.cz> "),
        PendingCase(name: "bare angle then space is not bare", input: "<x@y.cz> ", changed: false, labels: [], pending: "<x@y.cz> "),
        PendingCase(name: "invalid entry before comma", input: "foo, a", changed: true, labels: ["foo"], pending: "a"),
        PendingCase(name: "separator only", input: ",", changed: true, labels: [], pending: ""),
        PendingCase(name: "newline is a space", input: "a@b.cz\n", changed: true, labels: ["a@b.cz"], pending: ""),
        PendingCase(name: "controls dropped", input: "a\u{0}b", changed: false, labels: [], pending: "ab"),
    ]

    @Test(arguments: pendingCases)
    func setPending(_ c: PendingCase) {
        var tk = RecipientTokens()
        let changed = tk.setPending(c.input)
        #expect(changed == c.changed)
        #expect(labels(tk) == c.labels)
        #expect(tk.pending == c.pending)
    }

    @Test func setPendingKeepsEarlierTokens() {
        var tk = RecipientTokens(text: "a@b.cz")
        tk.setPending("c@d.cz, e")
        #expect(labels(tk) == ["a@b.cz", "c@d.cz"])
        #expect(tk.pending == "e")
        #expect(tk.text == "a@b.cz, c@d.cz, e")
        // Blank pending is not part of the value.
        tk.setPending("   ")
        #expect(tk.text == "a@b.cz, c@d.cz")
    }

    @Test func commit() {
        var tk = RecipientTokens()
        tk.setPending("Radek")
        var changed = tk.commit()
        #expect(changed)
        #expect(tk.pending == "")
        #expect(!tk.tokens[0].isValid)
        #expect(tk.hasInvalid)
        tk.setPending("a@b.cz")
        changed = tk.commit()
        #expect(changed)
        #expect(tk.tokens.count == 2)
        #expect(tk.tokens[1].isValid)
        tk.setPending("   ")
        changed = tk.commit()
        #expect(!changed)
        #expect(tk.pending == "")
        #expect(tk.tokens.count == 2)
        changed = tk.commit()
        #expect(!changed)
    }

    @Test func add() {
        var tk = RecipientTokens()
        tk.setPending("Ra")
        tk.add(Address(name: "Radek B.", address: "r@x.cz"))
        tk.add(Address(address: "r@x.cz"))
        tk.add(Address(address: "r@x.cz"))
        #expect(tk.pending == "")
        #expect(labels(tk) == ["Radek B.", "r@x.cz", "r@x.cz"])
        #expect(tk.text == "Radek B. <r@x.cz>, r@x.cz, r@x.cz")
    }

    @Test func remove() {
        var tk = RecipientTokens(text: "a@b.cz, c@d.cz, e@f.cz")
        tk.remove(at: 1)
        #expect(labels(tk) == ["a@b.cz", "e@f.cz"])
        for i in [-1, 2, 99] {
            tk.remove(at: i)
            #expect(tk.tokens.count == 2)
        }
    }

    @Test func edit() {
        var tk = RecipientTokens(text: "\"Doe, John\" <j@x.cz>, a@b.cz, nobody")
        tk.setPending("typed")
        var got = tk.edit(at: 0)
        #expect(got == "\"Doe, John\" <j@x.cz>")
        // Pending was committed first, the edited token left the list.
        #expect(labels(tk) == ["a@b.cz", "nobody", "typed"])
        #expect(tk.pending == "\"Doe, John\" <j@x.cz>")
        got = tk.edit(at: 1)
        #expect(got == "nobody")
        #expect(tk.pending == "nobody")
        // Out of range: nothing changes, not even the pending text.
        let before = tk.tokens.count
        for i in [-1, before, 99] {
            got = tk.edit(at: i)
            #expect(got == "nobody")
            #expect(tk.tokens.count == before)
        }
    }

    struct PasteCase: Sendable {
        let name: String
        let pending: String
        let input: String
        let labels: [String]
        let pending2: String
    }

    static let pasteCases: [PasteCase] = [
        PasteCase(name: "no separator is typing", pending: "", input: "Rad", labels: [], pending2: "Rad"),
        PasteCase(name: "appends to pending", pending: "Ra", input: "dek", labels: [], pending2: "Radek"),
        PasteCase(name: "bare address and space", pending: "", input: "a@b.cz ", labels: ["a@b.cz"], pending2: ""),
        PasteCase(name: "commas", pending: "", input: "a@b.cz, c@d.cz", labels: ["a@b.cz", "c@d.cz"], pending2: ""),
        PasteCase(name: "semicolons", pending: "", input: "a@b.cz; c@d.cz; e@f.cz", labels: ["a@b.cz", "c@d.cz", "e@f.cz"], pending2: ""),
        PasteCase(name: "newlines", pending: "", input: "a@b.cz\nc@d.cz\r\ne@f.cz\n", labels: ["a@b.cz", "c@d.cz", "e@f.cz"], pending2: ""),
        PasteCase(name: "tabs", pending: "", input: "a@b.cz\tc@d.cz", labels: ["a@b.cz", "c@d.cz"], pending2: ""),
        PasteCase(name: "last entry becomes a token", pending: "", input: "a@b.cz, Radek", labels: ["a@b.cz", "Radek"], pending2: ""),
        PasteCase(name: "pending joins the first entry", pending: "x", input: "@y.cz, c@d.cz", labels: ["x@y.cz", "c@d.cz"], pending2: ""),
        PasteCase(name: "quoted comma", pending: "", input: "\"Doe, John\" <j@x.cz>; a@b.cz", labels: ["Doe, John", "a@b.cz"], pending2: ""),
        PasteCase(name: "quoted newline stays", pending: "", input: "\"Doe,\nJohn\" <j@x.cz>\na@b.cz", labels: ["Doe, John", "a@b.cz"], pending2: ""),
        PasteCase(name: "unclosed angle is no separator", pending: "", input: "Radek <x@y.cz, a@b.cz", labels: [], pending2: "Radek <x@y.cz, a@b.cz"),
        PasteCase(name: "whitespace only", pending: "", input: " \n\t ", labels: [], pending2: ""),
        PasteCase(name: "controls", pending: "", input: "a@b.cz,\u{0}c@d.cz\u{1b}\u{2029}", labels: ["a@b.cz", "c@d.cz"], pending2: ""),
    ]

    @Test(arguments: pasteCases)
    func paste(_ c: PasteCase) {
        var tk = RecipientTokens()
        tk.setPending(c.pending)
        tk.paste(c.input)
        #expect(labels(tk) == c.labels)
        #expect(tk.pending == c.pending2)
    }

    @Test func hostileSizes() {
        // 5000 addresses: maxTokens become tokens, the rest stays pending.
        var lines = ""
        for i in 0..<5000 {
            lines += "u\(i)@x.cz\n"
        }
        var tk = RecipientTokens()
        tk.paste(lines)
        #expect(tk.tokens.count == RecipientTokens.maxTokens)
        #expect(tk.tokens[0].raw == "u0@x.cz")
        #expect(tk.tokens[RecipientTokens.maxTokens - 1].raw == "u999@x.cz")
        #expect(tk.pending.hasPrefix("u1000@x.cz, u1001@x.cz"))

        let commaed = String(lines.dropLast()).replacingOccurrences(of: "\n", with: ",")
        let tk2 = RecipientTokens(text: commaed)
        #expect(tk2.tokens.count == RecipientTokens.maxTokens)
        #expect(tk2.pending.hasPrefix("u1000@x.cz,u1001@x.cz"))
        let r = tk2.resolved()
        #expect(r.addresses.count == 5000 && r.invalid.isEmpty)
        #expect(tk2.tokens.count == RecipientTokens.maxTokens)
        #expect(tk2.text.components(separatedBy: "@").count - 1 == 5000)

        // Typing past the cap keeps the rest pending too.
        var st = RecipientTokens()
        st.setPending(commaed + ",")
        #expect(st.tokens.count == RecipientTokens.maxTokens && !st.pending.isEmpty)
        #expect(st.resolved().addresses.count == 5000)
        // commit and add append beyond the cap.
        let committed = st.commit()
        #expect(committed)
        #expect(st.tokens.count == 5000 && st.pending.isEmpty)
        st.add(Address(name: nil, address: "z@x.cz"))
        #expect(st.tokens.count == 5001)

        // 1 MB without a separator: paste is cut at 64 KiB.
        var big = RecipientTokens()
        big.paste(String(repeating: "a", count: 1 << 20))
        #expect(big.pending.utf8.count == RecipientTokens.maxInput)
        #expect(big.tokens.isEmpty)
        // Multi-byte paste is cut on a scalar boundary.
        var multi = RecipientTokens()
        multi.paste(String(repeating: "é", count: 1 << 20))
        #expect(multi.pending.utf8.count == RecipientTokens.maxInput)
        // Typed and initial text are never clipped.
        var typed = RecipientTokens()
        typed.setPending(String(repeating: "é", count: 1 << 20))
        #expect(typed.pending.utf8.count == 2 << 20)
        #expect(RecipientTokens(text: String(repeating: "a", count: 1 << 20)).tokens[0].raw.utf8.count == 1 << 20)
        // 1 MB of addresses pasted: input beyond 64 KiB is ignored.
        var long = RecipientTokens()
        long.paste(String(repeating: "a@b.cz,", count: 1 << 17))
        #expect(long.tokens.count >= 1 && long.tokens.count <= RecipientTokens.maxTokens)
    }

    @Test func resolved() {
        // A colon in a name: a valid token that does not read back from text.
        var tk = RecipientTokens(text: "\"ACME: Support\" <x@y.cz>, nobody")
        var r = tk.resolved()
        #expect(r.addresses == [Address(name: "ACME: Support", address: "x@y.cz")])
        #expect(r.invalid == ["nobody"])

        // Two invalid tokens whose joined text would read as one address.
        tk = RecipientTokens()
        tk.setPending("\"Joe")
        tk.commit()
        tk.setPending("a\" <b@c.cz>")
        tk.commit()
        r = tk.resolved()
        #expect(r.addresses.isEmpty)
        #expect(r.invalid == ["\"Joe", "a\" <b@c.cz>"])

        // Pending is evaluated like commit, without changing the model.
        tk = RecipientTokens(text: "a@b.cz")
        tk.setPending("c@d.cz")
        r = tk.resolved()
        #expect(r.addresses.count == 2 && r.addresses[1].address == "c@d.cz" && r.invalid.isEmpty)
        #expect(tk.pending == "c@d.cz" && tk.tokens.count == 1)
        // Blank pending adds nothing.
        tk.setPending("  ")
        #expect(tk.resolved().addresses.count == 1)
        // Pending with separators (left by the token cap) gives several entries.
        var cap = RecipientTokens(text: String(repeating: "u@x.cz,", count: 1000) + "a@b.cz, junk, c@d.cz")
        #expect(cap.tokens.count == RecipientTokens.maxTokens)
        r = cap.resolved()
        #expect(r.addresses.count == 1002 && r.invalid == ["junk"])
        cap.commit()
        #expect(cap.tokens.count == 1003)
    }

    @Test func bidiStripped() {
        let bidi = String(String.UnicodeScalarView([0x202a, 0x202e, 0x2066, 0x2069, 0x200e, 0x200f, 0x061c].compactMap(Unicode.Scalar.init)))
        let tk = RecipientTokens(text: bidi + "Eve" + bidi + " <e@x.cz>, " + bidi + "junk")
        #expect(tk.tokens[0].label == "Eve")
        #expect(tk.tokens[0].tooltip == "Eve <e@x.cz>")
        #expect(tk.tokens[1].label == "junk" && tk.tokens[1].tooltip == "junk")
        #expect(tk.tokens[0].address?.name != "Eve" && tk.tokens[1].raw != "junk")
    }
}
