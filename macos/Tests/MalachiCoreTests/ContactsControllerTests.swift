// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/contactsctl window_test.go, contact_test.go and
// prefs_lookup_test.go, ported: the contacts controllers against a fake
// daemon whose answers come back on the main actor (`settle` waits for every
// call and question to end, as the Go tests' loop.run) and a timer the test
// fires itself (loop.fire). Fictional people only.

// MARK: - The fake daemon and timer (fake_test.go)

private struct HarnessTimeout: Error {}

/// The daemon: a handler per method over the JSON of the params; every
/// call is recorded.
private final class FakeContactsDaemon: ContactsCaller, @unchecked Sendable {
    typealias Handler = @Sendable (Data) throws -> Data

    private let lock = NSLock()
    private var handlers: [String: Handler] = [:]
    private var calls: [(String, Data)] = []

    func on(_ method: String, _ h: @escaping Handler) {
        lock.withLock { handlers[method] = h }
    }

    func answer<T: Encodable & Sendable>(_ method: String, _ v: T) {
        let data = (try? JSONCoding.encoder().encode(v)) ?? Data()
        on(method) { _ in data }
    }

    func fail(_ method: String, _ e: RPCError) {
        on(method) { _ in throw e }
    }

    func count(_ method: String) -> Int {
        lock.withLock { calls.filter { $0.0 == method }.count }
    }

    func last<T: Decodable>(_ method: String, as type: T.Type) throws -> T {
        guard let d = lock.withLock({ calls.last { $0.0 == method }?.1 }) else { throw HarnessTimeout() }
        return try JSONCoding.decoder().decode(type, from: d)
    }

    func call<M: RPCMethod>(_ method: M.Type, _ params: M.Params, timeout: Duration?) async throws -> M.Result {
        let raw = try JSONCoding.encoder().encode(params)
        let h = lock.withLock { () -> Handler? in
            calls.append((M.name, raw))
            return handlers[M.name]
        }
        guard let h else { throw RPCError(code: .methodNotFound, message: M.name) }
        return try JSONCoding.decoder().decode(M.Result.self, from: try h(raw))
    }
}

/// loop.After and loop.fire.
@MainActor
private final class FakeTimers {
    var waiting: [@MainActor @Sendable () -> Void] = []

    func after(_ d: Duration, _ f: @escaping @MainActor @Sendable () -> Void) {
        waiting.append(f)
    }

    func fire() {
        let fs = waiting
        waiting = []
        for f in fs {
            f()
        }
    }
}

private func jsonValue<T: Encodable>(_ v: T) -> JSONValue {
    (try? JSONCoding.decoder().decode(JSONValue.self, from: JSONCoding.encoder().encode(v))) ?? .null
}

private func stamp(_ s: String) -> Date { RFC3339.parse(s)! }

@MainActor
private func options(_ d: FakeContactsDaemon, _ t: FakeTimers) -> ContactsOptions {
    ContactsOptions(caller: d, format: ContactBook.englishStrftime, now: { RFC3339.parse("2026-10-30T12:00:00Z")! },
                    after: { delay, f in t.after(delay, f) })
}

/// loop.run: waits until no call or question of the options runs.
@MainActor
private func settle(_ o: ContactsOptions) async throws {
    let deadline = ContinuousClock.now + .seconds(5)
    repeat {
        try await Task.sleep(for: .milliseconds(2))
        if ContinuousClock.now > deadline {
            throw HarnessTimeout()
        }
    } while o.calls.pending > 0
}

/// loop.fire: the timers, then what they started.
@MainActor
private func fire(_ t: FakeTimers, _ o: ContactsOptions) async throws {
    t.fire()
    try await settle(o)
}

private func ctlAccount(_ id: String, _ name: String, _ email: String, kind: AccountKind? = nil, caps: [Capability]? = nil,
                        graph: GraphConfig? = nil, caldav: CalDAVConfig? = nil) -> Account {
    Account(id: AccountID(id), config: AccountConfig(name: name, email: email, kind: kind, graph: graph, caldav: caldav),
            enabled: true, state: SyncState(accountId: AccountID(id), status: .idle), capabilities: caps)
}

