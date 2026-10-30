// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The address books' wire types (docs/api.md §4.19,
/// backend/pkg/api/contacts_book.go) with invented data: optional fields
/// absent and present, Go's omitempty on the way out, the open enums, the
/// error data, the account additions and the methods' names.
@Suite struct APICodingContactsTests {
    private func decode<T: Decodable>(_ type: T.Type, _ s: String) throws -> T {
        try JSONCoding.decoder().decode(type, from: json(s))
    }

    private func encodeObject<T: Encodable>(_ v: T) throws -> [String: Any] {
        let data = try JSONCoding.encoder().encode(v)
        return try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    private func roundTrip<T: Codable & Equatable>(_ v: T) throws -> T {
        try JSONCoding.decoder().decode(T.self, from: JSONCoding.encoder().encode(v))
    }

    @Test func books() throws {
        let r = try decode(AddressBookListResult.self, #"""
        {"books":[{"id":"b_1","accountId":"acc_1","name":"Contacts","kind":"graph","readOnly":false,"visible":true,"count":12,
          "lastSyncAt":"2026-10-30T10:00:00Z"},
         {"id":"recent","name":"","kind":"recent","readOnly":true,"visible":true,"count":3},
         {"id":"b_2","accountId":"acc_2","name":"Shared","kind":"jmap","readOnly":true,"searchOnly":true,"visible":false,"count":0}]}
        """#)
        #expect(r.books.count == 3)
        let b = r.books[0]
        #expect(b.id == "b_1" && b.accountId == "acc_1" && b.kind == .graph && !b.searchOnly && b.lastSyncAt == RFC3339.parse("2026-10-30T10:00:00Z"))
        #expect(r.books[1].id == .recent && r.books[1].accountId == nil && r.books[1].kind == .recent)
        #expect(r.books[2].kind.rawValue == "jmap" && r.books[2].searchOnly, "an open enum")
        #expect(try roundTrip(r) == r)
        let wire = try encodeObject(r.books[1])
        #expect(wire["accountId"] == nil && wire["searchOnly"] == nil && wire["lastSyncAt"] == nil, "omitempty")
        #expect(try decode(AddressBookListResult.self, #"{"books":null}"#).books.isEmpty)
        #expect(try encodeObject(AddressBookListParams()).isEmpty)
    }

    @Test func cards() throws {
        let c = try decode(ContactCard.self, #"""
        {"id":"p_1","bookId":"b_1","readOnly":false,"revision":"\"etag-1\"",
         "name":{"given":"Jana","family":"Nováková"},"displayName":"Jana Nováková","org":"Acme",
         "emails":[{"label":"work","address":"jana@example.org"},{"address":"j@example.org"}],
         "phones":[{"label":"Kancelář","number":"+420 1"}],"addresses":null,
         "urls":[{"url":"https://example.org"}],"birthday":{"month":3,"day":14}}
        """#)
        #expect(c.name == ContactName(given: "Jana", family: "Nováková") && c.nickname == "" && c.note == "" && c.title == "")
        #expect(c.emails[0].label == .work && c.emails[1].label.isEmpty && c.phones[0].label.rawValue == "Kancelář")
        #expect(c.addresses.isEmpty && c.birthday == ContactBirthday(month: 3, day: 14) && c.birthday?.year == 0)
        #expect(try roundTrip(c) == c)
        let wire = try encodeObject(c)
        #expect(wire["nickname"] == nil && wire["note"] == nil && wire["title"] == nil, "omitempty strings")
        #expect(wire["addresses"] is [Any] && wire["urls"] is [Any], "lists are always sent")
        let name = try #require(wire["name"] as? [String: Any])
        #expect(name.keys.sorted() == ["family", "given"])
        let email = try #require((wire["emails"] as? [[String: Any]])?[1])
        #expect(email.keys.sorted() == ["address"], "no label left out")
        let bday = try #require(wire["birthday"] as? [String: Any])
        #expect(bday.keys.sorted() == ["day", "month"], "year 0 left out")
        #expect(try decode(ContactCard.self, #"{"id":"p","bookId":"b"}"#).emails.isEmpty)
    }

    @Test func listAndLookup() throws {
        let l = try decode(ContactListResult.self, #"""
        {"contacts":[{"id":"p_1","bookId":"b_1","displayName":"Jana","sortKey":"jana","section":"J","email":"jana@example.org"}],"total":7}
        """#)
        #expect(l.total == 7 && l.contacts[0].org == "" && l.contacts[0].email == "jana@example.org")
        #expect(try encodeObject(l.contacts[0])["org"] == nil)
        let p = try encodeObject(ContactListParams(bookId: .all))
        #expect(p.keys.sorted() == ["bookId"] && p["bookId"] as? String == "all")
        let k = try decode(ContactLookupResult.self, #"{"known":{"jana@example.org":"p_1"}}"#)
        #expect(k.known == ["jana@example.org": "p_1"])
        #expect(try decode(ContactLookupResult.self, #"{"known":null}"#).known.isEmpty)
        let n = try decode(ContactsChangedNotification.self, #"{"accountId":"acc_1"}"#)
        #expect(n.accountId == "acc_1" && n.bookIds.isEmpty)
    }

    @Test func accountsAndSearch() throws {
        let g = try decode(GraphConfig.self, #"{"source":"daemon","contacts":{"sync":true}}"#)
        #expect(g.contacts == ContactsConfig(sync: true))
        #expect(try encodeObject(GraphConfig(source: .daemon)).keys.sorted() == ["source"])
        let d = try decode(CalDAVConfig.self, #"{"serverUrl":"https://x.example","username":"u","addressBookHomeSetUrl":"/ab/","contacts":true}"#)
        #expect(d.contacts && d.addressBookHomeSetUrl == "/ab/")
        #expect(try encodeObject(CalDAVConfig(serverUrl: "https://x.example", username: "u"))["contacts"] == nil)
        let found = try decode(AccountDiscoverCalDAVResult.self,
                               #"{"serverUrl":"https://x","foundVia":"wellKnown","needsCredentials":true,"addressBooks":true}"#)
        #expect(found.addressBooks && !found.calendars)
        let listed = try decode(AccountListCalDAVCalendarsResult.self, #"""
        {"principalUrl":"/p/","homeSetUrl":"/c/","scheduling":"client","calendars":[],
         "addressBookHomeSetUrl":"/ab/","addressBooks":[{"href":"/ab/1/","name":"Personal"},{"href":"/ab/2/","name":"Shared","readOnly":true}]}
        """#)
        #expect(listed.addressBooks == [CardDAVAddressBook(href: "/ab/1/", name: "Personal"),
                                        CardDAVAddressBook(href: "/ab/2/", name: "Shared", readOnly: true)])
        #expect(try decode(AccountListCalDAVCalendarsResult.self,
                           #"{"principalUrl":"p","homeSetUrl":"h","scheduling":"server","calendars":null}"#).addressBooks.isEmpty)
        #expect(try encodeObject(AccountOAuthStartParams(accountId: "acc_1", contacts: true))["contacts"] as? Bool == true)
        let s = try encodeObject(ContactSearchParams(accountId: "acc_1", query: "ja", scope: .all))
        #expect(s["scope"] as? String == "all")
        #expect(try encodeObject(ContactSearchParams(accountId: "acc_1", query: "ja"))["scope"] == nil)
        let hit = try decode(Contact.self, #"{"name":"Jana","address":"jana@example.org","source":"contacts","book":"Contacts","contactId":"p_1"}"#)
        #expect(hit.source == .contacts && hit.contactId == "p_1")
        #expect(Capability.contacts.rawValue == "contacts" && ContactSource.contacts.rawValue == "contacts")
    }

    @Test func errorData() throws {
        let consent = RPCError(code: .contactsConsentRequired, message: "x",
                               data: .object(["accountId": .string("acc_1"), "signIn": .bool(false)]))
        #expect(consent.contactsConsentRequired == ContactsConsentRequiredData(accountId: "acc_1", signIn: false))
        #expect(RPCError(code: .calendarConsentRequired, message: "x", data: consent.data).contactsConsentRequired == nil)
        let conflict = try decode(RPCError.self, #"""
        {"code":1308,"message":"x","data":{"server":{"id":"p_1","bookId":"b_1","readOnly":false,"revision":"r2","name":{},
          "displayName":"Jana","emails":[],"phones":[],"addresses":[],"urls":[]}}}
        """#)
        #expect(conflict.contactConflict?.server.revision == "r2")
        #expect(RPCError(code: .contactConflict, message: "x").contactConflict == nil)
        #expect(ErrorCode.contactConflict.name == "contactConflict" && ErrorCode.addressBookReadOnly.rawValue == 1509)
    }

    @Test func methods() {
        #expect(API.contactsMethods.map { $0.name } == [
            "addressBook.list", "addressBook.set", "contact.list", "contact.get", "contact.lookup",
            "contact.create", "contact.update", "contact.delete",
        ])
        #expect(API.ContactCreate.timeout == .seconds(60) && API.ContactList.timeout == RPCTimeouts.default)
        #expect(API.Notify.contactsChanged == "notify.contactsChanged")
    }
}
