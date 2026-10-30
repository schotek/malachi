// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The application's address books (docs/plans/address-book.md §5): the
/// controllers' options (the daemon, or the invented books with
/// MALACHI_CONTACTS_SAMPLES=1), the address chips' lookup
/// (`ContactsLookupController`, forgotten on notify.contactsChanged), the
/// books and accounts the chips and Add to Contacts… choose from, the one
/// Contacts window, and the ways out of it into the rest of the
/// application: Write Email (a compose window), Show Mail (the main
/// window's search), Allow… (the re-sign-in with the contacts scope) and
/// Settings → Contacts. The window has no keyboard shortcut (the owner's
/// answer 5): Window ▸ Contacts and the chips' Show Contact open it.
@MainActor
final class AppContacts {
    unowned let state: AppState
    /// The application's, for the address chips (one per application, set
    /// when it is made).
    private(set) static weak var shared: AppContacts?
    let samples = SampleContactsCaller.requested
    let options: ContactsOptions
    let lookup: ContactsLookupController

    /// addressBook.list and account.list as last answered.
    private(set) var books: [AddressBook] = []
    private(set) var accounts: [Account] = []

    /// The main window's Show Mail: its search over every account with
    /// the query (Integration wires it).
    var showMail: (@MainActor (String) -> Void)?
    /// Asks for the URL of a re-sign-in with the contacts scope
    /// (`SyncController.requestSignInURL(contacts: true)`; Integration
    /// wires it).
    var signInURL: (@MainActor (AccountID) async -> SyncController.SignInURL)?

    private var tokens: [NotificationHub.Token] = []
    private var refreshGen = 0
    private var window: ContactsWindowController?

    init(state: AppState) {
        self.state = state
        let caller: any ContactsCaller = samples ? SampleContactsCaller() : state.client
        options = ContactsOptions(
            caller: caller, format: ContactBook.strftime(locale: .current), rpcText: { what, error in rpcErrorText(what, error) })
        lookup = ContactsLookupController(options: options)
        Self.shared = self
        tokens = [
            state.notifications.addContactsChanged { [weak self] n in self?.contactsChanged(n) },
            state.notifications.addAccountsChanged { [weak self] in self?.refresh() },
            state.notifications.addConnectionState { [weak self] s in
                if AppState.isConnected(s) {
                    self?.refresh()
                }
            },
        ]
        if samples {
            refresh()
        }
    }

    // MARK: Books

    /// Lists the accounts and the books again (the chips decide between
    /// Show Contact and Add to Contacts… from them).
    func refresh() {
        refreshGen += 1
        let gen = refreshGen
        let caller = options.caller
        Task { @MainActor [weak self] in
            let a = try? await caller.call(API.AccountList.self, EmptyParams(), timeout: nil)
            let b = try? await caller.call(API.AddressBookList.self, AddressBookListParams(), timeout: nil)
            guard let self, gen == self.refreshGen else { return }
            if let a {
                self.accounts = a.accounts
            }
            if let b {
                self.books = b.books
            }
        }
    }

    private func contactsChanged(_ n: ContactsChangedNotification) {
        lookup.invalidate()
        refresh()
        window?.handle(n)
    }

    /// The account label of a sentence ("Microsoft 365 (jana@…)"); "" for
    /// an account nobody knows.
    func accountLabel(_ id: AccountID?) -> String {
        guard let id, let a = accounts.first(where: { $0.id == id }) else { return "" }
        return ContactBook.accountLabel(a)
    }

    // MARK: Chips

    /// Asks contact.lookup for the addresses of a message's header once
    /// (`key` is the message's id).
    func ask(_ key: String, addresses: [String]) {
        lookup.ask(key, addresses: addresses)
    }

    /// The address chip's contacts item and the contact it shows.
    func chipItem(_ key: String, address: String) -> (item: ContactBook.ChipItem, id: ContactID?) {
        lookup.item(key, address: address, books: books)
    }

    // MARK: The window

    /// Window ▸ Contacts: the one Contacts window to the front (opened when
    /// there is none).
    @discardableResult
    func showWindow() -> ContactsWindowController {
        if let w = window {
            w.showWindow(nil)
            w.window?.makeKeyAndOrderFront(nil)
            return w
        }
        let w = ContactsWindowController(contacts: self)
        window = w
        w.onClose = { [weak self, weak w] in
            if self?.window === w {
                self?.window = nil
            }
        }
        w.showWindow(nil)
        w.window?.makeKeyAndOrderFront(nil)
        return w
    }

    /// Show Contact of a chip: the window searching the address, the
    /// contact selected once the list has it.
    func showContact(_ id: ContactID, address: String) {
        showWindow().reveal(id, address: address)
    }

    /// More Details… of Add to Contacts: the window with the whole form,
    /// prefilled.
    func newContact(_ form: ContactBook.Form) {
        showWindow().beginNew(form)
    }

    // MARK: Ways out

    /// Write Email: a new message to the address, from the contact's
    /// account when it writes mail, else the first account that does.
    func writeEmail(to address: String, name: String, account: AccountID?) {
        guard let open = state.hooks.openCompose else { return }
        let from = Capabilities.forwardFrom(accounts, from: account ?? "")?.id ?? account ?? ""
        let to = Address(name: name.isEmpty ? nil : name, address: address)
        open(ComposeParams(kind: .new, accountID: from, to: [to]))
    }

    /// Settings → Contacts.
    func openPreferences() {
        Integration.showPreferences(state).select(.contacts)
    }

    /// Allow… of a consent banner: the browser with the re-sign-in asking
    /// for the contacts scope; the daemon finishes it and its
    /// notify.accountsChanged lists the books again.
    func signIn(_ account: AccountID, toast: @escaping @MainActor (String) -> Void) {
        guard !samples, let signInURL else { return }
        Task { @MainActor in
            switch await signInURL(account) {
            case .open(let url):
                openInBrowser(url, onError: toast)
            case .failed(let text):
                toast(text)
            }
        }
    }
}

extension Integration {
    /// The Contacts window's ways into the main window: Show Mail as the
    /// search `from:address` over every account (A14, search.query as it
    /// is), Allow… as the re-sign-in with the contacts scope.
    func wireContacts() {
        state.contacts.showMail = { [weak self] query in
            guard let self, let w = self.mainWindow, !query.isEmpty else { return }
            self.state.showMainWindow()
            if w.mode != .mail {
                w.setMode(.mail)
            }
            self.list.setSearchScope(.all)
            w.setSearchField(query)
            self.list.activateSearch(query)
        }
        state.contacts.signInURL = { [weak self] id in
            guard let self else { return .failed("") }
            return await self.sync.requestSignInURL(client: self.state.client, accountId: id, fallbackURL: nil, contacts: true)
        }
    }
}
