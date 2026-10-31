// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Address books and contacts of connected services (docs/api.md §4.19;
// backend/pkg/api/contacts_book.go; the design is
// docs/plans/address-book.md). The daemon keeps a cache of the server's
// address books (Microsoft Graph contact folders, CardDAV collections) and
// a virtual read-only book of recently used addresses; writes go to the
// server first. Every string of these types that comes from a server or a
// vCard (names, organisations, labels, notes, book names) is untrusted
// display text: show it with `stringValue` / plain text only. The client
// never imports Apple's Contacts framework: these are the daemon's books,
// not the system's.
//
// A Go `omitempty` string of a contact ("" = none) is a plain `String`
// here that reads "" when absent and is left out when empty, so that the
// pure logic (`ContactBook`) can follow the Go reference line by line.

import Foundation

// MARK: Identifiers and enumerations

/// api.AddressBookID: an address book (`b_…`), opaque; `.recent` is the
/// virtual book of recently used addresses, `.all` in contact.list every
/// visible book.
public struct AddressBookID: OpaqueID {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// api.AddressBookRecent.
    public static let recent: AddressBookID = "recent"
    /// api.AddressBookAll (contact.list only).
    public static let all: AddressBookID = "all"
}

/// api.ContactID: a contact (`p_…`), opaque. A contact of the recent book
/// has one too (`r_…`), valid for contact.get only.
public struct ContactID: OpaqueID {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }
}

/// api.AddressBookKind: where an address book comes from. An open enum: an
/// unknown value is a read-only book of an unknown source.
public struct AddressBookKind: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// A Microsoft Graph contact folder.
    public static let graph: AddressBookKind = "graph"
    /// A CardDAV collection of a caldav account.
    public static let carddav: AddressBookKind = "carddav"
    /// `AddressBookID.recent`.
    public static let recent: AddressBookKind = "recent"
}

/// api.ContactLabel: the label of an e-mail address, phone, postal address
/// or URL. An open enum: any other value is the server's own label
/// (untrusted display text, ≤ `maxContactLabelBytes`); "" (`.none`) is no
/// label.
public struct ContactLabel: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// No label (Go's "").
    public static let none = ContactLabel(rawValue: "")
    public static let home = ContactLabel(rawValue: "home")
    public static let work = ContactLabel(rawValue: "work")
    public static let other = ContactLabel(rawValue: "other")
    /// Phones.
    public static let mobile = ContactLabel(rawValue: "mobile")
    public static let fax = ContactLabel(rawValue: "fax")
    public static let pager = ContactLabel(rawValue: "pager")
    public static let main = ContactLabel(rawValue: "main")

    public var isEmpty: Bool { rawValue.isEmpty }
}

/// api.ContactSearchScope: which address books contact.search reads (the
/// setting contacts-suggest-scope).
public struct ContactSearchScope: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// The books of the sending account and the recent book (the default).
    public static let account: ContactSearchScope = "account"
    /// Every visible book of every enabled account.
    public static let all: ContactSearchScope = "all"
}

/// api.ContactPhotoAvailability: what `ContactSummary.photo`,
/// `ContactCard.photo` and contact.lookup's `photos` say of a contact's
/// photo; "" (`.none`, the field absent) is no photo. An open enum: an
/// unknown value is no photo.
public struct ContactPhotoAvailability: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// No photo (Go's "").
    public static let none = ContactPhotoAvailability(rawValue: "")
    /// The daemon has the photo; contact.photo returns it at once.
    public static let ready: ContactPhotoAvailability = "ready"
    /// A Microsoft 365 contact whose photo, if it has one, has not been
    /// fetched for its current revision; contact.photo fetches it (and may
    /// find none).
    public static let remote: ContactPhotoAvailability = "remote"

    public var isEmpty: Bool { rawValue.isEmpty }
}

/// api.ContactPhotoState: contact.photo's answer. An open enum: an unknown
/// value is no photo.
public struct ContactPhotoState: WireEnum {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// `mime` and `data` hold the photo.
    public static let ready: ContactPhotoState = "ready"
    /// No photo, or one the daemon refused.
    public static let none: ContactPhotoState = "none"
}

// MARK: Coding helpers

