// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/contacts contacts_test.go and form_test.go, ported (the
// template test po_test.go is check-strings.py here). Fictional people
// only.

private func stamp(_ s: String) -> Date { RFC3339.parse(s)! }

private func account(_ id: String, _ name: String, _ email: String, kind: AccountKind? = nil) -> Account {
    Account(id: AccountID(id), config: AccountConfig(name: name, email: email, kind: kind), enabled: true,
            state: SyncState(accountId: AccountID(id), status: .idle))
}

func contactBookAccounts() -> [Account] {
    [
        account("acc_m365", "Work", "jana@firma.cz", kind: .graph),
        account("acc_imap", "Home", "jana@example.org"),
        account("acc_dav", "Nextcloud", "", kind: .caldav),
    ]
}

func contactBookBooks() -> [AddressBook] {
    let t = stamp("2026-10-30T10:00:00Z")
    let t2 = t.addingTimeInterval(-3600)
    return [
        AddressBook(id: "b_dav", accountId: "acc_dav", name: "Personal", kind: .carddav, visible: true, count: 52, lastSyncAt: t2),
        AddressBook(id: "b_m1", accountId: "acc_m365", name: "Contacts", kind: .graph, visible: true, count: 318, lastSyncAt: t),
        AddressBook(id: "b_m2", accountId: "acc_m365", name: "Suppliers", kind: .graph, readOnly: true, visible: true, count: 41),
        AddressBook(id: "b_m3", accountId: "acc_m365", name: "Archive 2019", kind: .graph, visible: false, count: 122),
        AddressBook(id: .recent, name: "recent", kind: .recent, readOnly: true, visible: true, count: 1204),
    ]
}

func contactBookCard() -> ContactCard {
    ContactCard(
        id: "p_1", bookId: "b_m1", revision: "r1",
        name: ContactName(given: "Jana", family: "Nováková"),
        displayName: "Jana Nováková", org: "Stavby Novák s.r.o.", title: "Vedoucí nákupu",
        emails: [ContactEmail(label: .work, address: "jana.novakova@example.cz"), ContactEmail(label: .home, address: "jana@example.org")],
        phones: [ContactPhone(label: .mobile, number: "+420 601 234 567"), ContactPhone(label: "Kancelář", number: "1")],
        addresses: [ContactAddress(label: .work, street: "Dlouhá 12", city: "Praha 1", postcode: "110 00"), ContactAddress(street: "  ")],
        urls: [ContactURL(url: "stavbynovak.example.cz"), ContactURL(label: .work, url: "javascript:alert(1)")],
        birthday: ContactBirthday(month: 3, day: 14),
        note: "Volat po 10. hodině.\r\n\r\n\r\n\r\nDruhý řádek")
}

private let english = ContactBook.englishStrftime