private func ctlAccounts() -> [Account] {
    let on = GraphConfig(source: .daemon, contacts: ContactsConfig(sync: true))
    return [
        ctlAccount("acc_m", "Work", "jana@firma.cz", kind: .graph, caps: [.contacts], graph: on),
        ctlAccount("acc_p", "Petr", "petr@firma2.cz", kind: .graph, caps: [.contacts], graph: on),
        ctlAccount("acc_i", "Home", "jana@example.org"),
        ctlAccount("acc_d", "Nextcloud", "", kind: .caldav, caps: [.calendar, .contacts],
                   caldav: CalDAVConfig(serverUrl: "https://cloud.example.cz", username: "jana", contacts: true)),
    ]
}

private func ctlBooks() -> [AddressBook] {
    let t = stamp("2026-10-30T11:57:00Z")
    return [
        AddressBook(id: "b_m1", accountId: "acc_m", name: "Contacts", kind: .graph, visible: true, count: 3, lastSyncAt: t),
        AddressBook(id: "b_m2", accountId: "acc_m", name: "Archive", kind: .graph, visible: false, count: 9),
        AddressBook(id: "b_d", accountId: "acc_d", name: "Personal", kind: .carddav, visible: true, count: 2),
        AddressBook(id: .recent, name: "recent", kind: .recent, readOnly: true, visible: true, count: 5),
    ]
}

/// people: contacts of a book, sorted, sections by the first letter.
private func people(_ book: AddressBookID, _ names: [String]) -> [ContactSummary] {
    names.enumerated().map { i, n in
        ContactSummary(id: ContactID("p_\(book)_\(i)"), bookId: book, displayName: n, sortKey: n.lowercased(),
                       section: String(n.prefix(1)).uppercased())
    }
}

/// serveList: contact.list from `all` by book, query (a substring) and
/// paging.
private func serveList(_ d: FakeContactsDaemon, _ all: [AddressBookID: [ContactSummary]]) {
    d.on(API.ContactList.name) { raw in
        let p = try JSONCoding.decoder().decode(ContactListParams.self, from: raw)
        var src: [ContactSummary]
        if p.bookId == .all {
            src = (all["b_m1"] ?? []) + (all["b_d"] ?? [])
            src.sort { $0.sortKey < $1.sortKey }
        } else {
            src = all[p.bookId] ?? []
        }
        let q = (p.query ?? "").lowercased()
        let match = src.filter { q.isEmpty || $0.displayName.lowercased().contains(q) }
        let limit = p.limit ?? API.Limits.defaultContactListLimit
        let offset = p.offset ?? 0
        let end = min(offset + limit, match.count)
        let page = offset < end ? Array(match[offset..<end]) : []
        return try JSONCoding.encoder().encode(ContactListResult(contacts: page, total: match.count))
    }
}

/// standard: the accounts, the books, a consent refusal for acc_p and the
/// contacts.
private func standard() -> FakeContactsDaemon {
    let d = FakeContactsDaemon()
    d.answer(API.AccountList.name, AccountListResult(accounts: ctlAccounts()))
    let booksData = try! JSONCoding.encoder().encode(AddressBookListResult(books: ctlBooks()))
    d.on(API.AddressBookList.name) { raw in
        let p = try JSONCoding.decoder().decode(AddressBookListParams.self, from: raw)
        if p.accountId == "acc_p" {
            throw RPCError(code: .contactsConsentRequired, message: "consent",
                           data: .object(["accountId": .string("acc_p"), "signIn": .bool(true)]))
        }
        return booksData
    }
    serveList(d, [
        "b_m1": people("b_m1", ["Adámek", "Beneš", "Nováková"]),
        "b_d": people("b_d", ["Bílá", "Pokorný"]),
        .recent: people(.recent, ["a@x.cz", "b@x.cz"]),
    ])
    return d
}

// MARK: - window_test.go

@MainActor
@Suite(.serialized) struct ContactsListControllerTests {
    private func start() async throws -> (ContactsListController, FakeContactsDaemon, FakeTimers) {
        let d = standard()
        let t = FakeTimers()
        let w = ContactsListController(options: options(d, t))
        w.start()
        try await settle(w.options)
        return (w, d, t)
    }

    private func titles(_ w: ContactsListController) -> [String] {
        w.rows.map { $0.isHeading ? "[" + $0.heading + "]" : $0.title }
    }

