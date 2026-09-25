// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// ui/internal/compose/address_test.go.
struct AddressListTests {
    @Test func parseAddressList() {
        let (addrs, invalid) = AddressList.parse(
            "Alice <alice@example.invalid>, bob@example.invalid; \"Doe, Jane\" <jane@example.invalid>, Jörg Müller <j@example.invalid>,")
        #expect(invalid.isEmpty)
        #expect(addrs == [
            Address(name: "Alice", address: "alice@example.invalid"),
            Address(address: "bob@example.invalid"),
            Address(name: "Doe, Jane", address: "jane@example.invalid"),
            Address(name: "Jörg Müller", address: "j@example.invalid"),
        ])

        let (valid, bad) = AddressList.parse("foo, a@b c@d, ok@example.invalid, ,")
        #expect(valid.map(\.address) == ["ok@example.invalid"])
        #expect(bad == ["foo", "a@b c@d"])

        let empty = AddressList.parse("")
        #expect(empty.addresses.isEmpty)
        #expect(empty.invalid.isEmpty)
    }

    @Test func formatAddressList() {
        let input = [
            Address(name: "Alice", address: "alice@example.invalid"),
            Address(address: "bob@example.invalid"),
            Address(name: "Doe, Jane \"JD\"", address: "jane@example.invalid"),
        ]
        let got = AddressList.format(input)
        #expect(got == "Alice <alice@example.invalid>, bob@example.invalid, \"Doe, Jane \\\"JD\\\"\" <jane@example.invalid>")
        // Round trip.
        let (back, invalid) = AddressList.parse(got)
        #expect(invalid.isEmpty)
        #expect(back.count == 3)
        #expect(back.last?.name == "Doe, Jane \"JD\"")
        #expect(back == input)
    }

    /// The subset of net/mail.ParseAddress the parser reproduces.
    @Test func parseAddressGrammar() {
        #expect(AddressList.parseAddress("<a@b.example>") == Address(address: "a@b.example"))
        #expect(AddressList.parseAddress("\"\" <a@b.example>") == Address(address: "a@b.example"))
        #expect(AddressList.parseAddress("  a@b.example  ") == Address(address: "a@b.example"))
        #expect(AddressList.parseAddress("Two  Words <a@b.example>") == Address(name: "Two Words", address: "a@b.example"))
        #expect(AddressList.parseAddress("\"Back\\\\slash\" <a@b.example>") == Address(name: "Back\\slash", address: "a@b.example"))
        for bad in [
            "", "   ", "foo", "a@b c@d", "\"foo\"", "me@", "@example.org", "a b@example.org",
            "Name a@b.example", "a@b.example>", "<a@b.example", "a..b@x.example", ".a@x.example", "a@x.example.",
            "a@b.example (comment)", "\"unclosed <a@b.example>", "Name <a@b.example> extra", "a@b\u{01}.example",
        ] {
            #expect(AddressList.parseAddress(bad) == nil, "\(bad) accepted")
        }
    }
}
