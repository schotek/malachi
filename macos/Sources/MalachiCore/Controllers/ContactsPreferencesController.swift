// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Settings → Contacts (W3), after Calendar, a port of
// ui/internal/contactsctl preferences.go (`PreferencesController`): a
// section per account with the contacts capability in account order — a
// Microsoft account's switch Sync Contacts (account.update with
// graph.contacts.sync; switching on when the sign-in lacks
// Contacts.ReadWrite shows the consent row with Allow…, which hands the
// account to the re-sign-in with `contacts: true`), a CardDAV account's
// switch (caldav.contacts) and note, the account's books with their
// checkboxes (addressBook.set visible) — then the group Recipient
// Suggestions (the key contacts-suggest-scope). Each change is shown at
// once and undone when the daemon refuses it.

import Foundation

@MainActor
public final class ContactsPreferencesController {
    /// contactsctl.PrefsAccount: an account's section.
    public struct Section: Sendable, Equatable {
        public var account: Account
        /// The heading (`ContactBook.accountName`) and the kind capsule.
        public var title: String
        public var badge: String
        /// The switch Sync Contacts.
        public var syncOn: Bool
        /// Contacts are on but the grant lacks the scope; `signIn` says
        /// Allow… can fix it (else `ContactBook.consentOnline`).
        public var consent = false
        public var signIn = false
        /// The account's books with their checkboxes (`visible`).
        public var books: [AddressBook] = []
        /// `ContactBook.cardDAVNote` follows the books.
        public var cardDAV: Bool
    }

    public let options: ContactsOptions
    /// Read and write contacts-suggest-scope (`Settings.contactsSuggestScope`);
    /// nil is the default, not stored.
    public var scope: (@MainActor () -> ContactSearchScope)?
    public var setScope: (@MainActor (ContactSearchScope) -> Void)?

    /// The page changed; the view reads it anew.
    public var onChange: (@MainActor () -> Void)?
    /// Allow… (account.oauthStart with the account and `contacts: true`).
    public var onSignIn: (@MainActor (AccountID) -> Void)?

    /// The first answer arrived.
    public private(set) var loaded = false
    /// The last refusal's sentence ("" none).
    public private(set) var error = ""
    /// addressBook.list's books (for Add to Contacts' default).
    public private(set) var books: [AddressBook] = []

    private var accounts: [Account] = []
    private var consent: [AccountID: ContactsConsentNeed] = [:]
    private var savingAccounts: Set<AccountID> = []
    private var gen = 0
    private var closed = false

    public init(options: ContactsOptions) {
        self.options = options
    }

    /// Loads the accounts and the books.
    public func start() { refresh() }

    /// Ends the page.
    public func stop() { closed = true }

    private func changed() {
        if !closed {
            onChange?()
        }
    }

    /// Reloads after notify.contactsChanged and notify.accountsChanged.
    public func handle(_ method: String) {
        if method == API.Notify.contactsChanged || method == API.Notify.accountsChanged {
            refresh()
        }
    }

    private func refresh() {
        if closed {
            return
        }
        gen += 1
        let gen = gen
        options.call(API.AccountList.self, EmptyParams()) { [weak self] r in
            guard let self, !self.closed, gen == self.gen else { return }
            let accounts: [Account]
            switch r {
            case .failure(let e):
                self.error = self.options.errorMessage(e)
                self.changed()
                return
            case .success(let res):
                accounts = res.accounts
            }
            self.options.call(API.AddressBookList.self, AddressBookListParams()) { [weak self] b in
                guard let self, !self.closed, gen == self.gen else { return }
                self.accounts = accounts
                if case .success(let res) = b {
                    self.books = res.books
                }
                self.loaded = true
                self.checkConsent(gen)
                self.changed()
            }
        }
    }

