// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// The counterpart of ui/internal/window/addresses_test.go: which addresses
// a header row shows, and the key that decides whether it is rebuilt.

/// n addresses a0@example.invalid … with names.
private func people(_ n: Int) -> [Address] {
    (0..<n).map { Address(name: "Person \($0)", address: "a\($0)@example.invalid") }
}

@Suite struct AddressChipsTests {
    @Test(arguments: [
        ("none", 0, false, 0, 0),
        ("few", 3, false, 3, 0),
        ("exactly the fold", addressChipsFolded, false, addressChipsFolded, 0),
        // "+1 more" would take the room of the chip it hides.
        ("one over", addressChipsFolded + 1, false, addressChipsFolded + 1, 0),
        ("two over", addressChipsFolded + 2, false, addressChipsFolded, 2),
        ("many", 17, false, addressChipsFolded, 17 - addressChipsFolded),
        ("many, unfolded", 17, true, 17, 0),
    ])
    func foldAddressesCases(_ name: String, _ count: Int, _ expanded: Bool, _ wantShown: Int, _ wantMore: Int) {
        let list = people(count)
        let (shown, more) = foldAddresses(list, expanded: expanded)
        #expect(shown.count == wantShown && more == wantMore, "\(name)")
        #expect(shown == Array(list.prefix(shown.count)), "\(name): the list's order")
    }

    @Test func foldAddressesSkipsBlankEntries() {
        // A group with no members, or an entry that parsed to nothing, has
        // nothing to put on a chip; a name alone or an address alone does.
        let list = [Address(address: ""), Address(name: "  ", address: ""), Address(name: "Only a name", address: ""),
                    Address(address: "only@example.invalid")]
        let (shown, more) = foldAddresses(list, expanded: false)
        #expect(shown.count == 2 && more == 0)
        #expect(shown.first?.name == "Only a name" && shown.last?.address == "only@example.invalid")
        #expect(foldAddresses([Address(address: ""), Address(address: "")], expanded: false).shown.isEmpty)
        #expect(foldAddresses(nil, expanded: false).shown.isEmpty)
    }

    @Test func addressKeyTest() {
        let base = addressKey("acc", people(2), 0)
        #expect(addressKey("acc", people(2), 0) == base, "the same row gave two keys")
        var renamed = people(2)
        renamed[1].name = "Someone else"
        let others: [(String, String)] = [
            ("account", addressKey("other", people(2), 0)),
            ("name", addressKey("acc", renamed, 0)),
            ("fold", addressKey("acc", people(2), 3)),
            ("count", addressKey("acc", people(3), 0)),
            // Name and address must not run together: "a" + "bc" is not "ab" + "c".
            ("boundary", addressKey("acc", [Address(name: "Person 0a", address: "0@example.invalid"), people(2)[1]], 0)),
        ]
        for (name, key) in others {
            #expect(key != base, "a different \(name) gave the same key")
        }
    }
}
