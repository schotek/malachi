// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// ui/internal/contacts form.go: the contact form (W4), edited on the
// detail's card and saved to the server with Done. The form keeps the card
// it was opened from (`original`) and knows which fields the user changed
// (`changed`), so that Done sends a card the daemon turns into a patch, a
// contactConflict reloads the server's version under the user's changes
// (`rebase`), and closing an unchanged form asks nothing (`dirty`).

extension ContactBook {
    /// contacts.FieldKey: a part of the form for dirty tracking, rebase and
    /// validation problems.
    public enum FieldKey: Int, Sendable, CaseIterable, Comparable {
        case given, family, middle, prefix, suffix, nickname, org, title
        case emails, phones, addresses, urls, birthday, note

        public static func < (a: FieldKey, b: FieldKey) -> Bool { a.rawValue < b.rawValue }
    }

    /// contacts.LabelledValue: an e-mail address, phone or website row.
    public struct LabelledValue: Sendable, Hashable {
        public var label: ContactLabel
        public var value: String

        public init(label: ContactLabel = .none, value: String = "") {
            self.label = label
            self.value = value
        }
    }

    /// contacts.Form0: the comparable snapshot of a form's fields.
    struct Snapshot: Sendable, Equatable {
        var strings: [String]
        var emails: [LabelledValue]
        var phones: [LabelledValue]
        var addresses: [ContactAddress]
        var urls: [LabelledValue]
        var birthday: ContactBirthday?
        var note: String
    }

    /// contacts.Form: the state of the contact form. A value: the
    /// controller keeps the open one (`ContactsDetailController.form`) and
    /// the view edits it through `ContactsDetailController.updateForm`.
    public struct Form: Sendable, Equatable {
        /// Where a new contact is created (ignored for an update).
        public var book: AddressBookID?

        public var given = ""
        public var family = ""
        public var middle = ""
        public var prefix = ""
        public var suffix = ""
        public var nickname = ""
        public var org = ""
        public var title = ""
        public var emails: [LabelledValue] = []
        public var phones: [LabelledValue] = []
        public var addresses: [ContactAddress] = []
        public var urls: [LabelledValue] = []
        /// nil = none.
        public var birthday: ContactBirthday?
        public var note = ""

        /// The card the form was opened from (empty for a new contact);
        /// its display name stays when no name part changed.
        public internal(set) var original = ContactCard()
        /// Done creates (contact.create in `book`).
        public internal(set) var isNew = false
        private var base: Snapshot

        private init(book: AddressBookID?, isNew: Bool) {
            self.book = book
            self.isNew = isNew
            base = Snapshot(strings: Array(repeating: "", count: 8), emails: [], phones: [], addresses: [], urls: [],
                            birthday: nil, note: "")
            base = snapshot()
        }

        /// contacts.NewForm: the form of a new contact in `book`.
        public static func new(book: AddressBookID?) -> Form {
            Form(book: book, isNew: true)
        }

        /// contacts.EditForm: the form of an existing card.
        public static func edit(_ card: ContactCard) -> Form {
            var f = Form(book: card.bookId, isNew: false)
            f.original = card
            f.load(card)
            f.base = f.snapshot()
            return f
        }

        mutating func load(_ c: ContactCard) {
            given = c.name.given
            family = c.name.family
            middle = c.name.middle
            prefix = c.name.prefix
            suffix = c.name.suffix
            nickname = c.nickname
            org = c.org
            title = c.title
            note = c.note
            emails = c.emails.map { LabelledValue(label: $0.label, value: $0.address) }
            phones = c.phones.map { LabelledValue(label: $0.label, value: $0.number) }
            addresses = c.addresses
            urls = c.urls.map { LabelledValue(label: $0.label, value: $0.url) }
            birthday = c.birthday
            if given.isEmpty && family.isEmpty && middle.isEmpty && prefix.isEmpty && suffix.isEmpty && !c.displayName.isEmpty {
                // A card with only a display name (a server's FN): the form
                // shows it split, so that it can be edited.
                (given, family) = splitName(c.displayName)
            }
        }