    @Test func loads() async throws {
        let (w, d, _) = try await start()
        #expect(w.loaded && w.selectedBook == .all && w.error == "")
        #expect(titles(w) == ["[A]", "Adámek", "[B]", "Beneš", "Bílá", "[N]", "Nováková", "[P]", "Pokorný"])
        #expect(w.subtitle == "All Contacts · 5" && w.status == "Contacts synchronized 3 minutes ago")
        #expect(w.consent == [ContactsConsentNeed(accountId: "acc_p", account: "petr@firma2.cz", signIn: true)])
        let p = try d.last(API.ContactList.name, as: ContactListParams.self)
        #expect(p.bookId == .all && p.limit == ContactsListController.pageSize && (p.offset ?? 0) == 0)
        #expect(w.listEmpty == .none && w.detailEmpty == .selection && !w.hasMore)
        #expect(w.newContactBook == "b_m1" && w.canCreate)
        var signed: AccountID?
        w.onSignIn = { signed = $0 }
        w.allowConsent("acc_p")
        #expect(signed == "acc_p")
        let dav = try #require(w.book("b_d"))
        #expect(dav.account?.id == "acc_d" && dav.book.name == "Personal")
        let recent = try #require(w.book(.recent))
        #expect(recent.account == nil)
    }

    @Test func searchDebouncedAndFlat() async throws {
        let (w, d, t) = try await start()
        let n = d.count(API.ContactList.name)
        w.setQuery("b")
        w.setQuery("be")
        try await settle(w.options)
        #expect(d.count(API.ContactList.name) == n, "searched before the debounce")
        try await fire(t, w.options)
        #expect(d.count(API.ContactList.name) == n + 1)
        #expect(titles(w) == ["Beneš"])
        w.setQuery("zz")
        try await fire(t, w.options)
        #expect(w.listEmpty == .search)
        w.setQuery(" ")
        try await fire(t, w.options)
        #expect(w.contacts.count == 5 && w.listEmpty == .none)
    }

    @Test func selectionKeptAcrossReload() async throws {
        let (w, d, t) = try await start()
        var selected: [ContactID?] = []
        w.onSelect = { selected.append($0) }
        let id = w.contacts[2].id
        w.select(id)
        w.select(id)
        #expect(selected == [id] && w.detailEmpty == .none)
        // A change elsewhere: one reload after the debounce, selection kept.
        let n = d.count(API.AddressBookList.name)
        w.handle(API.Notify.contactsChanged)
        w.handle(API.Notify.contactsChanged)
        w.handle(API.Notify.newMessage)
        try await fire(t, w.options)
        #expect(d.count(API.AddressBookList.name) == n + 2, "the books and acc_p's consent")
        #expect(w.selected == id && selected.count == 1)
        // The contact goes away: the selection with it.
        serveList(d, ["b_m1": people("b_m1", ["Adámek"])])
        w.reload()
        try await settle(w.options)
        #expect(w.selected == nil && selected.last == .some(nil), "a vanished contact stays selected")
    }

    @Test func booksAndPaging() async throws {
        let (w, d, _) = try await start()
        w.selectBook("b_m2") // hidden: not in the sidebar
        #expect(w.selectedBook == .all, "selected a hidden book")
        w.selectBook(.recent)
        try await settle(w.options)
        #expect(w.subtitle == "Recently Used · 2 addresses")
        #expect(w.newContactBook == "b_m1", "recent is not writable")
        let letters = Array("abcdefghijklmnopqrstuvwxyz")
        let names = (0..<250).map { i in "N" + String(letters[i % 26]) + String(letters[i / 26]) }
        serveList(d, ["b_d": people("b_d", names)])
        w.selectBook("b_d")
        try await settle(w.options)
        #expect(w.contacts.count == ContactsListController.pageSize && w.hasMore && w.total == 250)
        w.loadMore()
        w.loadMore() // on its way: nothing
        try await settle(w.options)
        w.loadMore()
        try await settle(w.options)
        #expect(w.contacts.count == 250 && !w.hasMore)
        var p = try d.last(API.ContactList.name, as: ContactListParams.self)
        #expect(p.offset == 200)
        // A reload keeps every shown row.
        w.reload()
        try await settle(w.options)
        p = try d.last(API.ContactList.name, as: ContactListParams.self)
        #expect(p.limit == 250 && w.contacts.count == 250)
    }