@Suite struct ContactBookTests {
    @Test func sidebarGroupsByAccountOrder() throws {
        let items = ContactBook.sidebar(contactBookBooks(), accounts: contactBookAccounts())
        #expect(items.map { $0.title + "/" + $0.badge } == [
            "All Contacts/", "jana@firma.cz/M365", "Contacts/", "Suppliers/", "Nextcloud/CARDDAV", "Personal/",
            "On This Computer/", "Recently Used/",
        ])
        #expect(items[0].count == 318 + 41 + 52 && items[0].book == .all)
        #expect(!items[1].selectable && !items[6].selectable && items[7].selectable && items[7].readOnly && items[3].readOnly
            && !items[2].readOnly)
        #expect(ContactBook.hasBooks(items) && ContactBook.defaultSelection(items) == .all)
        let dav = try #require(ContactBook.find(items, "b_dav"))
        #expect(dav.accountId == "acc_dav")
        #expect(ContactBook.find(items, "b_m3") == nil, "a hidden book is listed")
    }

    @Test func sidebarOnlyRecent() {
        let books = [AddressBook(id: .recent, kind: .recent, visible: true, count: 3)]
        var items = ContactBook.sidebar(books, accounts: [])
        #expect(items.count == 2 && items[1].kind == .recent && !ContactBook.hasBooks(items)
            && ContactBook.defaultSelection(items) == .recent)
        #expect(ContactBook.defaultSelection([]) == nil)
        // A book of an account account.list no longer has stays, at the end.
        items = ContactBook.sidebar(books + [AddressBook(id: "b_x", accountId: "acc_gone", kind: .carddav, visible: true)], accounts: [])
        #expect(items[1].kind == .account && items[1].badge == ContactBook.badgeCardDAV && items[2].title == "Contacts")
    }

    @Test func listSubtitlesAndStatus() throws {
        let items = ContactBook.sidebar(contactBookBooks(), accounts: contactBookAccounts())
        #expect(ContactBook.listSubtitle(items[0], total: 412) == "All Contacts · 412")
        #expect(ContactBook.listSubtitle(items[7], total: 1) == "Recently Used · 1 address")
        #expect(ContactBook.listSubtitle(items[5], total: 82) == "Personal · 82 contacts")
        let at = try #require(ContactBook.syncedAt(contactBookBooks()))
        #expect(at == stamp("2026-10-30T09:00:00Z"))
        let now = stamp("2026-10-30T10:03:00Z")
        #expect(ContactBook.statusLine(contactBookBooks(), now: now).hasPrefix("Contacts synchronized "))
        #expect(ContactBook.statusLine([], now: now) == "")
    }

    @Test func rowsSections() {
        let list = [
            ContactSummary(id: "p1", bookId: "b", displayName: "Tomáš Adámek", section: "A", org: "Stavby"),
            ContactSummary(id: "p2", bookId: "b", displayName: "Lucie Andělová", section: "A", email: "lucie@example.com"),
            ContactSummary(id: "p3", bookId: "b", displayName: "", section: "", email: "x@example.com"),
            ContactSummary(id: "p4", bookId: "b", displayName: "\u{202E}Evil", section: "č"),
        ]
        let rows = ContactBook.rows(list, headings: true)
        let got = rows.map { $0.isHeading ? "[" + $0.heading + "]" : $0.title + "|" + $0.detail + "|" + $0.initials }
        #expect(got == ["[A]", "Tomáš Adámek|Stavby|TA", "Lucie Andělová|lucie@example.com|LA", "[#]", "x@example.com||X",
                        "[Č]", "Evil||E"])
        #expect(ContactBook.rows(list, headings: false).count == 4)
    }

    @Test func detail() {
        let d = ContactBook.detail(contactBookCard(), format: english)
        #expect(d.title == "Jana Nováková" && d.subtitle == "Vedoucí nákupu · Stavby Novák s.r.o." && d.initials == "JN" && !d.readOnly)
        #expect(d.rows.map { $0.label + "=" + $0.value + "=" + $0.url } == [
            "work email=jana.novakova@example.cz=", "home email=jana@example.org=",
            "mobile=+420 601 234 567=", "Kancelář=1=",
            "work=Dlouhá 12, 110 00 Praha 1=",
            "birthday=14 March=",
            "website=stavbynovak.example.cz=https://stavbynovak.example.cz", "work website=javascript:alert(1)=",
            "note=Volat po 10. hodině.\n\n\nDruhý řádek=",
        ])
        let a = d.actions
        #expect(a.writeEmail && a.emails.count == 2 && a.showMail == "from:jana.novakova@example.cz" && a.edit && a.delete)
        var c = contactBookCard()
        c.emails = Array(c.emails.prefix(1))
        c.readOnly = true
        let ro = ContactBook.detail(c, format: english)
        #expect(ro.actions.emails.isEmpty && !ro.actions.edit && !ro.actions.delete && ro.readOnly)
        let empty = ContactBook.detail(ContactCard(), format: english)
        #expect(empty.title == "No Name" && empty.initials == "" && !empty.actions.writeEmail && empty.actions.showMail == "")
    }

    @Test func copyText() {
        var c = contactBookCard()
        c.note = ""
        let got = ContactBook.copyText(c, format: english)
        #expect(got.hasPrefix("Jana Nováková\nVedoucí nákupu · Stavby Novák s.r.o.\nwork email: jana.novakova@example.cz\n"))
        #expect(!got.contains("BEGIN:VCARD"))
    }

    @Test func birthday() {
        #expect(ContactBook.birthdayText(ContactBirthday(year: 1980, month: 3, day: 14), format: english) == "14 March 1980")
        #expect(ContactBook.birthdayText(ContactBirthday(month: 2, day: 29), format: english) == "29 February")
        for b in [ContactBirthday(year: 2023, month: 2, day: 29), ContactBirthday(month: 13, day: 1), ContactBirthday(month: 4, day: 31),
                  ContactBirthday(month: 1, day: 0), ContactBirthday(year: -1, month: 1, day: 1)] {
            #expect(!ContactBook.validBirthday(b) && ContactBook.birthdayText(b, format: english) == "", "\(b)")
        }
    }

    @Test func showMailQuery() {
        let cases: [(String, String)] = [
            ("jana@example.org", "from:jana@example.org"),
            ("\"a b\"@example.org", "from:\"a b@example.org\""),
            ("„x“@e.cz", "from:x@e.cz"),
            ("a\nb@e.cz", "from:\"a b@e.cz\""),
            ("  ", ""),
            ("\"\"", ""),
            ("jana@example.org\u{0}x", "from:\"jana@example.org x\""),
        ]
        for (input, want) in cases {
            #expect(ContactBook.showMailQuery(input) == want, "\(input)")
        }
    }

    @Test func openURL() {
        let cases: [(String, String)] = [
            ("https://example.org/a", "https://example.org/a"),
            ("http://example.org", "http://example.org"),
            ("example.org/x", "https://example.org/x"),
            ("javascript:alert(1)", ""),
            ("file:///etc/passwd", ""),
            ("https://user:pw@example.org", ""),
            ("ftp://example.org", ""),
            ("localhost", ""),
            ("/relative", ""),
            ("exa mple.org", ""),
            ("https://exa\u{202E}mple.org/path", "https://example.org/path"),
        ]
        for (input, want) in cases {
            #expect(ContactBook.openURL(input) == want, "\(input)")
        }
    }

    @Test func labels() {
        #expect(ContactBook.labelText(.email, .work) == "work" && ContactBook.labelText(.email, .none) == "No Label")
        #expect(ContactBook.labelText(.phone, .fax) == "fax" && ContactBook.labelText(.url, .home) == "home")
        #expect(ContactBook.labelText(.phone, "x-\u{202E}custom") == "x-custom" && ContactBook.labelText(.address, .other) == "other")
        #expect(ContactBook.detailLabel(.email, .none) == "email" && ContactBook.detailLabel(.phone, .none) == "phone")
        #expect(ContactBook.detailLabel(.address, .none) == "address" && ContactBook.detailLabel(.url, .other) == "website")
        #expect(ContactBook.detailLabel(.url, .work) == "work website" && ContactBook.detailLabel(.email, .other) == "other email")
        let phone = ContactBook.labelChoices(.phone, current: "custom")
        #expect(phone.count == 9 && phone[7] == ContactLabel.none && phone[8] == "custom")
        #expect(ContactBook.labelChoices(.email, current: .work).count == 4)
    }

    @Test func addressLineAndClean() {
        let a = ContactAddress(street: "Dlouhá 12\nvchod B", city: "Praha", region: " ", postcode: "110 00", country: "Česko\u{200B}")
        #expect(ContactBook.addressLine(a) == "Dlouhá 12, vchod B, 110 00 Praha, Česko")
        #expect(ContactBook.clean("a\t\u{2066}b\u{2069}  c\u{07}", max: 0) == "a b c")
        #expect(ContactBook.clean("ěěě", max: 5) == "ěě")
        #expect(ContactBook.cleanBlock("a\r\nb\r\rc", max: 0) == "a\nb\n\nc")
    }

    @Test func initials() {
        let cases: [(String, String, String)] = [
            ("Jana Nováková", "", "JN"), ("eva", "", "E"), ("", "x@y.cz", "X"), ("", "", ""),
            ("(Ing.) Petr  van Dyk", "", "ID"), ("Řehoř Šťastný", "", "ŘŠ"),
        ]
        for (name, address, want) in cases {
            #expect(ContactBook.initials(name, address: address) == want, "\(name)")
        }
    }

    @Test func accountLabels() {
        let accs = contactBookAccounts()
        #expect(ContactBook.accountLabel(accs[0]) == "Microsoft 365 (jana@firma.cz)")
        #expect(ContactBook.accountLabel(accs[2]) == "Nextcloud")
        #expect(ContactBook.accountLabel(nil) == "")
        #expect(ContactBook.sourceLine(book: "Contacts", account: ContactBook.accountLabel(accs[0]))
            == "Address book Contacts · Microsoft 365 (jana@firma.cz)")
        #expect(ContactBook.suggestionTooltip(book: "", account: "") == "Address book")
        #expect(ContactBook.suggestionTooltip(book: "Kontakty", account: "") == "Kontakty")
        #expect(ContactBook.suggestionTooltip(book: "Kontakty", account: "Microsoft 365") == "Kontakty · Microsoft 365")
        #expect(ContactBook.suggestionTooltip(book: "", account: "X") == "X")
        #expect(ContactBook.badge("eway") == ContactBook.badgeEway && ContactBook.badge("weird") == "")
        #expect(ContactBook.accountKindBadge(accs[1]) == "")
    }

    @Test func errors() {
        for c in [ErrorCode.contactNotFound, .addressBookNotFound, .addressBookReadOnly, .contactConflict, .contactsConsentRequired,
                  .offline, .invalidArgument] {
            #expect(!(ContactBook.errorText(c, account: "") ?? "").isEmpty, "\(c)")
        }
        #expect(ContactBook.errorText(.contactsConsentRequired, account: "petr@firma2.cz")
            == "Microsoft needs your permission to access the contacts of petr@firma2.cz.")
        #expect(ContactBook.errorText(.networkError, account: "") == nil, "network error has its own sentence")
        let q = ContactBook.deleteQuestion(name: "Jana Nováková", account: "Microsoft 365 (jana@firma.cz)")
        #expect(q.heading == "Delete contact Jana Nováková?" && q.destructive && q.body.contains("Microsoft 365 (jana@firma.cz)"))
    }

    @Test func fold() {
        let cases: [(Double, ContactBook.Fold)] = [
            (900, ContactBook.Fold()), (719, ContactBook.Fold(sidebarCollapsed: true)),
            (519, ContactBook.Fold(sidebarCollapsed: true, detailOverList: true)), (720, ContactBook.Fold()),
        ]
        for (w, want) in cases {
            #expect(ContactBook.foldFor(width: w, ContactBook.macBreakpoints) == want, "\(w)")
        }
        #expect(ContactBook.foldFor(width: 800, ContactBook.gtkBreakpoints).sidebarCollapsed)
    }
}