extension KeyedDecodingContainer {
    /// A Go `omitempty` string: absent or null reads as "".
    func stringOrEmpty(_ key: Key) throws -> String {
        try decodeIfPresent(String.self, forKey: key) ?? ""
    }

    /// A Go `omitempty` label: absent reads as `.none`.
    func labelOrNone(_ key: Key) throws -> ContactLabel {
        try decodeIfPresent(ContactLabel.self, forKey: key) ?? .none
    }
}

extension KeyedEncodingContainer {
    /// Go's `omitempty` of a string: "" is left out.
    mutating func encodeNonEmpty(_ s: String, forKey key: Key) throws {
        if !s.isEmpty {
            try encode(s, forKey: key)
        }
    }

    mutating func encodeNonEmpty(_ l: ContactLabel, forKey key: Key) throws {
        if !l.isEmpty {
            try encode(l, forKey: key)
        }
    }
}

// MARK: Accounts

/// api.ContactsConfig: the contacts part of a graph account
/// (`GraphConfig.contacts`); a caldav account uses `CalDAVConfig.contacts`.
public struct ContactsConfig: Codable, Sendable, Equatable {
    /// Synchronise the account's contact folders; default false.
    public var sync: Bool

    public init(sync: Bool) {
        self.sync = sync
    }
}

/// api.CardDAVAddressBook: an address book collection of a CardDAV server
/// (account.listCalDAVCalendars).
public struct CardDAVAddressBook: Codable, Sendable, Equatable {
    public var href: String
    /// Untrusted display text.
    public var name: String
    @DefaultFalse public var readOnly: Bool

    public init(href: String, name: String, readOnly: Bool = false) {
        self.href = href
        self.name = name
        self.readOnly = readOnly
    }
}

// MARK: Error data

/// api.ContactsConsentRequiredData: `error.data` of
/// `contactsConsentRequired`. `signIn`: `account.oauthStart` with
/// `contacts` can fix it; false for GOA.
public struct ContactsConsentRequiredData: Codable, Sendable, Equatable {
    public var accountId: AccountID
    public var signIn: Bool

    public init(accountId: AccountID, signIn: Bool) {
        self.accountId = accountId
        self.signIn = signIn
    }
}

/// api.ContactConflictData: `error.data` of `contactConflict`, the server's
/// current version of the contact.
public struct ContactConflictData: Codable, Sendable, Equatable {
    public var server: ContactCard

    public init(server: ContactCard) {
        self.server = server
    }
}

/// api.ContactFieldData: `error.data` of an `invalidArgument` from
/// contact.create and contact.update naming the refused field of the card
/// as a path relative to it ("emails[2].address", "name.given",
/// "addresses[0]", "emails"); the message is not for display.
public struct ContactFieldData: Codable, Sendable, Equatable {
    public var field: String

    public init(field: String) {
        self.field = field
    }
}

extension RPCError {
    /// The refused field of an `invalidArgument`; nil for any other code
    /// or data of another shape.
    public var contactField: ContactFieldData? {
        guard code == .invalidArgument else { return nil }
        return contactsData(ContactFieldData.self)
    }

    /// The data of a `contactsConsentRequired` error; nil for any other
    /// code or data of another shape.
    public var contactsConsentRequired: ContactsConsentRequiredData? {
        guard code == .contactsConsentRequired else { return nil }
        return contactsData(ContactsConsentRequiredData.self)
    }

    /// The server's card of a `contactConflict`; nil for any other code or
    /// data of another shape.
    public var contactConflict: ContactConflictData? {
        guard code == .contactConflict else { return nil }
        return contactsData(ContactConflictData.self)
    }

    private func contactsData<T: Decodable>(_ type: T.Type) -> T? {
        guard let data, data.objectValue != nil, let raw = try? JSONCoding.encoder().encode(data) else { return nil }
        return try? JSONCoding.decoder().decode(type, from: raw)
    }
}

// MARK: Common types

/// api.AddressBook: one address book.
public struct AddressBook: Codable, Sendable, Equatable {
    public var id: AddressBookID
    /// nil for the recent book.
    public var accountId: AccountID?
    /// Untrusted display text.
    public var name: String
    public var kind: AddressBookKind
    public var readOnly: Bool
    /// The book cannot be listed whole, only searched (no kind of stage 1
    /// sets it).
    @DefaultFalse public var searchOnly: Bool
    /// The user's choice (addressBook.set); hidden books are neither listed
    /// by contact.list `all` nor searched.
    public var visible: Bool
    public var count: Int
    public var lastSyncAt: Date?