    @Test func onlyRecentAndFailure() async throws {
        let d = FakeContactsDaemon()
        let t = FakeTimers()
        d.answer(API.AccountList.name, AccountListResult(accounts: []))
        d.answer(API.AddressBookList.name, AddressBookListResult(books: [ctlBooks()[3]]))
        serveList(d, [:])
        let w = ContactsListController(options: options(d, t))
        var changes = 0
        w.onChange = { changes += 1 }
        w.start()
        try await settle(w.options)
        #expect(w.selectedBook == .recent && w.detailEmpty == .noBooks && w.listEmpty == .book && !w.canCreate)
        #expect(changes > 0, "no change reported")
        d.fail(API.AddressBookList.name, RPCError(code: .storageError, message: "x"))
        w.reload()
        try await settle(w.options)
        #expect(w.error != "")
        w.stop()
        w.reload()
        try await settle(w.options)
    }
}

// MARK: - contact_test.go

private func janaCard() -> ContactCard {
    ContactCard(id: "p_1", bookId: "b_m1", revision: "r1", name: ContactName(given: "Jana", family: "Nováková"),
                displayName: "Jana Nováková", org: "Stavby",
                emails: [ContactEmail(label: .work, address: "jana@firma.cz")],
                phones: [ContactPhone(label: .mobile, number: "+420 601")])
}

/// What the questions asked and what the next answer is.
@MainActor
private final class Questions {
    var asked: [ContactBook.Question] = []
    var answer = true
}

@MainActor
@Suite(.serialized) struct ContactsDetailControllerTests {
    /// contactSetup: a detail controller over the standard daemon's window
    /// (for the books), contact.get answering `card`.
    private func setup(_ card: ContactCard) async throws
        -> (ContactsDetailController, FakeContactsDaemon, FakeTimers, Questions, ContactsListController) {
        let d = standard()
        let t = FakeTimers()
        let o = options(d, t)
        let w = ContactsListController(options: o)
        w.start()
        try await settle(o)
        let data = try JSONCoding.encoder().encode(ContactGetResult(contact: card))
        let cardID = card.id
        d.on(API.ContactGet.name) { raw in
            let p = try JSONCoding.decoder().decode(ContactGetParams.self, from: raw)
            if p.contactId != cardID {
                throw RPCError(code: .contactNotFound, message: "x")
            }
            return data
        }
        let c = ContactsDetailController(options: o) { id in w.book(id) }
        let qs = Questions()
        c.onQuestion = { q in
            qs.asked.append(q)
            return qs.answer
        }
        c.open(card.id)
        #expect(c.loading)
        try await settle(o)
        return (c, d, t, qs, w)
    }

    @Test func detail() async throws {
        let (c, _, _, _, _) = try await setup(janaCard())
        let d = try #require(c.detail)
        #expect(d.title == "Jana Nováková" && c.canEdit && c.canDelete)
        #expect(c.sourceLine == "Address book Contacts · Microsoft 365 (jana@firma.cz)")
        #expect(c.copyText.hasPrefix("Jana Nováková\nStavby\nwork email: jana@firma.cz"))
    }

    @Test func openGone() async throws {
        let (c, _, _, _, _) = try await setup(janaCard())
        c.open("p_none")
        try await settle(c.options)
        #expect(c.card == nil && c.error == "The contact no longer exists.")
        c.open(nil)
        #expect(!c.loading && c.id == nil)
    }

    @Test func editUnchangedSendsNothing() async throws {
        let (c, d, _, _, _) = try await setup(janaCard())
        c.beginEdit()
        #expect(c.editing && !c.dirty)
        c.save()
        #expect(!c.editing && d.count(API.ContactUpdate.name) == 0)
    }

    @Test func update() async throws {
        let (c, d, _, _, _) = try await setup(janaCard())
        d.answer(API.ContactUpdate.name, ContactUpdateResult(revision: "r2"))
        c.beginEdit()
        c.updateForm { $0.title = "Ředitelka" }
        c.save()
        #expect(c.saving)
        try await settle(c.options)
        let p = try d.last(API.ContactUpdate.name, as: ContactUpdateParams.self)
        #expect(p.contactId == "p_1" && p.revision == "r1" && p.card.title == "Ředitelka" && p.card.displayName == "Jana Nováková")
        #expect(!c.editing && !c.saving && d.count(API.ContactGet.name) == 2)
    }

    @Test func validationKeepsTheForm() async throws {
        let (c, d, _, _, _) = try await setup(janaCard())
        c.beginEdit()
        c.updateForm { $0.emails[0].value = "nope" }
        c.save()
        #expect(c.editing && c.problems.count == 1 && d.count(API.ContactUpdate.name) == 0)
    }