    private func checkConsent(_ gen: Int) {
        let cands = ContactBook.consentCandidates(accounts, books: books)
        consent = consent.filter { e in cands.contains { $0.id == e.key } }
        for a in cands {
            let id = a.id
            let name = ContactBook.accountName(a)
            options.call(API.AddressBookList.self, AddressBookListParams(accountId: id)) { [weak self] r in
                guard let self, !self.closed, gen == self.gen else { return }
                self.consent.removeValue(forKey: id)
                if case .failure(let e) = r, ContactsOptions.asRPCError(e).code == .contactsConsentRequired {
                    let d = ContactsOptions.consentData(e)
                    self.consent[id] = ContactsConsentNeed(accountId: id, account: name, signIn: d?.signIn ?? true)
                }
                self.changed()
            }
        }
    }

    /// The page's sections: the accounts with the contacts capability
    /// (graph and caldav), in account order.
    public var sections: [Section] {
        var out: [Section] = []
        for a in accounts {
            let kind = a.config.protocolKind
            guard a.can(.contacts), kind == .graph || kind == .caldav else { continue }
            let on = ContactBook.syncOn(a.config)
            var s = Section(account: a, title: ContactBook.accountName(a), badge: ContactBook.accountKindBadge(a),
                            syncOn: on, cardDAV: kind == .caldav)
            if let c = consent[a.id], on {
                s.consent = true
                s.signIn = c.signIn
            }
            if on {
                s.books = books.filter { $0.accountId == a.id }
            }
            out.append(s)
        }
        return out
    }

    /// No account that can have contacts (`ContactBook.noContactAccounts`).
    public var empty: Bool { loaded && sections.isEmpty }

    /// An account.update of the account without its answer.
    public func saving(_ id: AccountID) -> Bool { savingAccounts.contains(id) }

    /// Sync Contacts: one account.update of the whole configuration with
    /// empty credentials (the keyring keeps the secret), shown at once and
    /// undone on a refusal.
    public func setSync(_ id: AccountID, on: Bool) {
        guard let i = accounts.firstIndex(where: { $0.id == id }), !savingAccounts.contains(id),
              ContactBook.syncOn(accounts[i].config) != on, accounts[i].can(.contacts) else { return }
        let before = accounts[i].config
        let name = ContactBook.accountName(accounts[i])
        let cfg = ContactBook.withSync(before, on)
        accounts[i].config = cfg
        savingAccounts.insert(id)
        error = ""
        changed()
        let gen = gen
        options.call(API.AccountUpdate.self, AccountUpdateParams(accountId: id, config: cfg),
                     timeout: ContactsOptions.writeTimeout) { [weak self] r in
            guard let self else { return }
            self.savingAccounts.remove(id)
            if self.closed {
                return
            }
            if case .failure(let e) = r {
                if gen == self.gen, let j = self.accounts.firstIndex(where: { $0.id == id }) {
                    self.accounts[j].config = before
                }
                self.error = self.options.errorMessage(e, account: name)
                self.changed()
                return
            }
            self.refresh()
        }
    }

    /// A book's checkbox (addressBook.set), shown at once and undone on a
    /// refusal.
    public func setVisible(_ book: AddressBookID, _ on: Bool) {
        guard let i = books.firstIndex(where: { $0.id == book }), books[i].visible != on else { return }
        books[i].visible = on
        error = ""
        changed()
        options.call(API.AddressBookSet.self, AddressBookSetParams(bookId: book, visible: on)) { [weak self] r in
            guard let self, !self.closed else { return }
            let j = self.books.firstIndex { $0.id == book }
            switch r {
            case .failure(let e):
                if let j {
                    self.books[j].visible = !on
                }
                self.error = self.options.errorMessage(e)
            case .success(let res):
                if let j {
                    self.books[j] = res.book
                }
            }
            self.changed()
        }
    }

    /// Allow… of an account's consent row.
    public func allow(_ id: AccountID) {
        onSignIn?(id)
    }

    /// Recipient Suggestions' choice (the account's books by default).
    public var suggestScope: ContactSearchScope {
        guard let scope else { return .account }
        return scope() == .all ? .all : .account
    }

    /// Writes Recipient Suggestions' choice; a value outside
    /// `ContactBook.scopes` is ignored.
    public func setSuggestScope(_ s: ContactSearchScope) {
        guard ContactBook.scopes.contains(s), s != suggestScope else { return }
        setScope?(s)
        changed()
    }
}
