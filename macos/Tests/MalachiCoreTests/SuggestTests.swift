// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

private let alice = Address(name: "Alice Example", address: "alice@example.org")
private let quoted = Address(name: "Novák, Jan", address: "jan@example.cz")
private let bare = Address(address: "bob@example.org")

/// ui/internal/compose/suggest_test.go. Go's byte offsets are String
/// indices here; the caret stays a character (Unicode scalar) offset.
struct SuggestTests {
    @Test(arguments: [
        ("", 0, ""),
        ("al", 2, "al"),
        ("al", 1, "al"),
        ("al", 0, "al"),
        ("bob@example.org, al", 19, "al"),
        ("bob@example.org, al", 17, "al"),
        ("bob@example.org, al", 16, "al"), // in the space before the token: still that token
        ("bob@example.org, al", 15, "bob@example.org"),
        ("bob@example.org, al", 3, "bob@example.org"),
        ("al, bob@example.org", 2, "al"),
        ("al, bob@example.org", 5, "bob@example.org"),
        ("a;b", 2, "b"),
        ("\"Nov, Jan\" <jan@example.cz>, al", 6, "\"Nov, Jan\" <jan@example.cz>"), // comma in quotes
        ("Nov <a,b@example.cz>, al", 7, "Nov <a,b@example.cz>"), // comma in brackets
        ("Jan Novák, no", 11, "no"), // multi-byte before the caret
        ("Jan Novák", 9, "Jan Novák"),
        ("al", 99, "al"), // caret past the end
        ("  al  ", 3, "al"),
    ])
    func tokenAtCases(_ text: String, _ caret: Int, _ want: String) {
        let (token, range) = tokenAt(text, caret: caret)
        #expect(token == want)
        #expect(String(text.unicodeScalars[range]) == token)
    }

    @Test(arguments: [
        ("al", 2, alice, "Alice Example <alice@example.org>, ", "Alice Example <alice@example.org>, "),
        ("bob@example.org, al", 19, alice, "bob@example.org, Alice Example <alice@example.org>, ", "bob@example.org, Alice Example <alice@example.org>, "),
        ("al, bob@example.org", 2, alice, "Alice Example <alice@example.org>, bob@example.org", "Alice Example <alice@example.org>"),
        ("al , bob@example.org", 2, alice, "Alice Example <alice@example.org>, bob@example.org", "Alice Example <alice@example.org>"),
        // Without a separator the whole segment is the token, as the
        // parser would read it.
        ("al bob@example.org", 2, alice, "Alice Example <alice@example.org>, ", "Alice Example <alice@example.org>, "),
        ("no", 2, quoted, "\"Novák, Jan\" <jan@example.cz>, ", "\"Novák, Jan\" <jan@example.cz>, "),
        ("bo", 2, bare, "bob@example.org, ", "bob@example.org, "),
        ("Novák, bo", 9, bare, "Novák, bob@example.org, ", "Novák, bob@example.org, "),
    ])
    func replaceTokenCases(_ text: String, _ caret: Int, _ address: Address, _ want: String, _ after: String) {
        let (_, range) = tokenAt(text, caret: caret)
        let (got, newCaret) = replaceToken(in: text, range: range, with: address)
        #expect(got == want)
        // The caret is a character offset that lands after the inserted address.
        #expect(newCaret == after.unicodeScalars.count)
        let idx = index(atScalarOffset: newCaret, in: got)
        #expect(got.utf8.distance(from: got.utf8.startIndex, to: idx) == after.utf8.count)
    }

    /// TestByteOffset: a character offset to a byte offset, clamped.
    @Test func scalarOffsetsMapToBytesAndClamp() {
        let text = "Novák, x"
        for (chars, wantBytes) in [0: 0, 3: 3, 4: 5, 5: 6, 8: 9, 20: 9, -1: 0] {
            let idx = index(atScalarOffset: chars, in: text)
            #expect(text.utf8.distance(from: text.utf8.startIndex, to: idx) == wantBytes, "chars \(chars)")
        }
        for chars in 0...8 {
            #expect(scalarOffset(of: index(atScalarOffset: chars, in: text), in: text) == chars)
        }
        #expect(scalarOffset(of: text.endIndex, in: text) == 8)
        #expect(index(atScalarOffset: 20, in: text) == text.endIndex)
        #expect(index(atScalarOffset: -1, in: text) == text.startIndex)
    }

    @Test func suggestionIconAndTooltip() {
        let book = Contact(address: "a@x.example", source: .addressBook, book: "Contacts")
        let unnamed = Contact(address: "a@x.example", source: .addressBook)
        let sent = Contact(address: "a@x.example", source: .sent)
        #expect(suggestionIcon(book.source) != suggestionIcon(sent.source))
        #expect(suggestionIcon(.addressBook) == "x-office-address-book-symbolic")
        #expect(suggestionIcon(.sent) == "document-open-recent-symbolic")
        #expect(suggestionTooltip(book) == "Contacts")
        #expect(!suggestionTooltip(unnamed).isEmpty)
        #expect(!suggestionTooltip(sent).isEmpty)
        #expect(suggestionTooltip(unnamed) != suggestionTooltip(sent))
        #expect(suggestionTooltip(Contact(address: "a@x.example", source: .addressBook, book: "")) == suggestionTooltip(unnamed))
    }

    @Test func splitAddressRanges() {
        let text = "a@x, \"b,c\" <b@x>;d@x"
        let got = AddressList.splitRanges(text).map { String(text.unicodeScalars[$0]) }
        #expect(got == ["a@x", " \"b,c\" <b@x>", "d@x"])
        let empty = AddressList.splitRanges("")
        #expect(empty.count == 1)
        #expect(empty[0].isEmpty)
        #expect(scalarOffset(of: empty[0].lowerBound, in: "") == 0)
    }

    @Test func constants() {
        #expect(suggestMinChars == 2)
        #expect(suggestDebounce == .milliseconds(150))
        #expect(suggestLimit == 8)
    }
}