    @Test func offlineKeepsTheForm() async throws {
        let (c, d, _, _, _) = try await setup(janaCard())
        d.fail(API.ContactUpdate.name, RPCError(code: .offline, message: "x"))
        c.beginEdit()
        c.updateForm { $0.org = "Jiná" }
        c.save()
        try await settle(c.options)
        #expect(c.editing && c.form?.org == "Jiná" && c.error == "Contacts cannot be changed while offline.")
        c.cancelEdit()
        #expect(!c.editing && c.error == "")
    }

    /// contact_test.go TestContactGraphLimitsAndRefusedField: a card of a
    /// Microsoft book is checked against Graph's limits before anything is
    /// sent; a field the daemon refuses is marked, not said.
    @Test func graphLimitsAndRefusedField() async throws {
        let (c, d, _, _, _) = try await setup(janaCard())
        c.beginEdit()
        c.updateForm { $0.urls = ["a.cz", "b.cz"].map { ContactBook.LabelledValue(value: $0) } }
        c.save()
        try await settle(c.options)
        #expect(d.count(API.ContactUpdate.name) == 0 && c.problems.count == 1 && c.problems.first?.key == .urls)
        c.updateForm { $0.urls = Array($0.urls.prefix(1)) }
        d.fail(API.ContactUpdate.name, RPCError(code: .invalidArgument, message: "x", data: .object(["field": .string("phones[0].number")])))
        c.save()
        try await settle(c.options)
        #expect(c.editing && c.error == "" && c.problems.count == 1 && c.problems.first?.key == .phones && c.problems.first?.index == 0)
    }

    @Test func conflictReloadKeepsTheUsersFields() async throws {
        let (c, d, _, qs, _) = try await setup(janaCard())
        var server = janaCard()
        server.revision = "r9"
        server.org = "Novák a syn"
        server.note = "nová"
        d.fail(API.ContactUpdate.name, RPCError(code: .contactConflict, message: "x", data: .object(["server": jsonValue(server)])))
        c.beginEdit()
        c.updateForm { $0.title = "Ředitelka" }
        c.save()
        try await settle(c.options)
        #expect(qs.asked.count == 1 && qs.asked.first?.confirm == "Reload")
        let f = try #require(c.form)
        #expect(f.org == "Novák a syn" && f.title == "Ředitelka" && f.note == "nová" && f.original.revision == "r9" && c.error == "")
        d.answer(API.ContactUpdate.name, ContactUpdateResult(revision: "r10"))
        c.save()
        try await settle(c.options)
        let p = try d.last(API.ContactUpdate.name, as: ContactUpdateParams.self)
        #expect(p.revision == "r9" && p.card.org == "Novák a syn" && !c.editing)
    }

    @Test func conflictWithoutDataAsksGet() async throws {
        let (c, d, _, qs, _) = try await setup(janaCard())
        d.fail(API.ContactUpdate.name, RPCError(code: .contactConflict, message: "x"))
        c.beginEdit()
        c.updateForm { $0.title = "X" }
        c.save()
        try await settle(c.options)
        #expect(qs.asked.count == 1 && c.form?.title == "X", "conflict without data")
        // Cancel keeps the form as it was.
        qs.answer = false
        c.save()
        try await settle(c.options)
        #expect(c.editing && c.error == "The contact changed meanwhile")
    }

    @Test func create() async throws {
        let (c, d, _, _, _) = try await setup(janaCard())
        var created: ContactID?
        c.onCreated = { created = $0 }
        d.answer(API.ContactCreate.name, ContactCreateResult(contactId: "p_new", revision: "n1"))
        let a = ContactBook.AddFromChip(address: Address(name: "Petr Svoboda", address: "petr@example.com"), account: "acc_d",
                                        books: ctlBooks(), accounts: ctlAccounts())
        c.beginNew(book: a.book, form: a.form)
        #expect(c.editing && c.card == nil)
        c.save()
        try await settle(c.options)
        let p = try d.last(API.ContactCreate.name, as: ContactCreateParams.self)
        #expect(p.bookId == "b_d" && p.card.displayName == "Petr Svoboda" && p.card.emails.first?.address == "petr@example.com")
        #expect(created == "p_new" && c.id == "p_new" && !c.editing)
        // A refused create keeps the form with the sentence.
        d.fail(API.ContactCreate.name, RPCError(code: .addressBookReadOnly, message: "x"))
        c.beginNew(book: "b_m1")
        c.updateForm { $0.given = "X" }
        c.save()
        try await settle(c.options)
        #expect(c.editing && c.error == "This address book cannot be changed.")
        // Consent names the account.
        d.fail(API.ContactCreate.name, RPCError(code: .contactsConsentRequired, message: "x"))
        c.save()
        try await settle(c.options)
        #expect(c.error == "Microsoft needs your permission to access the contacts of Microsoft 365 (jana@firma.cz).")
    }