    public init(
        id: AddressBookID, accountId: AccountID? = nil, name: String = "", kind: AddressBookKind, readOnly: Bool = false,
        searchOnly: Bool = false, visible: Bool = true, count: Int = 0, lastSyncAt: Date? = nil
    ) {
        self.id = id
        self.accountId = accountId
        self.name = name
        self.kind = kind
        self.readOnly = readOnly
        self.searchOnly = searchOnly
        self.visible = visible
        self.count = count
        self.lastSyncAt = lastSyncAt
    }

    private enum CodingKeys: String, CodingKey {
        case id, accountId, name, kind, readOnly, searchOnly, visible, count, lastSyncAt
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(AddressBookID.self, forKey: .id)
        accountId = try c.decodeIfPresent(AccountID.self, forKey: .accountId)
        name = try c.stringOrEmpty(.name)
        kind = try c.decodeIfPresent(AddressBookKind.self, forKey: .kind) ?? ""
        readOnly = try c.decodeIfPresent(Bool.self, forKey: .readOnly) ?? false
        _searchOnly = try c.decode(DefaultFalse.self, forKey: .searchOnly)
        visible = try c.decodeIfPresent(Bool.self, forKey: .visible) ?? false
        count = try c.decodeIfPresent(Int.self, forKey: .count) ?? 0
        lastSyncAt = try c.decodeIfPresent(Date.self, forKey: .lastSyncAt)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(id, forKey: .id)
        if let accountId, !accountId.rawValue.isEmpty {
            try c.encode(accountId, forKey: .accountId)
        }
        try c.encode(name, forKey: .name)
        try c.encode(kind, forKey: .kind)
        try c.encode(readOnly, forKey: .readOnly)
        try c.encode(_searchOnly, forKey: .searchOnly)
        try c.encode(visible, forKey: .visible)
        try c.encode(count, forKey: .count)
        try c.encodeIfPresent(lastSyncAt, forKey: .lastSyncAt)
    }
}

/// api.ContactSummary: a row of the contact list.
public struct ContactSummary: Codable, Sendable, Equatable {
    public var id: ContactID
    public var bookId: AddressBookID
    public var displayName: String
    /// Orders the list (family name first when known); `section` is its
    /// index letter, upper case, or "#".
    public var sortKey: String
    public var section: String
    /// "" none.
    public var org: String
    /// The first address; "" none.
    public var email: String
    /// Whether the contact has a photo (contact.photo); `.none` when it has
    /// none. Never set in the recent book.
    public var photo: ContactPhotoAvailability

    public init(
        id: ContactID, bookId: AddressBookID, displayName: String = "", sortKey: String = "", section: String = "",
        org: String = "", email: String = "", photo: ContactPhotoAvailability = .none
    ) {
        self.id = id
        self.bookId = bookId
        self.displayName = displayName
        self.sortKey = sortKey
        self.section = section
        self.org = org
        self.email = email
        self.photo = photo
    }

    private enum CodingKeys: String, CodingKey {
        case id, bookId, displayName, sortKey, section, org, email, photo
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(ContactID.self, forKey: .id)
        bookId = try c.decodeIfPresent(AddressBookID.self, forKey: .bookId) ?? ""
        displayName = try c.stringOrEmpty(.displayName)
        sortKey = try c.stringOrEmpty(.sortKey)
        section = try c.stringOrEmpty(.section)
        org = try c.stringOrEmpty(.org)
        email = try c.stringOrEmpty(.email)
        photo = try c.decodeIfPresent(ContactPhotoAvailability.self, forKey: .photo) ?? .none
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(id, forKey: .id)
        try c.encode(bookId, forKey: .bookId)
        try c.encode(displayName, forKey: .displayName)
        try c.encode(sortKey, forKey: .sortKey)
        try c.encode(section, forKey: .section)
        try c.encodeNonEmpty(org, forKey: .org)
        try c.encodeNonEmpty(email, forKey: .email)
        if !photo.isEmpty {
            try c.encode(photo, forKey: .photo)
        }
    }
}

