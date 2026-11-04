// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The Contacts window (W1, W5), a port of ui/internal/contactsctl
// window.go (`WindowController`): the sidebar of books (account.list and
// addressBook.list), the list of the selected row (contact.list, a page at
// a time, searched by the daemon from the first character after
// `searchDebounce`), the selected contact (kept by id across reloads), the
// consent banners of Microsoft accounts whose contacts are on but whose
// sign-in lacks the scope (addressBook.list with the account answers
// contactsConsentRequired), and the status line. notify.contactsChanged and
// notify.accountsChanged reload everything after `changeDebounce`, keeping
// the book, the search, as many rows as were shown and the selection.

import Foundation

@MainActor
public final class ContactsListController {
    /// The window's timing and paging.
    public static let searchDebounce: Duration = .milliseconds(150)
    public static let changeDebounce: Duration = .milliseconds(300)
    /// How many contacts a page asks for.
    public static let pageSize = API.Limits.defaultContactListLimit

    public let options: ContactsOptions

    // MARK: Outputs

    /// The sidebar, the list or a state changed; the view reads it anew.
    public var onChange: (@MainActor () -> Void)?
    /// The selected contact changed (nil none); the detail opens it
    /// (`ContactsDetailController.open`).
    public var onSelect: (@MainActor (ContactID?) -> Void)?
    /// Allow… of a consent banner: the window calls account.oauthStart
    /// with the account and `contacts: true` and opens the page.
    public var onSignIn: (@MainActor (AccountID) -> Void)?

    // MARK: State

    /// account.list's accounts.
    public private(set) var accounts: [Account] = []
    /// addressBook.list's books (hidden ones too).
    public private(set) var books: [AddressBook] = []
    /// The sidebar's rows.
    public private(set) var items: [ContactBook.Item] = []
    /// The sidebar's selected row.
    public private(set) var selectedBook: AddressBookID?
    /// The search.
    public private(set) var query = ""
    /// The listed contacts (the pages asked for so far).
    public private(set) var contacts: [ContactSummary] = []
    /// contact.list's total of the book and search.
    public private(set) var total = 0
    /// The selected contact.
    public private(set) var selected: ContactID?
    /// The consent banners, in account order.
    public private(set) var consent: [ContactsConsentNeed] = []
    /// The books have been listed once.
    public private(set) var loaded = false
    /// A contact.list without its answer.
    public private(set) var loading = false
    /// The last failure's sentence ("" none).
    public private(set) var error = ""

    private var listed = false
    private var refreshGen = 0
    private var listGen = 0
    private var searchGen = 0
    private var changeGen = 0
    private var closed = false

    public init(options: ContactsOptions) {
        self.options = options
    }

    /// Loads the accounts, the books and the first page.
    public func start() { refresh() }

    /// Ends the window: no answer is taken any more.
    public func stop() { closed = true }

    // MARK: Derived

    /// The list's rows: with index headings, flat while searching.
    public var rows: [ContactBook.Row] { ContactBook.rows(contacts, headings: query.isEmpty) }

    /// More contacts than listed (the list asks `loadMore` near its end).
    public var hasMore: Bool { listed && contacts.count < total }

    /// The list's subtitle ("All Contacts · 412"); "" before the first
    /// answer.
    public var subtitle: String {
        guard let it = ContactBook.find(items, selectedBook), listed else { return "" }
        return ContactBook.listSubtitle(it, total: total)
    }

    /// The status bar's line.
    public var status: String { ContactBook.statusLine(books, now: options.now()) }

    /// The list's empty state: `.search`, `.book` or `.none`.
    public var listEmpty: ContactBook.Empty {
        if !listed || loading || total > 0 {
            return .none
        }
        return query.isEmpty ? .book : .search
    }

    /// The detail's empty state without a selected contact: `.noBooks`
    /// (with Open Settings…) when only Recently Used exists, else
    /// `.selection`; `.none` with a selection or before the books.
    public var detailEmpty: ContactBook.Empty {
        if !loaded || selected != nil {
            return .none
        }
        return ContactBook.hasBooks(items) ? .selection : .noBooks
    }

    /// New Contact has a book to create in.
    public var canCreate: Bool { newContactBook != nil }

    /// The book New Contact creates in: the selected one when writable,
    /// else the default (`ContactBook.defaultBook`).
    public var newContactBook: AddressBookID? {
        ContactBook.defaultBook(books, selected: selectedBook, account: nil)
    }

    /// A book with its account (nil for Recently Used or an account
    /// account.list no longer has); nil for an unknown book. The
    /// `ContactsDetailController`'s resolver.
    public func book(_ id: AddressBookID) -> (book: AddressBook, account: Account?)? {
        guard let b = books.first(where: { $0.id == id }) else { return nil }
        return (b, accounts.first { $0.id == b.accountId })
    }

    private func changed() {
        if !closed {
            onChange?()
        }
    }

    // MARK: Loading

    /// Lists the accounts and the books, then the list of the shown book
    /// with as many rows as are shown, then the consent of accounts without
    /// books.
    private func refresh() {
        if closed {
            return
        }
        refreshGen += 1
        let gen = refreshGen
        options.call(API.AccountList.self, EmptyParams()) { [weak self] r in
            guard let self, !self.closed, gen == self.refreshGen else { return }
            if case .success(let res) = r {
                self.accounts = res.accounts
            }
            self.options.call(API.AddressBookList.self, AddressBookListParams()) { [weak self] b in
                guard let self, !self.closed, gen == self.refreshGen else { return }
                switch b {
                case .failure(let e):
                    self.error = self.options.errorMessage(e)
                    self.changed()
                case .success(let res):
                    self.error = ""
                    self.books = res.books
                    self.loaded = true
                    self.items = ContactBook.sidebar(self.books, accounts: self.accounts)
                    if ContactBook.find(self.items, self.selectedBook) == nil {
                        self.selectedBook = ContactBook.defaultSelection(self.items)
                        self.query = ""
                        self.listed = false
                    }
                    self.listFirst(max(self.contacts.count, Self.pageSize))
                    self.checkConsent()
                    self.changed()
                }
            }
        }
    }