    @Test func delete() async throws {
        let (c, d, _, qs, _) = try await setup(janaCard())
        var deleted: ContactID?
        c.onDeleted = { deleted = $0 }
        d.answer(API.ContactDelete.name, EmptyResult())
        c.delete()
        try await settle(c.options)
        #expect(qs.asked.count == 1 && qs.asked.first?.heading == "Delete contact Jana Nováková?")
        #expect(qs.asked.first?.body.contains("Microsoft 365 (jana@firma.cz)") == true)
        let p = try d.last(API.ContactDelete.name, as: ContactDeleteParams.self)
        #expect(p.contactId == "p_1" && p.revision == "r1" && deleted == "p_1" && c.card == nil)
    }

    @Test func deleteCancelledAndConflict() async throws {
        let (c, d, _, qs, _) = try await setup(janaCard())
        qs.answer = false
        c.delete()
        try await settle(c.options)
        #expect(d.count(API.ContactDelete.name) == 0, "deleted after Cancel")
        qs.answer = true
        var server = janaCard()
        server.revision = "r5"
        d.fail(API.ContactDelete.name, RPCError(code: .contactConflict, message: "x", data: .object(["server": jsonValue(server)])))
        c.delete()
        try await settle(c.options)
        #expect(c.card?.revision == "r5" && c.error != "", "conflict on delete")
        // Read only: no delete, no edit.
        var ro = janaCard()
        ro.readOnly = true
        let (c2, _, _, _, _) = try await setup(ro)
        #expect(!c2.canEdit && !c2.canDelete)
    }

    @Test func followsChanges() async throws {
        let (c, d, _, _, _) = try await setup(janaCard())
        let n = d.count(API.ContactGet.name)
        c.contactsChanged(ContactsChangedNotification(accountId: "acc_d"))
        c.contactsChanged(ContactsChangedNotification(accountId: "acc_m", bookIds: ["b_m2"]))
        try await settle(c.options)
        #expect(d.count(API.ContactGet.name) == n, "reloaded for another book")
        c.contactsChanged(ContactsChangedNotification(accountId: "acc_m"))
        try await settle(c.options)
        #expect(d.count(API.ContactGet.name) == n + 1, "not reloaded")
        c.beginEdit()
        c.contactsChanged(ContactsChangedNotification())
        try await settle(c.options)
        #expect(d.count(API.ContactGet.name) == n + 1, "reloaded under the form")
    }
}

// MARK: - prefs_lookup_test.go