        func snapshot() -> Snapshot {
            Snapshot(
                strings: [given, family, middle, prefix, suffix, nickname, org, title],
                emails: emails, phones: phones, addresses: addresses, urls: urls, birthday: birthday, note: note)
        }

        /// contacts.Form.Changed: the parts the user changed since the form
        /// opened (or was rebased), compared after trimming, empty rows
        /// left out.
        public var changed: [FieldKey] {
            let cur = Form.normal(snapshot())
            let old = Form.normal(base)
            var out: [FieldKey] = []
            for i in 0..<8 where cur.strings[i] != old.strings[i] {
                out.append(FieldKey(rawValue: i)!)
            }
            if cur.emails != old.emails {
                out.append(.emails)
            }
            if cur.phones != old.phones {
                out.append(.phones)
            }
            if cur.addresses != old.addresses {
                out.append(.addresses)
            }
            if cur.urls != old.urls {
                out.append(.urls)
            }
            if cur.birthday != old.birthday {
                out.append(.birthday)
            }
            if cur.note != old.note {
                out.append(.note)
            }
            return out
        }

        /// contacts.Form.Dirty: a change (closing the form asks; Done of an
        /// unchanged existing contact sends nothing).
        public var dirty: Bool { !changed.isEmpty }

        /// contacts.normal: a snapshot as it would be saved: strings
        /// trimmed, empty rows dropped.
        static func normal(_ s: Snapshot) -> Snapshot {
            var s = s
            s.strings = s.strings.map { trimSpace($0) }
            s.emails = normalRows(s.emails)
            s.phones = normalRows(s.phones)
            s.urls = normalRows(s.urls)
            s.addresses = s.addresses.map { trimAddress($0) }.filter { !emptyAddress($0) }
            s.note = trimSpace(s.note)
            return s
        }

        static func normalRows(_ rs: [LabelledValue]) -> [LabelledValue] {
            rs.compactMap { r in
                let v = trimSpace(r.value)
                return v.isEmpty ? nil : LabelledValue(label: r.label, value: v)
            }
        }

        static func trimAddress(_ a: ContactAddress) -> ContactAddress {
            var a = a
            a.street = trimSpace(a.street)
            a.city = trimSpace(a.city)
            a.region = trimSpace(a.region)
            a.postcode = trimSpace(a.postcode)
            a.country = trimSpace(a.country)
            return a
        }

        static func emptyAddress(_ a: ContactAddress) -> Bool {
            a.street.isEmpty && a.city.isEmpty && a.region.isEmpty && a.postcode.isEmpty && a.country.isEmpty
        }

        /// contacts.Form.Card: the card Done sends: trimmed, empty rows
        /// left out; the display name composed from the name parts when one
        /// of them changed or the original had none, else the original's.
        public var card: ContactCard {
            let s = Form.normal(snapshot())
            var c = ContactCard(
                id: original.id, bookId: original.bookId, revision: original.revision,
                name: ContactName(given: s.strings[FieldKey.given.rawValue], family: s.strings[FieldKey.family.rawValue],
                                  middle: s.strings[FieldKey.middle.rawValue], prefix: s.strings[FieldKey.prefix.rawValue],
                                  suffix: s.strings[FieldKey.suffix.rawValue]),
                nickname: s.strings[FieldKey.nickname.rawValue], org: s.strings[FieldKey.org.rawValue],
                title: s.strings[FieldKey.title.rawValue],
                emails: s.emails.map { ContactEmail(label: $0.label, address: $0.value) },
                phones: s.phones.map { ContactPhone(label: $0.label, number: $0.value) },
                addresses: s.addresses,
                urls: s.urls.map { ContactURL(label: $0.label, url: $0.value) },
                birthday: s.birthday, note: s.note)
            if isNew {
                c.bookId = book ?? ""
            }
            let nameChanged = changed.contains { $0 <= .suffix }
            if !isNew && !nameChanged && !trimSpace(original.displayName).isEmpty {
                // Unchanged: the original's name as it was, so the patch
                // leaves it alone (a server's card with only a display name
                // stays so).
                c.name = original.name
                c.displayName = original.displayName
            } else {
                c.displayName = composeName(c.name)
                if c.displayName.isEmpty {
                    c.displayName = c.org
                }
                if c.displayName.isEmpty, let e = c.emails.first {
                    c.displayName = e.address
                }
            }
            return c
        }