/// api.ContactName: the structured name (vCard N); "" = none.
public struct ContactName: Codable, Sendable, Hashable {
    public var given: String
    public var family: String
    public var middle: String
    public var prefix: String
    public var suffix: String

    public init(given: String = "", family: String = "", middle: String = "", prefix: String = "", suffix: String = "") {
        self.given = given
        self.family = family
        self.middle = middle
        self.prefix = prefix
        self.suffix = suffix
    }

    private enum CodingKeys: String, CodingKey {
        case given, family, middle, prefix, suffix
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        given = try c.stringOrEmpty(.given)
        family = try c.stringOrEmpty(.family)
        middle = try c.stringOrEmpty(.middle)
        prefix = try c.stringOrEmpty(.prefix)
        suffix = try c.stringOrEmpty(.suffix)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeNonEmpty(given, forKey: .given)
        try c.encodeNonEmpty(family, forKey: .family)
        try c.encodeNonEmpty(middle, forKey: .middle)
        try c.encodeNonEmpty(prefix, forKey: .prefix)
        try c.encodeNonEmpty(suffix, forKey: .suffix)
    }
}

/// api.ContactEmail.
public struct ContactEmail: Codable, Sendable, Hashable {
    public var label: ContactLabel
    public var address: String

    public init(label: ContactLabel = .none, address: String) {
        self.label = label
        self.address = address
    }

    private enum CodingKeys: String, CodingKey {
        case label, address
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        label = try c.labelOrNone(.label)
        address = try c.stringOrEmpty(.address)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeNonEmpty(label, forKey: .label)
        try c.encode(address, forKey: .address)
    }
}

/// api.ContactPhone. A client only copies a number, never dials it.
public struct ContactPhone: Codable, Sendable, Hashable {
    public var label: ContactLabel
    public var number: String

    public init(label: ContactLabel = .none, number: String) {
        self.label = label
        self.number = number
    }

    private enum CodingKeys: String, CodingKey {
        case label, number
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        label = try c.labelOrNone(.label)
        number = try c.stringOrEmpty(.number)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeNonEmpty(label, forKey: .label)
        try c.encode(number, forKey: .number)
    }
}

/// api.ContactAddress: a postal address; `street` may have several lines.
public struct ContactAddress: Codable, Sendable, Hashable {
    public var label: ContactLabel
    public var street: String
    public var city: String
    public var region: String
    public var postcode: String
    public var country: String

    public init(
        label: ContactLabel = .none, street: String = "", city: String = "", region: String = "", postcode: String = "",
        country: String = ""
    ) {
        self.label = label
        self.street = street
        self.city = city
        self.region = region
        self.postcode = postcode
        self.country = country
    }

    private enum CodingKeys: String, CodingKey {
        case label, street, city, region, postcode, country
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        label = try c.labelOrNone(.label)
        street = try c.stringOrEmpty(.street)
        city = try c.stringOrEmpty(.city)
        region = try c.stringOrEmpty(.region)
        postcode = try c.stringOrEmpty(.postcode)
        country = try c.stringOrEmpty(.country)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeNonEmpty(label, forKey: .label)
        try c.encodeNonEmpty(street, forKey: .street)
        try c.encodeNonEmpty(city, forKey: .city)
        try c.encodeNonEmpty(region, forKey: .region)
        try c.encodeNonEmpty(postcode, forKey: .postcode)
        try c.encodeNonEmpty(country, forKey: .country)
    }
}

/// api.ContactURL: a web address; clients open only http and https,
/// through their "Open this link?".
public struct ContactURL: Codable, Sendable, Hashable {
    public var label: ContactLabel
    public var url: String

    public init(label: ContactLabel = .none, url: String) {
        self.label = label
        self.url = url
    }

    private enum CodingKeys: String, CodingKey {
        case label, url
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        label = try c.labelOrNone(.label)
        url = try c.stringOrEmpty(.url)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeNonEmpty(label, forKey: .label)
        try c.encode(url, forKey: .url)
    }
}

/// api.ContactBirthday: `year` 0 = unknown.
public struct ContactBirthday: Codable, Sendable, Hashable {
    @DefaultZero public var year: Int
    public var month: Int
    public var day: Int