@MainActor
@Suite(.serialized) struct ContactsLookupAndPreferencesTests {
    @Test func lookup() async throws {
        let d = FakeContactsDaemon()
        let t = FakeTimers()
        d.on(API.ContactLookup.name) { raw in
            let p = try JSONCoding.decoder().decode(ContactLookupParams.self, from: raw)
            var known: [String: ContactID] = [:]
            for a in p.addresses where a == "jana@firma.cz" {
                known[a] = "p_1"
            }
            return try JSONCoding.encoder().encode(ContactLookupResult(known: known))
        }
        let o = options(d, t)
        let lk = ContactsLookupController(options: o)
        var changed: [String] = []
        lk.onChange = { changed.append($0) }
        lk.ask("m1", addresses: [" Jana@Firma.cz ", "petr@example.net", "JANA@firma.cz", ""])
        lk.ask("m1", addresses: ["x@y.cz"])
        #expect(lk.item("m1", address: "jana@firma.cz", books: ctlBooks()).item == ContactBook.ChipItem.none, "an item before the answer")
        try await settle(o)
        let p = try d.last(API.ContactLookup.name, as: ContactLookupParams.self)
        #expect(p.addresses == ["jana@firma.cz", "petr@example.net"] && d.count(API.ContactLookup.name) == 1)
        let show = lk.item("m1", address: "JANA@firma.cz", books: ctlBooks())
        #expect(show.item == .show && show.id == "p_1")
        #expect(lk.item("m1", address: "petr@example.net", books: ctlBooks()).item == .add)
        #expect(lk.item("m1", address: "petr@example.net", books: [ctlBooks()[3]]).item == ContactBook.ChipItem.none, "no writable book")
        #expect(changed == ["m1"])
        // Invalidate drops answers, also those on their way.
        lk.ask("m2", addresses: ["a@b.cz"])
        lk.invalidate()
        try await settle(o)
        #expect(!lk.known("m2", address: "a@b.cz").answered, "a stale answer stored")
        #expect(!lk.known("m1", address: "jana@firma.cz").answered, "not forgotten")
        // Many addresses go in chunks; an empty message is answered at once.
        let many = (0..<450).map { "a\($0)@x.cz" }
        let n = d.count(API.ContactLookup.name)
        lk.ask("big", addresses: many)
        try await settle(o)
        #expect(d.count(API.ContactLookup.name) == n + 3)
        #expect(lk.known("big", address: "a1@x.cz").answered, "chunks not stored")
        lk.ask("empty", addresses: [])
        #expect(lk.known("empty", address: "x").answered)
        // Only `kept` messages stay.
        for i in 0..<(ContactsLookupController.kept + 5) {
            lk.ask("k\(i)", addresses: [])
        }
        #expect(!lk.known("big", address: "a1@x.cz").answered, "old answers kept")
        // A failed lookup stores nothing; it is asked again.
        d.fail(API.ContactLookup.name, RPCError(code: .storageError, message: "x"))
        lk.ask("f", addresses: ["a@b.cz"])
        try await settle(o)
        #expect(!lk.known("f", address: "a@b.cz").answered, "failure stored")
        lk.ask("f", addresses: ["a@b.cz"])
        try await settle(o)
        #expect(d.count(API.ContactLookup.name) == n + 5, "not asked again")
    }

    private func startPrefs() async throws -> (ContactsPreferencesController, FakeContactsDaemon) {
        let d = standard()
        let p = ContactsPreferencesController(options: options(d, FakeTimers()))
        p.start()
        try await settle(p.options)
        return (p, d)
    }