        /// contacts.Form.Rebase: a contactConflict's Reload. The form shows
        /// `server`, and the parts the user changed keep the user's values
        /// (a list as a whole); afterwards `server` is the original, so Done
        /// updates it with its revision.
        public mutating func rebase(_ server: ContactCard) {
            let changedKeys = changed
            let mine = self
            original = server
            load(server)
            base = snapshot()
            for k in changedKeys {
                switch k {
                case .given: given = mine.given
                case .family: family = mine.family
                case .middle: middle = mine.middle
                case .prefix: prefix = mine.prefix
                case .suffix: suffix = mine.suffix
                case .nickname: nickname = mine.nickname
                case .org: org = mine.org
                case .title: title = mine.title
                case .emails: emails = mine.emails
                case .phones: phones = mine.phones
                case .addresses: addresses = mine.addresses
                case .urls: urls = mine.urls
                case .birthday: birthday = mine.birthday
                case .note: note = mine.note
                }
            }
        }

        /// contacts.Form.Saved: the answer of Done: the card as sent with
        /// its new id (nil keeps it) and revision becomes the original, the
        /// form unchanged.
        public mutating func saved(id: ContactID?, revision: String) {
            var c = card
            if let id, !id.rawValue.isEmpty {
                c.id = id
            }
            c.revision = revision
            original = c
            isNew = false
            base = snapshot()
        }

        // MARK: Rows

        /// contacts.Form.Count: the number of rows of a kind.
        public func count(_ k: FieldKind) -> Int {
            switch k {
            case .email: return emails.count
            case .phone: return phones.count
            case .address: return addresses.count
            case .url: return urls.count
            }
        }

        /// contacts.Form.CanAdd: another row of the kind fits.
        public func canAdd(_ k: FieldKind) -> Bool { count(k) < maxRows(k) }

        /// contacts.Form.NextLabel: the label of a new row: the first of
        /// `labels(k)` no row of the kind has, else other.
        public func nextLabel(_ k: FieldKind) -> ContactLabel {
            let used: Set<ContactLabel>
            switch k {
            case .email: used = Set(emails.map(\.label))
            case .phone: used = Set(phones.map(\.label))
            case .address: used = Set(addresses.map(\.label))
            case .url: used = Set(urls.map(\.label))
            }
            return labels(k).first { !used.contains($0) } ?? .other
        }

        /// contacts.Form.AddRow: appends an empty row of the kind
        /// (`nextLabel`) and returns its index; nil at the limit.
        @discardableResult
        public mutating func addRow(_ k: FieldKind) -> Int? {
            guard canAdd(k) else { return nil }
            let l = nextLabel(k)
            switch k {
            case .email: emails.append(LabelledValue(label: l))
            case .phone: phones.append(LabelledValue(label: l))
            case .address: addresses.append(ContactAddress(label: l))
            case .url: urls.append(LabelledValue(label: l))
            }
            return count(k) - 1
        }

        /// contacts.Form.RemoveRow: an index out of range does nothing.
        public mutating func removeRow(_ k: FieldKind, at i: Int) {
            guard i >= 0, i < count(k) else { return }
            switch k {
            case .email: emails.remove(at: i)
            case .phone: phones.remove(at: i)
            case .address: addresses.remove(at: i)
            case .url: urls.remove(at: i)
            }
        }

        // MARK: Validation