    public init(year: Int = 0, month: Int, day: Int) {
        self.year = year
        self.month = month
        self.day = day
    }
}

/// api.ContactCard: a whole contact. As input (contact.create,
/// contact.update) `id`, `bookId`, `readOnly` and `revision` are ignored.
public struct ContactCard: Codable, Sendable, Equatable {
    public var id: ContactID
    public var bookId: AddressBookID
    public var readOnly: Bool
    /// The server's version (CardDAV ETag, Graph changeKey), opaque;
    /// contact.update and contact.delete send it back.
    public var revision: String
    public var name: ContactName
    public var displayName: String
    public var nickname: String
    public var org: String
    public var title: String
    @NullAsEmpty public var emails: [ContactEmail]
    @NullAsEmpty public var phones: [ContactPhone]
    @NullAsEmpty public var addresses: [ContactAddress]
    @NullAsEmpty public var urls: [ContactURL]
    public var birthday: ContactBirthday?
    public var note: String
    /// As `ContactSummary.photo`. Ignored as input: contact.update never
    /// changes a photo, the server keeps it as it is.
    public var photo: ContactPhotoAvailability

    public init(
        id: ContactID = "", bookId: AddressBookID = "", readOnly: Bool = false, revision: String = "",
        name: ContactName = ContactName(), displayName: String = "", nickname: String = "", org: String = "",
        title: String = "", emails: [ContactEmail] = [], phones: [ContactPhone] = [], addresses: [ContactAddress] = [],
        urls: [ContactURL] = [], birthday: ContactBirthday? = nil, note: String = "", photo: ContactPhotoAvailability = .none
    ) {
        self.id = id
        self.bookId = bookId
        self.readOnly = readOnly
        self.revision = revision
        self.name = name
        self.displayName = displayName
        self.nickname = nickname
        self.org = org
        self.title = title
        self.emails = emails
        self.phones = phones
        self.addresses = addresses
        self.urls = urls
        self.birthday = birthday
        self.note = note
        self.photo = photo
    }

    private enum CodingKeys: String, CodingKey {
        case id, bookId, readOnly, revision, name, displayName, nickname, org, title, emails, phones, addresses, urls,
             birthday, note, photo
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(ContactID.self, forKey: .id) ?? ""
        bookId = try c.decodeIfPresent(AddressBookID.self, forKey: .bookId) ?? ""
        readOnly = try c.decodeIfPresent(Bool.self, forKey: .readOnly) ?? false
        revision = try c.stringOrEmpty(.revision)
        name = try c.decodeIfPresent(ContactName.self, forKey: .name) ?? ContactName()
        displayName = try c.stringOrEmpty(.displayName)
        nickname = try c.stringOrEmpty(.nickname)
        org = try c.stringOrEmpty(.org)
        title = try c.stringOrEmpty(.title)
        _emails = try c.decode(NullAsEmpty<ContactEmail>.self, forKey: .emails)
        _phones = try c.decode(NullAsEmpty<ContactPhone>.self, forKey: .phones)
        _addresses = try c.decode(NullAsEmpty<ContactAddress>.self, forKey: .addresses)
        _urls = try c.decode(NullAsEmpty<ContactURL>.self, forKey: .urls)
        birthday = try c.decodeIfPresent(ContactBirthday.self, forKey: .birthday)
        note = try c.stringOrEmpty(.note)
        photo = try c.decodeIfPresent(ContactPhotoAvailability.self, forKey: .photo) ?? .none
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(id, forKey: .id)
        try c.encode(bookId, forKey: .bookId)
        try c.encode(readOnly, forKey: .readOnly)
        try c.encode(revision, forKey: .revision)
        try c.encode(name, forKey: .name)
        try c.encode(displayName, forKey: .displayName)
        try c.encodeNonEmpty(nickname, forKey: .nickname)
        try c.encodeNonEmpty(org, forKey: .org)
        try c.encodeNonEmpty(title, forKey: .title)
        try c.encode(emails, forKey: .emails)
        try c.encode(phones, forKey: .phones)
        try c.encode(addresses, forKey: .addresses)
        try c.encode(urls, forKey: .urls)
        try c.encodeIfPresent(birthday, forKey: .birthday)
        try c.encodeNonEmpty(note, forKey: .note)
        if !photo.isEmpty {
            try c.encode(photo, forKey: .photo)
        }
    }
}