// MARK: - form_test.go

@Suite struct ContactBookFormTests {
    @Test func editFormUnchanged() {
        var f = ContactBook.Form.edit(contactBookCard())
        #expect(!f.dirty && !f.isNew)
        let out = f.card
        #expect(out.displayName == "Jana Nováková" && out.id == "p_1" && out.revision == "r1")
        // Whitespace alone is no change.
        f.given = " Jana "
        #expect(!f.dirty)
    }

    @Test func editFormChangesAndDisplayName() {
        var f = ContactBook.Form.edit(contactBookCard())
        f.family = "Malá"
        f.emails.append(ContactBook.LabelledValue(label: .other, value: "  "))
        #expect(f.changed == [.family])
        let c = f.card
        #expect(c.displayName == "Jana Malá" && c.emails.count == 2 && c.addresses.count == 1)
    }

    @Test func displayNameOnlyCardKeepsItsName() {
        let c = ContactCard(id: "p", displayName: "Pavel Novák", phones: [ContactPhone(number: "1")])
        var f = ContactBook.Form.edit(c)
        #expect(f.given == "Pavel" && f.family == "Novák" && !f.dirty)
        f.phones[0].value = "2"
        let out = f.card
        #expect(out.name == ContactName() && out.displayName == "Pavel Novák", "an unchanged name changed")
    }