        /// contacts.Form.Validate: the form's problems, none when Done may
        /// save: a name, a company or an address at all; valid e-mail
        /// addresses; websites with no scheme or http/https; a real
        /// birthday; the API's limits.
        public func validate() -> [Problem] {
            let c = card
            var out: [Problem] = []
            if composeName(c.name).isEmpty && c.org.isEmpty && c.emails.isEmpty && c.nickname.isEmpty {
                out.append(Problem(key: .given, text: L10n.T("Enter a name, a company or an email address.")))
            }
            let long = L10n.T("This text is too long.")
            let names = [c.name.given, c.name.family, c.name.middle, c.name.prefix, c.name.suffix, c.nickname]
            for (i, s) in names.enumerated() where s.utf8.count > API.Limits.maxContactNameBytes {
                out.append(Problem(key: FieldKey(rawValue: i)!, text: long))
            }
            if c.org.utf8.count > API.Limits.maxContactFieldBytes {
                out.append(Problem(key: .org, text: long))
            }
            if c.title.utf8.count > API.Limits.maxContactFieldBytes {
                out.append(Problem(key: .title, text: long))
            }
            for (i, r) in emails.enumerated() {
                let v = trimSpace(r.value)
                if v.isEmpty {
                    continue
                }
                if !validEmail(v) {
                    // TRANSLATORS: %s is what the user typed as an e-mail address.
                    out.append(Problem(key: .emails, index: i, text: L10n.T("%s is not a valid email address.", clean(v, max: 80))))
                } else if v.utf8.count > API.Limits.maxContactFieldBytes || r.label.rawValue.utf8.count > API.Limits.maxContactLabelBytes {
                    out.append(Problem(key: .emails, index: i, text: long))
                }
            }
            for (i, r) in phones.enumerated()
            where trimSpace(r.value).utf8.count > API.Limits.maxContactFieldBytes
                || r.label.rawValue.utf8.count > API.Limits.maxContactLabelBytes {
                out.append(Problem(key: .phones, index: i, text: long))
            }
            for (i, a) in addresses.enumerated() {
                let t = Form.trimAddress(a)
                if [t.street, t.city, t.region, t.postcode, t.country].contains(where: { $0.utf8.count > API.Limits.maxContactFieldBytes }) {
                    out.append(Problem(key: .addresses, index: i, text: long))
                }
            }
            for (i, r) in urls.enumerated() {
                let v = trimSpace(r.value)
                if v.isEmpty {
                    continue
                }
                if !validURL(v) {
                    out.append(Problem(key: .urls, index: i, text: L10n.T("A website must be an address starting with http:// or https://.")))
                } else if v.utf8.count > API.Limits.maxContactFieldBytes {
                    out.append(Problem(key: .urls, index: i, text: long))
                }
            }
            if let b = birthday, !validBirthday(b) {
                out.append(Problem(key: .birthday, text: L10n.T("Enter a valid date.")))
            }
            if c.note.utf8.count > API.Limits.maxContactNoteBytes {
                out.append(Problem(key: .note, text: long))
            }
            return out
        }
    }

    /// contacts.MaxRows: the limit of rows of a kind (the API's).
    public static func maxRows(_ k: FieldKind) -> Int {
        switch k {
        case .email: return API.Limits.maxContactEmails
        case .phone: return API.Limits.maxContactPhones
        case .address: return API.Limits.maxContactAddresses
        case .url: return API.Limits.maxContactURLs
        }
    }

    /// contacts.LabelChoices: the picker's choices for a row with label
    /// `current`: `labels(k)`, no label, and `current` when it is the
    /// server's own.
    public static func labelChoices(_ k: FieldKind, current: ContactLabel) -> [ContactLabel] {
        var out = labels(k) + [ContactLabel.none]
        if !current.isEmpty && !isKnownLabel(k, current) {
            out.append(current)
        }
        return out
    }

    /// contacts.AddRowText: the button under the rows of a kind.
    public static func addRowText(_ k: FieldKind) -> String {
        switch k {
        case .email:
            // TRANSLATORS: the contact form's button that adds an e-mail address row.
            return L10n.T("Add Email")
        case .phone:
            return L10n.T("Add Phone")
        case .address:
            return L10n.T("Add Address")
        case .url:
            return L10n.T("Add Website")
        }
    }

    /// contacts.RemoveRowText: a row's remove button (tooltip and
    /// accessibility label).
    public static var removeRowText: String { L10n.T("Remove") }

    /// contacts.AddBirthday.
    public static var addBirthday: String { L10n.T("Add Birthday") }