// MARK: addressBook.* and contact.* (§4.19)

/// api.AddressBookListParams: `accountId` nil = every account and the
/// recent book.
public struct AddressBookListParams: Codable, Sendable, Equatable {
    public var accountId: AccountID?

    public init(accountId: AccountID? = nil) {
        self.accountId = accountId
    }
}

public struct AddressBookListResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var books: [AddressBook]

    public init(books: [AddressBook] = []) {
        self.books = books
    }
}

public struct AddressBookSetParams: Codable, Sendable, Equatable {
    public var bookId: AddressBookID
    public var visible: Bool

    public init(bookId: AddressBookID, visible: Bool) {
        self.bookId = bookId
        self.visible = visible
    }
}

public struct AddressBookSetResult: Codable, Sendable, Equatable {
    public var book: AddressBook

    public init(book: AddressBook) {
        self.book = book
    }
}

/// api.ContactListParams: `bookId` a book or `.all`; `query` filters by
/// name, organisation and address (≤ `maxContactQueryBytes`); `limit` nil
/// = `defaultContactListLimit`, clamped to `maxContactListLimit`.
public struct ContactListParams: Codable, Sendable, Equatable {
    public var bookId: AddressBookID
    public var query: String?
    public var offset: Int?
    public var limit: Int?

    public init(bookId: AddressBookID, query: String? = nil, offset: Int? = nil, limit: Int? = nil) {
        self.bookId = bookId
        self.query = query
        self.offset = offset
        self.limit = limit
    }
}

/// api.ContactListResult: by sort key; `total` counts every match.
public struct ContactListResult: Codable, Sendable, Equatable {
    @NullAsEmpty public var contacts: [ContactSummary]
    public var total: Int

    public init(contacts: [ContactSummary] = [], total: Int = 0) {
        self.contacts = contacts
        self.total = total
    }
}

public struct ContactGetParams: Codable, Sendable, Equatable {
    public var contactId: ContactID

    public init(contactId: ContactID) {
        self.contactId = contactId
    }
}

public struct ContactGetResult: Codable, Sendable, Equatable {
    public var contact: ContactCard

    public init(contact: ContactCard) {
        self.contact = contact
    }
}

/// api.ContactLookupParams: at most `maxContactLookupAddresses`;
/// `withPhotos` asks for `photos` too (left out when false).
public struct ContactLookupParams: Codable, Sendable, Equatable {
    public var addresses: [String]
    public var withPhotos: Bool

    public init(addresses: [String], withPhotos: Bool = false) {
        self.addresses = addresses
        self.withPhotos = withPhotos
    }

    private enum CodingKeys: String, CodingKey {
        case addresses, withPhotos
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        addresses = try c.decodeIfPresent([String].self, forKey: .addresses) ?? []
        withPhotos = try c.decodeIfPresent(Bool.self, forKey: .withPhotos) ?? false
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(addresses, forKey: .addresses)
        if withPhotos {
            try c.encode(withPhotos, forKey: .withPhotos)
        }
    }
}

/// api.ContactPhotoRef: the contact whose photo stands for an address.
public struct ContactPhotoRef: Codable, Sendable, Equatable {
    public var contactId: ContactID
    public var state: ContactPhotoAvailability

    public init(contactId: ContactID, state: ContactPhotoAvailability) {
        self.contactId = contactId
        self.state = state
    }

    private enum CodingKeys: String, CodingKey {
        case contactId, state
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        contactId = try c.decodeIfPresent(ContactID.self, forKey: .contactId) ?? ""
        state = try c.decodeIfPresent(ContactPhotoAvailability.self, forKey: .state) ?? .none
    }
}

/// api.ContactLookupResult: each address with a contact in a visible book
/// (normalised, lower case) to that contact; unknown addresses are absent.
/// `photos` (only with `withPhotos`; absent when empty) maps each address
/// that a contact of a visible book with a photo (ready or remote) has to
/// that contact (several: the first visible by sort key).
public struct ContactLookupResult: Codable, Sendable, Equatable {
    public var known: [String: ContactID]
    public var photos: [String: ContactPhotoRef]