    @Test func newFormCard() {
        var f = ContactBook.Form.new(book: "b_m1")
        #expect(!f.dirty)
        f.org = "Acme"
        let c = f.card
        #expect(c.bookId == "b_m1" && c.displayName == "Acme")
        let wire = try? JSONSerialization.jsonObject(with: JSONCoding.encoder().encode(c)) as? [String: Any]
        #expect(wire?["emails"] is [Any] && wire?["urls"] is [Any], "lists are sent as arrays")
        f.org = ""
        f.emails = [ContactBook.LabelledValue(value: "a@b.cz")]
        #expect(f.card.displayName == "a@b.cz")
    }

    @Test func rows() {
        var f = ContactBook.Form.new(book: "b")
        let firstRow = f.addRow(.email)
        #expect(firstRow == 0 && f.emails[0].label == .work)
        f.addRow(.email)
        f.addRow(.email)
        f.addRow(.email)
        #expect([f.emails[1].label, f.emails[2].label, f.emails[3].label] == [.home, .other, .other])
        f.addRow(.phone)
        #expect(f.phones[0].label == .mobile)
        f.addRow(.address)
        f.addRow(.url)
        f.removeRow(.email, at: 0)
        f.removeRow(.email, at: 9)
        #expect(f.count(.email) == 3 && f.emails[0].label == .home && f.count(.address) == 1 && f.count(.url) == 1)
        while f.canAdd(.url) {
            f.addRow(.url)
        }
        let overLimit = f.addRow(.url)
        #expect(overLimit == nil && f.count(.url) == API.Limits.maxContactURLs)
        f.removeRow(.phone, at: 0)
        f.removeRow(.address, at: 0)
        f.removeRow(.url, at: 0)
    }

