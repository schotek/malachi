// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// ui/internal/contacts add.go: Add to Contacts… of an address chip (W4,
// A12): a popover with the name and the address of the header prefilled —
// the name selected and `senderNameWarning` under it, because the sender
// wrote it — and the book to save in. Nothing is saved before Add; More
// Details… opens the whole form with the same values. Show Contact is
// offered only for an address contact.lookup knows exactly, never by name.

extension ContactBook {
    /// contacts.BookChoice: a book in a choice of books.
    public struct BookChoice: Sendable, Equatable {
        public var book: AddressBookID
        /// `bookChoiceText`.
        public var text: String

        public init(book: AddressBookID, text: String) {
            self.book = book
            self.text = text
        }
    }

    /// contacts.BookChoices: the books a new contact can be created in
    /// (`writable`), in the order of `books`, each with its account.
    public static func bookChoices(_ books: [AddressBook], accounts: [Account]) -> [BookChoice] {
        var names: [AccountID: String] = [:]
        for a in accounts {
            names[a.id] = accountName(a)
        }
        return books.filter(writable).map { b in
            BookChoice(book: b.id, text: bookChoiceText(bookName(b), account: b.accountId.flatMap { names[$0] } ?? ""))
        }
    }

    /// contacts.DefaultBook: the book a new contact goes to: `selected`
    /// when it is writable (New Contact in a book), else the first visible
    /// writable book of `account` (the message's account for Add to
    /// Contacts…), else the first visible writable book, else the first
    /// writable one; nil none.
    public static func defaultBook(_ books: [AddressBook], selected: AddressBookID?, account: AccountID?) -> AddressBookID? {
        if let selected, let b = books.first(where: { $0.id == selected && writable($0) }) {
            return b.id
        }
        func pick(_ ok: (AddressBook) -> Bool) -> AddressBookID? {
            books.first { writable($0) && ok($0) }?.id
        }
        if let account, !account.rawValue.isEmpty, let id = pick({ $0.visible && $0.accountId == account }) {
            return id
        }
        if let id = pick({ $0.visible }) {
            return id
        }
        return pick { _ in true }
    }

    /// contacts.AddFromChip: the state of Add to Contacts….
    public struct AddFromChip: Sendable, Equatable {
        public var name = ""
        public var address = ""
        /// The name field starts selected (a name came from the header).
        public var selectName = false
        /// `senderNameWarning` when the name came from the header ("" else).
        public var warning = ""
        public var books: [BookChoice] = []
        public var book: AddressBookID?

        /// contacts.NewAddFromChip: prefilled for an address of a header of
        /// a message of `account`.
        public init(address addr: Address, account: AccountID?, books: [AddressBook], accounts: [Account]) {
            name = clean(addr.name ?? "", max: API.Limits.maxContactNameBytes)
            address = clean(addr.address, max: API.Limits.maxContactFieldBytes)
            self.books = bookChoices(books, accounts: accounts)
            book = defaultBook(books, selected: nil, account: account)
            if name.lowercased() == address.lowercased() {
                name = ""
            }
            if !name.isEmpty {
                selectName = true
                warning = senderNameWarning
            }
        }

        /// contacts.AddFromChip.CanAdd: a book and a valid address.
        public var canAdd: Bool { book != nil && validEmail(trimSpace(address)) }

        /// contacts.AddFromChip.Form: the form Add saves and More Details…
        /// opens: a new contact in the chosen book with the name split and
        /// the address without a label.
        public var form: Form {
            var f = Form.new(book: book)
            (f.given, f.family) = splitName(name)
            let addr = trimSpace(address)
            if !addr.isEmpty {
                f.emails = [LabelledValue(value: addr)]
            }
            return f
        }
    }

    /// contacts.ChipItem: the address chip's contacts item.
    public enum ChipItem: Int, Sendable {
        /// Nothing (no book can be written and none knows the address, or
        /// the lookup has not answered).
        case none
        /// Show Contact (contact.lookup knows the address).
        case show
        /// Add to Contacts… (a writable book exists).
        case add
    }

    /// contacts.ChipItemFor: Show Contact for a known address, else Add to
    /// Contacts… when some book can take it.
    public static func chipItemFor(known: Bool, books: [AddressBook]) -> ChipItem {
        if known {
            return .show
        }
        return books.contains(where: writable) ? .add : .none
    }

    /// contacts.ChipItemText ("" for `.none`).
    public static func chipItemText(_ i: ChipItem) -> String {
        switch i {
        case .show: return showContact
        case .add: return addToContacts
        case .none: return ""
        }
    }
}
