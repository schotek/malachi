// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Invented address books for looking at the Contacts window without a
// daemon (MALACHI_CONTACTS_SAMPLES=1, read once at launch; the dev steps of
// MALACHI_START use it): a `ContactsCaller` that answers account.list,
// addressBook.*, contact.* and contact.lookup from memory, as the daemon
// would (sorted by sort key, filtered by name, company and address, paged,
// written at once). One Microsoft 365 account has two books, a second one
// waits for Microsoft's consent, a CardDAV server has one, and Recently
// Used lists a few addresses. Nothing here is anybody's data.

import Foundation

/// The samples' daemon. An actor: the controllers call it from tasks.
public actor SampleContactsCaller: ContactsCaller {
    /// MALACHI_CONTACTS_SAMPLES=1.
    public static var requested: Bool {
        ProcessInfo.processInfo.environment["MALACHI_CONTACTS_SAMPLES"] == "1"
    }

    public static let work: AccountID = "acc_sample_work"
    public static let second: AccountID = "acc_sample_second"
    public static let cloud: AccountID = "acc_sample_cloud"
    public static let contactsBook: AddressBookID = "b_sample_contacts"
    public static let suppliers: AddressBookID = "b_sample_suppliers"
    public static let personal: AddressBookID = "b_sample_personal"
    public static let archive: AddressBookID = "b_sample_archive"

    /// The contact the dev steps open (Jana Nováková).
    public static let jana: ContactID = "p_sample_jana"

    /// Technical English of the samples' errors (`RPCError.message` is
    /// never shown).
    private static let notSampled = "not in the samples"
    private static let wrongType = "sample type"
    private static let sampleError = "sample"

    private var accounts: [Account]
    private var books: [AddressBook]
    private var cards: [ContactCard]
    private var recent: [ContactCard]
    private var next = 1

    public init(now: Date = Date()) {
        accounts = Self.sampleAccounts()
        let synced = now.addingTimeInterval(-180)
        var books = [
            AddressBook(id: Self.contactsBook, accountId: Self.work, name: "Contacts", kind: .graph, lastSyncAt: synced),
            AddressBook(id: Self.suppliers, accountId: Self.work, name: "Suppliers", kind: .graph, lastSyncAt: synced),
            AddressBook(id: Self.archive, accountId: Self.work, name: "Archive 2019", kind: .graph, visible: false, lastSyncAt: synced),
            AddressBook(id: Self.personal, accountId: Self.cloud, name: "Personal", kind: .carddav, lastSyncAt: synced),
            AddressBook(id: .recent, name: "", kind: .recent, readOnly: true),
        ]
        let cards = Self.sampleCards()
        self.cards = cards
        recent = [
            ("Pavel Novotný", "pavel@example.net"), ("", "info@example.org"), ("Lenka Malá", "lenka.mala@example.com"),
            ("Ondřej Král", "ondrej@example.cz"),
        ].enumerated().map { i, c in
            ContactCard(
                id: ContactID("r_sample_\(i)"), bookId: .recent, readOnly: true, revision: "r", displayName: c.0,
                emails: [ContactEmail(address: c.1)])
        }
        for i in books.indices {
            let id = books[i].id
            books[i].count = id == .recent ? 4 : cards.filter { $0.bookId == id }.count
        }
        self.books = books
    }

    // MARK: ContactsCaller

    public func call<M: RPCMethod>(_ method: M.Type, _ params: M.Params, timeout: Duration?) async throws -> M.Result {
        try await Task.sleep(for: .milliseconds(40))
        let result: any Sendable
        switch params {
        case is EmptyParams where M.name == API.AccountList.name:
            result = AccountListResult(accounts: accounts)
        case let p as AddressBookListParams:
            result = try listBooks(p)
        case let p as AddressBookSetParams:
            guard let i = books.firstIndex(where: { $0.id == p.bookId }) else { throw Self.error(.addressBookNotFound) }
            books[i].visible = p.visible
            result = AddressBookSetResult(book: books[i])
        case let p as ContactListParams:
            result = list(p)
        case let p as ContactGetParams:
            guard let c = card(p.contactId) else { throw Self.error(.contactNotFound) }
            result = ContactGetResult(contact: c)
        case let p as ContactLookupParams:
            result = lookup(p)
        case let p as ContactCreateParams:
            result = try create(p)
        case let p as ContactUpdateParams:
            result = try update(p)
        case let p as ContactDeleteParams:
            guard let i = cards.firstIndex(where: { $0.id == p.contactId }) else { throw Self.error(.contactNotFound) }
            let book = cards[i].bookId
            cards.remove(at: i)
            recount(book)
            result = EmptyResult()
        default:
            throw RPCError(code: .notImplemented, message: Self.notSampled)
        }
        guard let r = result as? M.Result else { throw RPCError(code: .internalError, message: Self.wrongType) }
        return r
    }

    // MARK: Methods

    private func listBooks(_ p: AddressBookListParams) throws -> AddressBookListResult {
        guard let id = p.accountId else { return AddressBookListResult(books: books) }
        if id == Self.second {
            throw RPCError(
                code: .contactsConsentRequired, message: Self.sampleError,
                data: .object(["accountId": .string(id.rawValue), "signIn": .bool(true)]))
        }
        return AddressBookListResult(books: books.filter { $0.accountId == id })
    }

    private func visibleBooks() -> Set<AddressBookID> {
        Set(books.filter { $0.visible && $0.id != .recent }.map(\.id))
    }

    private func list(_ p: ContactListParams) -> ContactListResult {
        let pool: [ContactCard]
        switch p.bookId {
        case .all:
            let shown = visibleBooks()
            pool = cards.filter { shown.contains($0.bookId) }
        case .recent:
            pool = recent
        default:
            pool = cards.filter { $0.bookId == p.bookId }
        }
        let q = (p.query ?? "").lowercased()
        let hits = pool.filter { q.isEmpty || Self.haystack($0).contains(q) }.map(Self.summary)
            .sorted { $0.sortKey.localizedStandardCompare($1.sortKey) == .orderedAscending }
        let offset = min(max(p.offset ?? 0, 0), hits.count)
        let limit = min(p.limit ?? API.Limits.defaultContactListLimit, API.Limits.maxContactListLimit)
        return ContactListResult(contacts: Array(hits[offset..<min(offset + limit, hits.count)]), total: hits.count)
    }

    private func card(_ id: ContactID) -> ContactCard? {
        cards.first { $0.id == id } ?? recent.first { $0.id == id }
    }

    private func lookup(_ p: ContactLookupParams) -> ContactLookupResult {
        let shown = visibleBooks()
        var known: [String: ContactID] = [:]
        for a in p.addresses {
            let n = a.trimmingCharacters(in: .whitespaces).lowercased()
            if let c = cards.first(where: { c in shown.contains(c.bookId) && c.emails.contains { $0.address.lowercased() == n } }) {
                known[n] = c.id
            }
        }
        return ContactLookupResult(known: known)
    }

    private func create(_ p: ContactCreateParams) throws -> ContactCreateResult {
        guard let b = books.first(where: { $0.id == p.bookId }) else { throw Self.error(.addressBookNotFound) }
        guard ContactBook.writable(b) else { throw Self.error(.addressBookReadOnly) }
        var c = p.card
        c.id = ContactID("p_sample_new_\(next)")
        c.bookId = p.bookId
        c.revision = "1"
        next += 1
        cards.append(c)
        recount(p.bookId)
        return ContactCreateResult(contactId: c.id, revision: c.revision)
    }

    private func update(_ p: ContactUpdateParams) throws -> ContactUpdateResult {
        guard let i = cards.firstIndex(where: { $0.id == p.contactId }) else { throw Self.error(.contactNotFound) }
        guard p.revision == cards[i].revision else {
            throw RPCError(code: .contactConflict, message: Self.sampleError, data: try? Self.json(ContactConflictData(server: cards[i])))
        }
        var c = p.card
        c.id = cards[i].id
        c.bookId = cards[i].bookId
        c.revision = String((Int(cards[i].revision) ?? 1) + 1)
        cards[i] = c
        return ContactUpdateResult(revision: c.revision)
    }

    private func recount(_ book: AddressBookID) {
        if let i = books.firstIndex(where: { $0.id == book }) {
            books[i].count = cards.filter { $0.bookId == book }.count
        }
    }

    // MARK: Helpers

    private static func error(_ code: ErrorCode) -> RPCError { RPCError(code: code, message: sampleError) }

    private static func json<T: Encodable>(_ v: T) throws -> JSONValue {
        try JSONCoding.decoder().decode(JSONValue.self, from: JSONCoding.encoder().encode(v))
    }

    private static func haystack(_ c: ContactCard) -> String {
        ([c.displayName, c.org, ContactBook.composeName(c.name)] + c.emails.map(\.address)).joined(separator: " ").lowercased()
    }

    /// The row as the daemon makes it: family name first in the sort key,
    /// its first letter the section.
    static func summary(_ c: ContactCard) -> ContactSummary {
        let family = c.name.family.isEmpty ? c.displayName : c.name.family
        let key = (family + " " + c.name.given).trimmingCharacters(in: .whitespaces)
        let first = key.folding(options: [.diacriticInsensitive, .caseInsensitive], locale: nil).first.map { String($0).uppercased() } ?? "#"
        let section = first.rangeOfCharacter(from: .letters) != nil ? first : "#"
        return ContactSummary(
            id: c.id, bookId: c.bookId, displayName: c.displayName.isEmpty ? ContactBook.composeName(c.name) : c.displayName,
            sortKey: key, section: section, org: c.org, email: c.emails.first?.address ?? "")
    }

    private static func sampleAccounts() -> [Account] {
        let caps: [Capability] = API.mailCapabilities + [.calendar, .contacts]
        return [
            Account(
                id: work,
                config: AccountConfig(
                    name: "Work", email: "jana@firma.example", kind: .graph,
                    graph: GraphConfig(source: .daemon, contacts: ContactsConfig(sync: true))),
                enabled: true, state: SyncState(accountId: work, status: .idle), capabilities: caps),
            Account(
                id: second,
                config: AccountConfig(
                    name: "Second", email: "petr@firma2.example", kind: .graph,
                    graph: GraphConfig(source: .daemon, contacts: ContactsConfig(sync: true))),
                enabled: true, state: SyncState(accountId: second, status: .idle), capabilities: caps),
            Account(
                id: cloud,
                config: AccountConfig(
                    name: "Nextcloud", email: "", kind: .caldav,
                    caldav: CalDAVConfig(serverUrl: "https://cloud.example.com/remote.php/dav", username: "jana", contacts: true)),
                enabled: true, state: SyncState(accountId: cloud, status: .idle), capabilities: [.calendar, .contacts]),
        ]
    }

    private static func sampleCards() -> [ContactCard] {
        // macOS-only strings: invented sample data, not for translation.
        typealias Row = (given: String, family: String, org: String, email: String, book: AddressBookID)
        let rows: [Row] = [
            ("Tomáš", "Adámek", "Stavby Novák s.r.o.", "tomas.adamek@example.cz", contactsBook),
            ("Lucie", "Andělová", "", "lucie.andelova@example.com", contactsBook),
            ("Martin", "Beneš", "Beneš Elektro s.r.o.", "martin@benes.example", suppliers),
            ("Eva", "Bílá", "", "eva.bila@example.org", personal),
            ("Jakub", "Černý", "Černý a syn", "jakub@cerny.example", suppliers),
            ("David", "Dvořák", "", "david@dvorak.example", personal),
            ("Hana", "Horáková", "Účetnictví Horáková", "hana@horakova.example", contactsBook),
            ("Karel", "Kučera", "", "karel.kucera@example.cz", contactsBook),
            ("Petra", "Malá", "Stavby Novák s.r.o.", "petra.mala@example.cz", contactsBook),
            ("Jiří", "Pokorný", "Pila Pokorný", "jiri@pokorny.example", suppliers),
            ("Karel", "Pokorný", "", "karel.pokorny@example.net", personal),
            ("Petr", "Novotný", "", "petr.novotny@example.cz", contactsBook),
            ("Lenka", "Růžičková", "", "lenka@ruzickova.example", personal),
            ("Tomáš", "Svoboda", "Pila Svoboda s.r.o.", "tomas@svoboda.example", suppliers),
            ("Eva", "Svobodová", "Svobodová účetnictví", "eva@svobodova.example", contactsBook),
            ("Petra", "Veselá", "", "petra.vesela@example.org", personal),
            ("Marek", "Zeman", "Zeman Transport", "marek@zeman.example", suppliers),
        ]
        var out = rows.enumerated().map { i, r in
            ContactCard(
                id: ContactID("p_sample_\(i)"), bookId: r.book, revision: "1",
                name: ContactName(given: r.given, family: r.family), displayName: "\(r.given) \(r.family)", org: r.org,
                emails: [ContactEmail(label: .work, address: r.email)])
        }
        out.append(ContactCard(
            id: jana, bookId: contactsBook, revision: "1", name: ContactName(given: "Jana", family: "Nováková"),
            displayName: "Jana Nováková", org: "Stavby Novák s.r.o.", title: "Vedoucí nákupu",
            emails: [ContactEmail(label: .work, address: "jana.novakova@example.cz"), ContactEmail(label: .home, address: "jana@example.org")],
            phones: [ContactPhone(label: .mobile, number: "+420 601 234 567")],
            addresses: [ContactAddress(label: .work, street: "Dlouhá 12", city: "Praha 1", postcode: "110 00")],
            urls: [ContactURL(label: .work, url: "stavbynovak.example.cz")],
            birthday: ContactBirthday(month: 3, day: 14), note: "Volat po 10. hodině."))
        return out
    }
}