    @Test func sections() async throws {
        let (p, _) = try await startPrefs()
        let secs = p.sections
        let got = secs.map { s in
            "\(s.title)/\(s.badge) sync=\(s.syncOn) consent=\(s.consent) carddav=\(s.cardDAV) "
                + "[" + s.books.map { "\($0.name):\($0.visible)" }.joined(separator: " ") + "]"
        }
        #expect(got == [
            "jana@firma.cz/M365 sync=true consent=false carddav=false [Contacts:true Archive:false]",
            "petr@firma2.cz/M365 sync=true consent=true carddav=false []",
            "Nextcloud/CARDDAV sync=true consent=false carddav=true [Personal:true]",
        ])
        #expect(secs[1].signIn && !p.empty)
        var signed: AccountID?
        p.onSignIn = { signed = $0 }
        p.allow("acc_p")
        #expect(signed == "acc_p")
    }

    @Test func syncSwitch() async throws {
        let (p, d) = try await startPrefs()
        d.answer(API.AccountUpdate.name, EmptyResult())
        p.setSync("acc_m", on: false)
        #expect(!p.sections[0].syncOn && p.saving("acc_m"), "not shown at once")
        p.setSync("acc_m", on: true) // while saving: nothing
        try await settle(p.options)
        var u = try d.last(API.AccountUpdate.name, as: AccountUpdateParams.self)
        #expect(u.accountId == "acc_m" && u.config.graph?.contacts?.sync == false && u.credentials == Credentials())
        #expect(d.count(API.AccountUpdate.name) == 1)
        // CardDAV and a refusal undone.
        d.fail(API.AccountUpdate.name, RPCError(code: .accountNotFound, message: "x"))
        p.setSync("acc_d", on: false)
        try await settle(p.options)
        u = try d.last(API.AccountUpdate.name, as: AccountUpdateParams.self)
        #expect(u.config.caldav?.contacts == false && u.config.caldav?.serverUrl == "https://cloud.example.cz")
        #expect(p.sections[2].syncOn && p.error != "", "refusal not undone")
        p.setSync("acc_i", on: true) // no contacts capability
        #expect(d.count(API.AccountUpdate.name) == 2, "imap account updated")
        #expect(ContactBook.syncOn(ContactBook.withSync(AccountConfig(name: "", email: "", kind: .graph), true)), "graph without config")
        #expect(ContactBook.syncOn(ContactBook.withSync(AccountConfig(name: "", email: "", kind: .caldav), true)), "caldav without config")
        #expect(!ContactBook.syncOn(AccountConfig(name: "", email: "")))
    }

    @Test func bookCheckbox() async throws {
        let (p, d) = try await startPrefs()
        let archive = ctlBooks()[1]
        d.on(API.AddressBookSet.name) { raw in
            let s = try JSONCoding.decoder().decode(AddressBookSetParams.self, from: raw)
            var b = archive
            b.visible = s.visible
            b.count = 10
            return try JSONCoding.encoder().encode(AddressBookSetResult(book: b))
        }
        p.setVisible("b_m2", true)
        #expect(p.sections[0].books[1].visible, "not at once")
        try await settle(p.options)
        #expect(p.sections[0].books[1].count == 10, "answer not taken")
        d.fail(API.AddressBookSet.name, RPCError(code: .addressBookNotFound, message: "x"))
        p.setVisible("b_m1", false)
        try await settle(p.options)
        #expect(p.sections[0].books[0].visible && p.error == "The address book no longer exists.")
        p.setVisible("b_none", true)
    }

    @Test func scope() async throws {
        let (p, _) = try await startPrefs()
        #expect(p.suggestScope == .account)
        var stored = ContactSearchScope("")
        p.scope = { stored }
        p.setScope = { stored = $0 }
        p.setSuggestScope("bogus")
        p.setSuggestScope(.all)
        #expect(stored == .all && p.suggestScope == .all)
        stored = "weird"
        #expect(p.suggestScope == .account, "unknown stored value")
    }

    @Test func emptyAndReload() async throws {
        let d = FakeContactsDaemon()
        d.answer(API.AccountList.name, AccountListResult(accounts: [ctlAccounts()[2]]))
        d.answer(API.AddressBookList.name, AddressBookListResult(books: [ctlBooks()[3]]))
        let p = ContactsPreferencesController(options: options(d, FakeTimers()))
        p.start()
        try await settle(p.options)
        #expect(p.empty && p.loaded)
        let n = d.count(API.AccountList.name)
        p.handle(API.Notify.accountsChanged)
        p.handle(API.Notify.newMessage)
        try await settle(p.options)
        #expect(d.count(API.AccountList.name) == n + 1)
    }

    @Test func helpers() throws {
        let typed = RPCError(code: .contactConflict, message: "x", data: .object(["server": jsonValue(ContactCard(id: "p"))]))
        #expect(ContactsOptions.conflictCard(typed)?.id == "p")
        #expect(ContactsOptions.conflictCard(RPCError(code: .offline, message: "x")) == nil)
        #expect(ContactsOptions.consentData(RPCError(code: .contactsConsentRequired, message: "x")) == nil)
        let raw = RPCNotification(method: API.Notify.contactsChanged,
                                  line: json(#"{"jsonrpc":"2.0","method":"notify.contactsChanged","params":{"accountId":"a","bookIds":["b"]}}"#))
        #expect(try ContactsChangedNotification.decode(raw) == ContactsChangedNotification(accountId: "a", bookIds: ["b"]))
        let other = RPCNotification(method: API.Notify.newMessage, line: json(#"{"jsonrpc":"2.0","method":"notify.newMessage","params":{}}"#))
        #expect(try ContactsChangedNotification.decode(other) == nil)
        let bare = RPCNotification(method: API.Notify.contactsChanged, line: json(#"{"jsonrpc":"2.0","method":"notify.contactsChanged"}"#))
        #expect(try ContactsChangedNotification.decode(bare) == ContactsChangedNotification(), "no params: any book")
        #expect(ContactsOptions.asRPCError(HarnessTimeout()).code == .internalError)
        #expect(ContactsOptions.asRPCError(RPCClient.ClientError.disconnected).code == .unavailable)
        #expect(ContactsOptions.asRPCError(CancellationError()).code == .cancelled)
        let o = options(FakeContactsDaemon(), FakeTimers())
        #expect(o.errorMessage(RPCError(code: .networkError, message: "x")) == "Contacts" && o.errorMessage(nil) == "")
    }
}