    @Test func validate() {
        var f = ContactBook.Form.new(book: "b")
        let first = f.validate()
        #expect(first.count == 1 && first.first?.text == "Enter a name, a company or an email address.")
        f.given = "Jana"
        f.emails = ["jana@example.org", "Jana <j@x.cz>", "nope", "", "a@localhost"].map { ContactBook.LabelledValue(value: $0) }
        f.urls = ["example.org", "javascript:alert(1)", "https://ok.cz"].map { ContactBook.LabelledValue(value: $0) }
        f.birthday = ContactBirthday(month: 2, day: 30)
        f.note = String(repeating: "x", count: API.Limits.maxContactNoteBytes + 1)
        f.org = String(repeating: "o", count: API.Limits.maxContactFieldBytes + 1)
        #expect(f.validate().map(\.text) == [
            "This text is too long.", "Jana <j@x.cz> is not a valid email address.", "nope is not a valid email address.",
            "a@localhost is not a valid email address.", "A website must be an address starting with http:// or https://.",
            "Enter a valid date.", "This text is too long.",
        ])
        f = ContactBook.Form.new(book: "b")
        f.emails = [ContactBook.LabelledValue(value: "jana@example.org")]
        #expect(f.validate().isEmpty, "an address alone is enough")
    }

    /// form_test.go TestValidateGraphLimits: a Microsoft book holds at most
    /// 3 e-mail addresses, one address per slot (other and custom labels
    /// share one) and one website; the problem names the first row over,
    /// counting only filled rows. Other books do not have these limits.
    @Test func validateGraphLimits() {
        var f = ContactBook.Form.new(book: "b")
        f.given = "Jana"
        f.emails = ["a@b.cz", "", "c@b.cz", "d@b.cz", "e@b.cz"].map { ContactBook.LabelledValue(value: $0) }
        f.addresses = [ContactAddress(label: .work, city: "A"), ContactAddress(label: "custom", city: "B"),
                       ContactAddress(city: "C"), ContactAddress(label: .home, city: "D")]
        f.urls = ["a.cz", "b.cz"].map { ContactBook.LabelledValue(value: $0) }
        #expect(f.validate().isEmpty, "a CardDAV book")
        f.kind = .graph
        var got: [ContactBook.FieldKey: Int?] = [:]
        for p in f.validate() {
            #expect(got[p.key] == nil, "two problems of \(p.key)")
            got[p.key] = p.index
        }
        #expect(got == [.emails: 4, .addresses: 2, .urls: 1])
        f.emails = Array(f.emails.prefix(4))
        f.addresses = Array(f.addresses.prefix(2))
        f.urls = Array(f.urls.prefix(1))
        #expect(f.validate().isEmpty, "within the limits")
    }

    /// form_test.go TestProblemOf: the field from the error's data; the
    /// message is never read, and anything it cannot read is no problem.
    @Test func problemOf() {
        func inv(_ field: String) -> RPCError {
            RPCError(code: .invalidArgument, message: "card.org: misleading", data: .object(["field": .string(field)]))
        }
        let cases: [(String, ContactBook.FieldKey, Int?)] = [
            ("emails[2].address", .emails, 2), ("emails", .emails, nil), ("addresses[1]", .addresses, 1),
            ("phones[0].label", .phones, 0), ("name.family", .family, nil), ("urls", .urls, nil), ("note", .note, nil),
            ("title", .title, nil),
        ]
        for (field, key, index) in cases {
            let p = ContactBook.problemOf(inv(field))
            #expect(p?.key == key && p?.index == index && p?.text == ContactBook.refusedText, "\(field)")
        }
        let bad: [(any Error)?] = [
            nil, RPCError(code: .invalidArgument, message: "card.org: no data"),
            RPCError(code: .offline, message: "x", data: .object(["field": .string("org")])),
            RPCError(code: .invalidArgument, message: "x", data: .string("org")),
            inv(""), inv("card.org"), inv("colour"), inv("emails[x].address"), inv("emails[-1]"), inv("emails[2"), inv("name.nick"),
        ]
        for e in bad {
            #expect(ContactBook.problemOf(e) == nil)
        }
    }

    @Test func rebaseKeepsTheUsersChanges() {
        var f = ContactBook.Form.edit(contactBookCard())
        f.title = "Ředitelka"
        f.phones = [ContactBook.LabelledValue(label: .mobile, value: "+420 777")]
        var server = contactBookCard()
        server.revision = "r2"
        server.org = "Novák a syn"
        server.title = "Nákupčí"
        server.phones.append(ContactPhone(number: "3"))
        server.note = "Nová poznámka"
        f.rebase(server)
        #expect(f.org == "Novák a syn" && f.title == "Ředitelka" && f.note == "Nová poznámka" && f.phones.count == 1
            && f.phones[0].value == "+420 777")
        #expect(f.changed == [.title, .phones] && f.card.revision == "r2")
        f.saved(id: nil, revision: "r3")
        #expect(!f.dirty && f.original.revision == "r3" && f.original.title == "Ředitelka")
    }

    @Test func splitName() {
        let cases: [(String, String, String)] = [
            ("Petr Svoboda", "Petr", "Svoboda"), ("Eva", "Eva", ""), ("", "", ""), ("Jan  Amos Komenský", "Jan Amos", "Komenský"),
        ]
        for (input, given, family) in cases {
            let r = ContactBook.splitName(input)
            #expect(r.given == given && r.family == family, "\(input)")
        }
    }

    @Test func addFromChip() {
        let books = contactBookBooks()
        let accs = contactBookAccounts()
        var a = ContactBook.AddFromChip(address: Address(name: "Petr \u{202E}Svoboda", address: "petr.svoboda@example.com"),
                                        account: "acc_dav", books: books, accounts: accs)
        #expect(a.name == "Petr Svoboda" && a.selectName && !a.warning.isEmpty && a.book == "b_dav" && a.canAdd)
        #expect(a.books.map(\.text) == ["Personal (Nextcloud)", "Contacts (jana@firma.cz)", "Archive 2019 (jana@firma.cz)"])
        let f = a.form
        #expect(f.isNew && f.book == "b_dav" && f.given == "Petr" && f.family == "Svoboda"
            && f.emails == [ContactBook.LabelledValue(value: "petr.svoboda@example.com")])
        // No name: nothing to warn about; an IMAP message goes to the first
        // visible writable book.
        a = ContactBook.AddFromChip(address: Address(name: "x@y.cz", address: "x@y.cz"), account: "acc_imap", books: books, accounts: accs)
        #expect(a.name == "" && !a.selectName && a.warning == "" && a.book == "b_dav")
        #expect(ContactBook.defaultBook(books, selected: "b_m1", account: nil) == "b_m1")
        #expect(ContactBook.defaultBook(books, selected: "b_m2", account: "acc_m365") == "b_m1")
        #expect(ContactBook.defaultBook(books, selected: nil, account: "acc_m365") == "b_m1")
        let only = [AddressBook(id: "h", kind: .graph, visible: false)]
        #expect(ContactBook.defaultBook(only, selected: nil, account: nil) == "h", "hidden fallback")
        #expect(ContactBook.defaultBook([], selected: nil, account: nil) == nil)
        #expect(ContactBook.chipItemFor(known: true, books: []) == .show)
        #expect(ContactBook.chipItemFor(known: false, books: books) == .add)
        #expect(ContactBook.chipItemFor(known: false, books: [books[2]]) == ContactBook.ChipItem.none)
        #expect(ContactBook.chipItemText(.show) == "Show Contact" && ContactBook.chipItemText(.add) == "Add to Contacts…")
        #expect(ContactBook.chipItemText(ContactBook.ChipItem.none) == "")
        a.address = "bad"
        #expect(!a.canAdd)
    }
}