    public init(known: [String: ContactID] = [:], photos: [String: ContactPhotoRef] = [:]) {
        self.known = known
        self.photos = photos
    }

    private enum CodingKeys: String, CodingKey {
        case known, photos
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        known = try c.decodeIfPresent([String: ContactID].self, forKey: .known) ?? [:]
        photos = try c.decodeIfPresent([String: ContactPhotoRef].self, forKey: .photos) ?? [:]
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(known, forKey: .known)
        if !photos.isEmpty {
            try c.encode(photos, forKey: .photos)
        }
    }
}

/// api.ContactPhotoParams.
public struct ContactPhotoParams: Codable, Sendable, Equatable {
    public var contactId: ContactID

    public init(contactId: ContactID) {
        self.contactId = contactId
    }
}

/// api.ContactPhotoResult: a contact's photo. `revision` is the contact's
/// version the photo was made from (`ContactCard.revision`). `mime` and
/// `data` (base64 on the wire) are set only when `state` is `.ready`: the
/// daemon's own JPEG or PNG (`maxContactPhotoBytes`,
/// `maxContactPhotoPixels`), never the server's bytes.
public struct ContactPhotoResult: Codable, Sendable, Equatable {
    public var state: ContactPhotoState
    /// "" none.
    public var mime: String
    /// Empty none.
    public var data: Data
    public var revision: String

    public init(state: ContactPhotoState, mime: String = "", data: Data = Data(), revision: String = "") {
        self.state = state
        self.mime = mime
        self.data = data
        self.revision = revision
    }

    private enum CodingKeys: String, CodingKey {
        case state, mime, data, revision
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        state = try c.decodeIfPresent(ContactPhotoState.self, forKey: .state) ?? .none
        mime = try c.stringOrEmpty(.mime)
        data = try c.decodeIfPresent(Data.self, forKey: .data) ?? Data()
        revision = try c.stringOrEmpty(.revision)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(state, forKey: .state)
        try c.encodeNonEmpty(mime, forKey: .mime)
        if !data.isEmpty {
            try c.encode(data, forKey: .data)
        }
        try c.encode(revision, forKey: .revision)
    }
}

public struct ContactCreateParams: Codable, Sendable, Equatable {
    public var bookId: AddressBookID
    public var card: ContactCard

    public init(bookId: AddressBookID, card: ContactCard) {
        self.bookId = bookId
        self.card = card
    }
}

public struct ContactCreateResult: Codable, Sendable, Equatable {
    public var contactId: ContactID
    public var revision: String

    public init(contactId: ContactID, revision: String) {
        self.contactId = contactId
        self.revision = revision
    }
}

public struct ContactUpdateParams: Codable, Sendable, Equatable {
    public var contactId: ContactID
    public var revision: String
    public var card: ContactCard

    public init(contactId: ContactID, revision: String, card: ContactCard) {
        self.contactId = contactId
        self.revision = revision
        self.card = card
    }
}

public struct ContactUpdateResult: Codable, Sendable, Equatable {
    public var revision: String

    public init(revision: String) {
        self.revision = revision
    }
}

public struct ContactDeleteParams: Codable, Sendable, Equatable {
    public var contactId: ContactID
    public var revision: String

    public init(contactId: ContactID, revision: String) {
        self.contactId = contactId
        self.revision = revision
    }
}

// MARK: Notification

/// api.ContactsChangedNotification (`notify.contactsChanged`): the contacts
/// of these books changed (a pass, a write, a book added or removed,
/// addressBook.set). `accountId` nil for the recent book; no `bookIds` =
/// any book of the account. `DaemonNotification` reads it as `.unknown`;
/// the application's hub decodes it with `decode(_:)`.
public struct ContactsChangedNotification: Codable, Sendable, Equatable {
    public var accountId: AccountID?
    @NullAsEmpty public var bookIds: [AddressBookID]

    public init(accountId: AccountID? = nil, bookIds: [AddressBookID] = []) {
        self.accountId = accountId
        self.bookIds = bookIds
    }

