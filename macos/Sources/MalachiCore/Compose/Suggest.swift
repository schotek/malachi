// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The pure part of ui/internal/compose/suggest.go: recipient completion
// finds the address token under the caret, asks the backend, and inserts
// the answer. The popover and its keys belong to the AppKit layer.
//
// Caret positions are Unicode scalar offsets, the character positions a
// GTK entry reports; Go's byte offsets are `String.Index` here.

import Foundation

/// suggestMinChars: how many characters a token needs before the backend
/// is asked (a directory book searches on its server).
public let suggestMinChars = 2

/// suggestDebounce: how long typing must pause before a search.
public let suggestDebounce: Duration = .milliseconds(150)

/// suggestLimit: how many suggestions are asked for and shown.
public let suggestLimit = 8

/// compose.tokenAt: the address token the caret is in: the run between the
/// separators `AddressList.splitRanges` honours, without the spaces and
/// tabs around it. `caret` is a scalar offset; the range is in `text`. A
/// caret past the text is the last token.
public func tokenAt(_ text: String, caret: Int) -> (token: String, range: Range<String.Index>) {
    let scalars = text.unicodeScalars
    let pos = index(atScalarOffset: caret, in: text)
    for r in AddressList.splitRanges(text) {
        if pos < r.lowerBound || pos > r.upperBound {
            continue
        }
        var s = r.lowerBound
        var e = r.upperBound
        while s < e, scalars[s] == " " || scalars[s] == "\t" {
            s = scalars.index(after: s)
        }
        while e > s, scalars[scalars.index(before: e)] == " " || scalars[scalars.index(before: e)] == "\t" {
            e = scalars.index(before: e)
        }
        return (String(scalars[s..<e]), s..<e)
    }
    return ("", text.endIndex..<text.endIndex)
}

/// compose.replaceToken: swaps `range` of `text` for the formatted
/// address. A separator and a space follow unless one is already there
/// (spaces the token had before it are dropped), and the caret (a scalar
/// offset) lands after the inserted address.
public func replaceToken(in text: String, range: Range<String.Index>, with address: Address) -> (text: String, caret: Int) {
    var insert = AddressList.format([address])
    let scalars = text.unicodeScalars
    var restStart = range.upperBound
    while restStart < scalars.endIndex, scalars[restStart] == " " || scalars[restStart] == "\t" {
        restStart = scalars.index(after: restStart)
    }
    let rest = String(scalars[restStart...])
    if let first = rest.unicodeScalars.first, first == "," || first == ";" {
        // A separator is already there.
    } else {
        insert += ", "
    }
    let head = String(scalars[..<range.lowerBound]) + insert
    return (head + rest, head.unicodeScalars.count)
}

/// compose.byteOffset in reverse: the scalar offset of `index` in `text`,
/// what a caret position is.
public func scalarOffset(of index: String.Index, in text: String) -> Int {
    text.unicodeScalars.distance(from: text.unicodeScalars.startIndex, to: index)
}

/// compose.byteOffset: the index of the scalar at `offset`, clamped to the
/// text (a negative offset is the start, one past the end is the end).
public func index(atScalarOffset offset: Int, in text: String) -> String.Index {
    let scalars = text.unicodeScalars
    if offset <= 0 {
        return scalars.startIndex
    }
    return scalars.index(scalars.startIndex, offsetBy: offset, limitedBy: scalars.endIndex) ?? scalars.endIndex
}

/// compose.suggestionIcon: the GTK icon name for a suggestion's source.
public func suggestionIcon(_ source: ContactSource) -> String {
    if source == .addressBook {
        return "x-office-address-book-symbolic"
    }
    return "document-open-recent-symbolic"
}

/// compose.suggestionTooltip: where a suggestion comes from: the address
/// book's own name when it has one.
public func suggestionTooltip(_ contact: Contact) -> String {
    if contact.source == .addressBook {
        if let book = contact.book, !book.isEmpty {
            return book
        }
        // TRANSLATORS: tooltip of a recipient suggestion that came from a
        // system address book whose name is unknown.
        return L10n.T("Address book")
    }
    // TRANSLATORS: tooltip of a recipient suggestion that is an address
    // the user has written to before.
    return L10n.T("Recently used")
}