    /// Asks for the first `limit` contacts of the book and search.
    private func listFirst(_ limit: Int) {
        listGen += 1
        let gen = listGen
        guard let book = selectedBook else {
            contacts = []
            total = 0
            listed = true
            loading = false
            keepSelection()
            return
        }
        loading = true
        let p = ContactListParams(bookId: book, query: query.isEmpty ? nil : query,
                                  limit: min(limit, API.Limits.maxContactListLimit))
        options.call(API.ContactList.self, p) { [weak self] r in
            guard let self, !self.closed, gen == self.listGen else { return }
            self.loading = false
            switch r {
            case .failure(let e):
                self.error = self.options.errorMessage(e)
            case .success(let res):
                self.error = ""
                self.contacts = res.contacts
                self.total = res.total
                self.listed = true
                self.keepSelection()
            }
            self.changed()
        }
    }

    /// Drops a selection the list no longer has.
    private func keepSelection() {
        guard let selected else { return }
        if !contacts.contains(where: { $0.id == selected }) {
            select(nil)
        }
    }

    /// Asks for the next page; nothing while a page is on its way or
    /// everything is listed.
    public func loadMore() {
        guard !loading, hasMore, let book = selectedBook else { return }
        loading = true
        let gen = listGen
        let p = ContactListParams(bookId: book, query: query.isEmpty ? nil : query, offset: contacts.count,
                                  limit: Self.pageSize)
        options.call(API.ContactList.self, p) { [weak self] r in
            guard let self, !self.closed, gen == self.listGen else { return }
            self.loading = false
            switch r {
            case .failure(let e):
                self.error = self.options.errorMessage(e)
            case .success(let res):
                let seen = Set(self.contacts.map(\.id))
                self.contacts += res.contacts.filter { !seen.contains($0.id) }
                self.total = res.total
                if res.contacts.isEmpty {
                    self.total = self.contacts.count
                }
            }
            self.changed()
        }
    }

    // MARK: Actions

    /// Shows a sidebar row; the search stays.
    public func selectBook(_ id: AddressBookID) {
        guard ContactBook.find(items, id) != nil, id != selectedBook else { return }
        selectedBook = id
        contacts = []
        total = 0
        listed = false
        listFirst(Self.pageSize)
        changed()
    }

    /// Searches: the daemon is asked after `searchDebounce`, from the
    /// first character; "" lists the book again.
    public func setQuery(_ q: String) {
        var q = ContactBook.trimSpace(q)
        if q.utf8.count > API.Limits.maxContactQueryBytes {
            q = ContactBook.clean(q, max: API.Limits.maxContactQueryBytes)
        }
        if q == query {
            return
        }
        query = q
        searchGen += 1
        let gen = searchGen
        options.after(Self.searchDebounce) { [weak self] in
            guard let self, !self.closed, gen == self.searchGen else { return }
            self.listFirst(Self.pageSize)
            self.changed()
        }
    }

    /// Selects a contact (nil none).
    public func select(_ id: ContactID?) {
        if id == selected {
            return
        }
        selected = id
        if !closed {
            onSelect?(id)
        }
    }

    /// Takes a notification of the daemon by its method:
    /// notify.contactsChanged and notify.accountsChanged reload after
    /// `changeDebounce` (several in a row once).
    public func handle(_ method: String) {
        guard method == API.Notify.contactsChanged || method == API.Notify.accountsChanged else { return }
        changeGen += 1
        let gen = changeGen
        options.after(Self.changeDebounce) { [weak self] in
            guard let self, !self.closed, gen == self.changeGen else { return }
            self.refresh()
        }
    }

    /// Loads everything again at once (after the window's own write).
    public func reload() { refresh() }

    /// Allow… of an account's banner.
    public func allowConsent(_ id: AccountID) {
        onSignIn?(id)
    }

    /// Asks each candidate's books alone; a contactsConsentRequired makes a
    /// banner, anything else drops it.
    private func checkConsent() {
        let cands = ContactBook.consentCandidates(accounts, books: books)
        consent = consent.filter { c in cands.contains { $0.id == c.accountId } }
        let gen = refreshGen
        for a in cands {
            let id = a.id
            let name = ContactBook.accountName(a)
            options.call(API.AddressBookList.self, AddressBookListParams(accountId: id)) { [weak self] r in
                guard let self, !self.closed, gen == self.refreshGen else { return }
                self.consent.removeAll { $0.accountId == id }
                if case .failure(let e) = r, ContactsOptions.asRPCError(e).code == .contactsConsentRequired {
                    let d = ContactsOptions.consentData(e)
                    self.consent.append(ContactsConsentNeed(accountId: id, account: name, signIn: d?.signIn ?? true))
                    self.sortConsent()
                }
                self.changed()
            }
        }
    }

    private func sortConsent() {
        var pos: [AccountID: Int] = [:]
        for (i, a) in accounts.enumerated() {
            pos[a.id] = i
        }
        let order = consent.enumerated().sorted { l, r in
            let a = pos[l.element.accountId] ?? 0
            let b = pos[r.element.accountId] ?? 0
            return a != b ? a < b : l.offset < r.offset
        }
        consent = order.map(\.element)
    }
}