    /// contacts.Placeholder: a form field's placeholder.
    public static func placeholder(_ k: FieldKey) -> String {
        switch k {
        case .given:
            // TRANSLATORS: placeholders of the contact form's fields.
            return L10n.T("First Name")
        case .family:
            return L10n.T("Last Name")
        case .middle:
            return L10n.T("Middle Name")
        case .prefix:
            // TRANSLATORS: a title before a name, such as "Ing." or "Dr.".
            return L10n.T("Prefix")
        case .suffix:
            // TRANSLATORS: a title after a name, such as "Ph.D." or "Jr.".
            return L10n.T("Suffix")
        case .nickname:
            return L10n.T("Nickname")
        case .org:
            return L10n.T("Company")
        case .title:
            return L10n.T("Job Title")
        case .emails:
            return L10n.T("Email")
        case .phones:
            return L10n.T("Phone")
        case .urls:
            return L10n.T("Website")
        case .birthday:
            return L10n.T("Birthday")
        case .note:
            return L10n.T("Note")
        case .addresses:
            return ""
        }
    }

    /// contacts.AddressPart: a part of a postal address, in the form's
    /// order.
    public enum AddressPart: Int, Sendable, CaseIterable {
        case street, postcode, city, region, country
    }

    /// contacts.AddressPlaceholder.
    public static func addressPlaceholder(_ p: AddressPart) -> String {
        switch p {
        case .street:
            return L10n.T("Street")
        case .postcode:
            return L10n.T("Postal Code")
        case .city:
            return L10n.T("City")
        case .region:
            // TRANSLATORS: a part of a postal address: state, province or region.
            return L10n.T("Region")
        case .country:
            return L10n.T("Country")
        }
    }

    /// contacts.BookLabel: the choice of book of a new contact.
    public static var bookLabel: String {
        // TRANSLATORS: the label of the choice where a new contact is saved.
        L10n.T("Address Book")
    }

    /// contacts.NameLabel: Add to Contacts' name.
    public static var nameLabel: String {
        // TRANSLATORS: the label of a new contact's name in Add to Contacts.
        L10n.T("Name")
    }

    /// contacts.EmailLabel: Add to Contacts' address.
    public static var emailLabel: String { placeholder(.emails) }

    /// contacts.Problem: a reason Done cannot save: the part, the row (nil
    /// for a part without rows) and the sentence.
    public struct Problem: Sendable, Equatable {
        public var key: FieldKey
        public var index: Int?
        public var text: String

        public init(key: FieldKey, index: Int? = nil, text: String) {
            self.key = key
            self.index = index
            self.text = text
        }
    }

    /// contacts.ValidEmail: a bare e-mail address (no name, no angle
    /// brackets) as the recipients' fields read one, with a dot in the
    /// domain.
    public static func validEmail(_ s: String) -> Bool {
        if s.isEmpty || s.unicodeScalars.contains(where: { $0.properties.isWhitespace || $0.properties.generalCategory == .control }) {
            return false
        }
        if s.unicodeScalars.contains(where: { "<>,;\"".unicodeScalars.contains($0) }) {
            return false
        }
        guard Cal.isBareAddress(s), let at = s.lastIndex(of: "@") else { return false }
        return s[s.index(after: at)...].contains(".")
    }

    /// contacts.ValidURL: a website the form may save: http or https with
    /// a host, or no scheme where `openURL` reads it as https.
    public static func validURL(_ s: String) -> Bool {
        if s.contains("://") {
            guard let u = URLComponents(string: s), let scheme = u.scheme?.lowercased() else { return false }
            return (scheme == "http" || scheme == "https") && !(u.host ?? "").isEmpty
        }
        return !openURL(s).isEmpty
    }

    /// contacts.SplitName: the last word is the family name ("Petr
    /// Svoboda" → "Petr", "Svoboda"); one word is a given name.
    public static func splitName(_ name: String) -> (given: String, family: String) {
        let words = fields(clean(name, max: API.Limits.maxContactNameBytes))
        switch words.count {
        case 0:
            return ("", "")
        case 1:
            return (words[0], "")
        default:
            return (words.dropLast().joined(separator: " "), words[words.count - 1])
        }
    }
}
