// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The address chips' contacts item (W2), a port of ui/internal/contactsctl
// lookup.go (`Lookup`): Show Contact for an address a visible book has
// exactly (contact.lookup, never by name), else Add to Contacts…. The
// lookup is asked once per shown message for all its header addresses
// (`key` is the message's id, or any key the view uses) and kept for the
// `kept` most recent messages; notify.contactsChanged forgets every answer
// (`invalidate`), and the chips ask again.

import Foundation

@MainActor
public final class ContactsLookupController {
    /// contactsctl.LookupKept: how many messages' answers are kept.
    public static let kept = 50

    public let options: ContactsOptions
    /// The answer for a key arrived (the chips of that message rebuild
    /// their menus).
    public var onChange: (@MainActor (String) -> Void)?

    private var answers: [String: [String: ContactID]] = [:]
    private var pending: [String: Int] = [:]
    private var partial: [String: [String: ContactID]] = [:]
    private var failed: Set<String> = []
    private var order: [String] = []
    private var gen = 0

    public init(options: ContactsOptions) {
        self.options = options
    }

    /// contactsctl.NormalAddress: trimmed, lower case (contact.lookup's
    /// keys).
    public nonisolated static func normalAddress(_ a: String) -> String {
        ContactBook.trimSpace(a).lowercased()
    }

    /// Asks for the addresses of a message once (a key answered or on its
    /// way is not asked again), at most `maxContactLookupAddresses` per
    /// call. A failed call stores nothing: not known is the safe answer,
    /// and the key is asked again next time.
    public func ask(_ key: String, addresses: [String]) {
        if answers[key] != nil || pending[key] != nil {
            return
        }
        var addrs: [String] = []
        for a in addresses {
            let n = Self.normalAddress(a)
            if !n.isEmpty && !addrs.contains(n) {
                addrs.append(n)
            }
        }
        if addrs.isEmpty {
            store(key, [:])
            return
        }
        let gen = gen
        let size = API.Limits.maxContactLookupAddresses
        let chunks = stride(from: 0, to: addrs.count, by: size).map { Array(addrs[$0..<min($0 + size, addrs.count)]) }
        pending[key] = chunks.count
        partial[key] = [:]
        failed.remove(key)
        for part in chunks {
            options.call(API.ContactLookup.self, ContactLookupParams(addresses: part)) { [weak self] r in
                guard let self, gen == self.gen else { return }
                switch r {
                case .success(let res):
                    for (a, id) in res.known {
                        self.partial[key, default: [:]][Self.normalAddress(a)] = id
                    }
                case .failure:
                    self.failed.insert(key)
                }
                let left = (self.pending[key] ?? 1) - 1
                if left > 0 {
                    self.pending[key] = left
                    return
                }
                self.pending.removeValue(forKey: key)
                let known = self.partial.removeValue(forKey: key) ?? [:]
                if self.failed.remove(key) != nil {
                    self.onChange?(key)
                    return
                }
                self.store(key, known)
            }
        }
    }

    private func store(_ key: String, _ known: [String: ContactID]) {
        answers[key] = known
        order.removeAll { $0 == key }
        order.append(key)
        while order.count > Self.kept {
            answers.removeValue(forKey: order.removeFirst())
        }
        onChange?(key)
    }

    /// The contact of an address of `key`'s message; `answered` false
    /// before the answer.
    public func known(_ key: String, address: String) -> (id: ContactID?, answered: Bool) {
        guard let m = answers[key] else { return (nil, false) }
        return (m[Self.normalAddress(address)], true)
    }

    /// The chip's contacts item for an address (`books` are the window's
    /// or the preferences' addressBook.list; empty = no book can take a new
    /// contact).
    public func item(_ key: String, address: String, books: [AddressBook]) -> (item: ContactBook.ChipItem, id: ContactID?) {
        let k = known(key, address: address)
        guard k.answered else { return (.none, nil) }
        return (ContactBook.chipItemFor(known: k.id != nil, books: books), k.id)
    }

    /// Forgets every answer (notify.contactsChanged); answers on their way
    /// are dropped.
    public func invalidate() {
        gen += 1
        answers = [:]
        pending = [:]
        partial = [:]
        failed = []
        order = []
    }
}