    /// contactsctl.ContactsChanged: the notification's params; nil for
    /// another notification. Missing or null params mean any book. Throws
    /// when the params have the wrong shape.
    public static func decode(_ raw: RPCNotification) throws -> ContactsChangedNotification? {
        guard raw.method == API.Notify.contactsChanged else { return nil }
        let e = try JSONCoding.decoder().decode(OptionalParamsEnvelope<ContactsChangedNotification>.self, from: raw.line)
        return e.params ?? ContactsChangedNotification()
    }
}

// MARK: Methods

extension API {
    /// Lists the books; a pass runs first when the last is older than a
    /// minute (it answers from the cache meanwhile).
    public enum AddressBookList: RPCMethod {
        public typealias Params = AddressBookListParams
        public typealias Result = AddressBookListResult
        public static let name = "addressBook.list"
    }

    public enum AddressBookSet: RPCMethod {
        public typealias Params = AddressBookSetParams
        public typealias Result = AddressBookSetResult
        public static let name = "addressBook.set"
    }

    public enum ContactList: RPCMethod {
        public typealias Params = ContactListParams
        public typealias Result = ContactListResult
        public static let name = "contact.list"
    }

    public enum ContactGet: RPCMethod {
        public typealias Params = ContactGetParams
        public typealias Result = ContactGetResult
        public static let name = "contact.get"
    }

    public enum ContactLookup: RPCMethod {
        public typealias Params = ContactLookupParams
        public typealias Result = ContactLookupResult
        public static let name = "contact.lookup"
    }

    /// The writes wait for the server (contactsctl.WriteTimeout).
    public enum ContactCreate: RPCMethod {
        public typealias Params = ContactCreateParams
        public typealias Result = ContactCreateResult
        public static let name = "contact.create"
        public static let timeout: Duration = .seconds(60)
    }

    public enum ContactUpdate: RPCMethod {
        public typealias Params = ContactUpdateParams
        public typealias Result = ContactUpdateResult
        public static let name = "contact.update"
        public static let timeout: Duration = .seconds(60)
    }

    public enum ContactDelete: RPCMethod {
        public typealias Params = ContactDeleteParams
        public typealias Result = EmptyResult
        public static let name = "contact.delete"
        public static let timeout: Duration = .seconds(60)
    }

    /// A contact's photo; a Microsoft 365 one is downloaded first
    /// (`contactPhotoTimeout`), so the call is given more
    /// (contactsctl.PhotoTimeout).
    public enum ContactPhoto: RPCMethod {
        public typealias Params = ContactPhotoParams
        public typealias Result = ContactPhotoResult
        public static let name = "contact.photo"
        public static let timeout: Duration = .seconds(20)
    }

    /// The address-book methods, in the order of methods.go.
    static let contactsMethods: [any RPCMethod.Type] = [
        AddressBookList.self, AddressBookSet.self,
        ContactList.self, ContactGet.self, ContactLookup.self,
        ContactCreate.self, ContactUpdate.self, ContactDelete.self,
        ContactPhoto.self,
    ]
}

// MARK: Limits

extension API.Limits {
    /// docs/api.md §4.19 (pkg/api/contacts_book.go).
    public static let defaultContactListLimit = 100
    public static let maxContactListLimit = 500
    public static let maxVCardBytes = 1 << 20
    public static let maxVCardProperties = 500
    public static let maxContactLookupAddresses = 200
    public static let maxAddressBooksPerAccount = 200
    public static let maxContactsPerBook = 50000
    /// Each `ContactName` part, `displayName`, `nickname`.
    public static let maxContactNameBytes = 256
    public static let maxContactFieldBytes = 1024
    public static let maxContactLabelBytes = 64
    public static let maxContactNoteBytes = 32 << 10
    public static let maxContactEmails = 50
    public static let maxContactPhones = 50
    public static let maxContactAddresses = 20
    public static let maxContactURLs = 20
    /// A photo is the daemon's own encoding: JPEG (PNG with transparency),
    /// a square of at most `maxContactPhotoPixels` a side, at most
    /// `maxContactPhotoBytes`.
    public static let maxContactPhotoBytes = 64 << 10
    public static let maxContactPhotoPixels = 256
    /// What contact.photo gives a Microsoft 365 download.
    public static let contactPhotoTimeout: Duration = .seconds(15)
}
