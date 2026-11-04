// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// The pure helpers behind the address chips above a message
// (ui/internal/window/addresses.go): which addresses a From, To or Cc row
// shows, and a key that says whether a row has to be rebuilt. Names and
// addresses are server data and are shown as plain text.

/// How many chips a row shows while folded. A fold that would hide a
/// single address shows it instead: "+1 more" takes the room of the chip
/// it hides (addresses.go `addressChipsFolded`).
public let addressChipsFolded = 5

/// Caps the name on a chip, in characters; the tooltip has all of it
/// (addresses.go `addressNameChars`).
public let addressNameChars = 28

/// What a row shows (addresses.go `foldAddresses`): the addresses with
/// something to display, all of them when `expanded` or when at most one
/// would fold away, otherwise the first `addressChipsFolded` and how many
/// more there are.
public func foldAddresses(_ list: [Address]?, expanded: Bool) -> (shown: [Address], more: Int) {
    let shown = (list ?? []).filter { !displayName($0).isEmpty }
    if expanded || shown.count <= addressChipsFolded + 1 {
        return (shown, 0)
    }
    return (Array(shown.prefix(addressChipsFolded)), shown.count - addressChipsFolded)
}

/// Identifies what a row shows (addresses.go `addressKey`): the account
/// the chips write from, each name and address, and the fold. A render
/// whose key is unchanged leaves the row, and a menu open on it, alone.
public func addressKey(_ account: AccountID, _ shown: [Address], _ more: Int) -> String {
    var key = account.rawValue
    for a in shown {
        key += "\u{0}" + (a.name ?? "") + "\u{1}" + a.address
    }
    key += "\u{0}\(more)"
    return key
}
