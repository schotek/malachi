// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// ui/internal/contacts detail.go: the contact's detail (W1) — the heading
// (initials, name, "Title · Company", the source line, Edit or Read Only),
// the actions (Write Email, Show Mail, Copy) and the rows of label and
// value in a fixed order: e-mail addresses, phones, postal addresses,
// birthday, websites, nickname, note. Everything is plain text: a website
// is opened only when it is http or https (`openURL`, through the
// client's "Open This Link?"), a phone number only copied.

extension ContactBook {
    /// contacts.FieldKind: a kind of labelled field of a card.
    public enum FieldKind: Int, Sendable, CaseIterable {
        case email, phone, address, url
    }

    /// contacts.Labels: a kind's label choices in the form's picker, in
    /// their order; no label is offered after them (`noLabel`).
    public static func labels(_ k: FieldKind) -> [ContactLabel] {
        if k == .phone {
            return [.mobile, .work, .home, .main, .fax, .pager, .other]
        }
        return [.work, .home, .other]
    }

    /// contacts.IsKnownLabel: a label of `labels(k)`; any other non-empty
    /// label is the server's own (display text, shown as it is).
    public static func isKnownLabel(_ k: FieldKind, _ l: ContactLabel) -> Bool {
        labels(k).contains(l)
    }

    /// contacts.NoLabel.
    public static var noLabel: String {
        // TRANSLATORS: the contact form's label choice for an e-mail address, phone or address without a label.
        L10n.T("No Label")
    }

    /// contacts.LabelText: a label in the form's picker: a known label
    /// translated by kind, the server's own cleaned, none `noLabel`.
    public static func labelText(_ k: FieldKind, _ l: ContactLabel) -> String {
        if l.isEmpty {
            return noLabel
        }
        if !isKnownLabel(k, l) {
            return clean(l.rawValue, max: API.Limits.maxContactLabelBytes)
        }
        switch k {
        case .email:
            switch l {
            case .work:
                // TRANSLATORS: the label of a work e-mail address in the contact form (lower case, like the other labels).
                return L10n.C("email label", "work")
            case .home:
                // TRANSLATORS: the label of a personal e-mail address.
                return L10n.C("email label", "home")
            default:
                return L10n.C("email label", "other")
            }
        case .phone:
            return phoneLabel(l)
        case .address:
            switch l {
            case .work:
                // TRANSLATORS: the label of a postal address at work.
                return L10n.C("address label", "work")
            case .home:
                return L10n.C("address label", "home")
            default:
                return L10n.C("address label", "other")
            }
        case .url:
            switch l {
            case .work:
                // TRANSLATORS: the label of a work website in the contact form.
                return L10n.C("url label", "work")
            case .home:
                return L10n.C("url label", "home")
            default:
                return L10n.C("url label", "other")
            }
        }
    }

    static func phoneLabel(_ l: ContactLabel) -> String {
        switch l {
        case .mobile:
            // TRANSLATORS: the label of a mobile phone number.
            return L10n.C("phone label", "mobile")
        case .work:
            return L10n.C("phone label", "work")
        case .home:
            return L10n.C("phone label", "home")
        case .main:
            // TRANSLATORS: the label of a company's main phone number.
            return L10n.C("phone label", "main")
        case .fax:
            return L10n.C("phone label", "fax")
        case .pager:
            return L10n.C("phone label", "pager")
        default:
            return L10n.C("phone label", "other")
        }
    }

    /// contacts.DetailLabel: a labelled row's label in the detail ("work
    /// email", "mobile", "website"); the server's own label as it is.
    public static func detailLabel(_ k: FieldKind, _ l: ContactLabel) -> String {
        if !l.isEmpty && !isKnownLabel(k, l) {
            return clean(l.rawValue, max: API.Limits.maxContactLabelBytes)
        }
        switch k {
        case .email:
            switch l {
            case .work:
                // TRANSLATORS: a row label of the contact detail (lower case).
                return L10n.T("work email")
            case .home:
                return L10n.T("home email")
            case .other:
                return L10n.T("other email")
            default:
                // TRANSLATORS: a row label of the contact detail: an e-mail address without a label.
                return L10n.T("email")
            }
        case .phone:
            if l.isEmpty {
                // TRANSLATORS: a row label of the contact detail: a phone number without a label.
                return L10n.T("phone")
            }
            return phoneLabel(l)
        case .address:
            if l.isEmpty {
                // TRANSLATORS: a row label of the contact detail: a postal address without a label.
                return L10n.T("address")
            }
            return labelText(k, l)
        case .url:
            switch l {
            case .work:
                return L10n.T("work website")
            case .home:
                return L10n.T("home website")
            default:
                // TRANSLATORS: a row label of the contact detail: a web address.
                return L10n.T("website")
            }
        }
    }

    /// contacts.RowKind: what a detail row shows, in the rows' order.
    public enum RowKind: Int, Sendable, CaseIterable {
        case email, phone, address, birthday, url, nickname, note
    }

    /// contacts.DetailRow: one row of the detail, plain text.
    public struct DetailRow: Sendable, Equatable {
        public var kind: RowKind
        public var label: String
        public var value: String
        /// An email row's address (Write Email to it).
        public var address = ""
        /// A website row's address to open (`openURL`); "" when it cannot
        /// be opened (the value is then only text).
        public var url = ""

        public init(kind: RowKind, label: String, value: String, address: String = "", url: String = "") {
            self.kind = kind
            self.label = label
            self.value = value
            self.address = address
            self.url = url
        }
    }

    /// contacts.Actions: the detail's buttons.
    public struct Actions: Sendable, Equatable {
        /// The contact has an address; with several, `emails` are the
        /// choices (a menu), else empty.
        public var writeEmail = false
        public var emails: [DetailRow] = []
        /// The search of Show Mail ("" none: no address).
        public var showMail = ""
        /// The book can be changed.
        public var edit = false
        public var delete = false

        public init() {}
    }

    /// contacts.Detail: the detail of a contact.
    public struct Detail: Sendable, Equatable {
        public var title = ""
        /// "Job · Company", "" none.
        public var subtitle = ""
        public var initials = ""
        public var rows: [DetailRow] = []
        /// The Read Only capsule instead of Edit.
        public var readOnly = false
        public var actions = Actions()

        public init() {}
    }

    /// contacts.BirthdayText: "14 March 1980", without a year "14 March";
    /// "" for an invalid one.
    public static func birthdayText(_ b: ContactBirthday, format: Strftime) -> String {
        guard validBirthday(b) else { return "" }
        if b.year == 0 {
            // TRANSLATORS: strftime format of a birthday without a year, e.g. "14 March".
            // xgettext:no-c-format
            return format(noon(2000, b.month, b.day), L10n.T("%-d %B"))
        }
        // TRANSLATORS: strftime format of a birthday with its year, e.g. "14 March 1980".
        // xgettext:no-c-format
        return format(noon(b.year, b.month, b.day), L10n.T("%-d %B %Y"))
    }

    /// Noon UTC of a proleptic Gregorian date (Go's time.Date(…, 12, 0, 0,
    /// 0, time.UTC)).
    static func noon(_ year: Int, _ month: Int, _ day: Int) -> Date {
        Date(timeIntervalSince1970: Double(RFC3339.daysFromCivil(year: year, month: month, day: day) * 86400 + 43200))
    }

    /// contacts.ValidBirthday: a real date (year 0 = unknown; 29 February
    /// without a year is valid).
    public static func validBirthday(_ b: ContactBirthday) -> Bool {
        guard (1...12).contains(b.month), b.day >= 1, (0...9999).contains(b.year) else { return false }
        let y = b.year == 0 ? 2000 : b.year
        let leap = (y % 4 == 0 && y % 100 != 0) || y % 400 == 0
        let days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]
        return b.day <= days[b.month - 1]
    }

    /// contacts.AddressLine: a postal address on one line: "Dlouhá 12, 110
    /// 00 Praha 1, Czechia" (street lines, postcode and city, region,
    /// country).
    public static func addressLine(_ a: ContactAddress) -> String {
        let field = API.Limits.maxContactFieldBytes
        var parts: [String] = []
        for l in a.street.components(separatedBy: "\n") {
            let c = clean(l, max: field)
            if !c.isEmpty {
                parts.append(c)
            }
        }
        let city = trimSpace(clean(a.postcode, max: field) + " " + clean(a.city, max: field))
        for p in [city, clean(a.region, max: field), clean(a.country, max: field)] where !p.isEmpty {
            parts.append(p)
        }
        return parts.joined(separator: ", ")
    }

    /// contacts.OpenURL: the address a website row opens: an http or https
    /// URL with a host and no user, a bare host ("example.org/x") as
    /// https; "" anything else.
    public static func openURL(_ raw: String) -> String {
        var s = trimSpace(clean(raw, max: API.Limits.maxContactFieldBytes))
        if s.isEmpty || s.contains(" ") || s.contains("\t") {
            return ""
        }
        if !s.contains("://") {
            if s.contains(":") || s.hasPrefix("/") || !s.contains(".") {
                return ""
            }
            s = "https://" + s
        }
        guard var u = URLComponents(string: s), let scheme = u.scheme?.lowercased(), scheme == "http" || scheme == "https",
              let host = u.host, !host.isEmpty, u.user == nil, u.password == nil else { return "" }
        u.scheme = scheme
        return u.string ?? ""
    }

    /// contacts.Title: a contact's name in a list or heading: the display
    /// name, else the structured name, the company, the first address,
    /// `noName`.
    public static func title(_ c: ContactCard) -> String {
        let n = clean(c.displayName, max: API.Limits.maxContactNameBytes)
        if !n.isEmpty {
            return n
        }
        let composed = composeName(c.name)
        if !composed.isEmpty {
            return composed
        }
        let o = clean(c.org, max: API.Limits.maxContactFieldBytes)
        if !o.isEmpty {
            return o
        }
        for e in c.emails {
            let a = clean(e.address, max: API.Limits.maxContactFieldBytes)
            if !a.isEmpty {
                return a
            }
        }
        return noName
    }

    /// contacts.SummaryTitle: a list row's name: the display name, the
    /// company, the address, `noName`.
    public static func summaryTitle(_ s: ContactSummary) -> String {
        for v in [s.displayName, s.org, s.email] {
            let c = clean(v, max: API.Limits.maxContactFieldBytes)
            if !c.isEmpty {
                return c
            }
        }
        return noName
    }

    /// contacts.ComposeName: prefix, given, middle, family, suffix.
    public static func composeName(_ n: ContactName) -> String {
        [n.prefix, n.given, n.middle, n.family, n.suffix]
            .map { clean($0, max: API.Limits.maxContactNameBytes) }
            .filter { !$0.isEmpty }
            .joined(separator: " ")
    }

    /// contacts.Initials: the avatar's letters: the first letters of the
    /// first and the last word of `name` (one for one word), upper case;
    /// else the first letter of the address; "" none.
    public static func initials(_ name: String, address: String) -> String {
        var words: [String] = []
        for w in fields(clean(name, max: API.Limits.maxContactNameBytes)) {
            if let r = firstLetter(w) {
                words.append(upper(r))
            }
        }
        switch words.count {
        case 0:
            if let r = firstLetter(address) {
                return upper(r)
            }
            return ""
        case 1:
            return words[0]
        default:
            return words[0] + words[words.count - 1]
        }
    }

    static func firstLetter(_ s: String) -> Unicode.Scalar? {
        s.unicodeScalars.first { isLetter($0) || $0.properties.generalCategory == .decimalNumber }
    }

    /// unicode.IsLetter: the L categories.
    static func isLetter(_ r: Unicode.Scalar) -> Bool {
        switch r.properties.generalCategory {
        case .uppercaseLetter, .lowercaseLetter, .titlecaseLetter, .modifierLetter, .otherLetter:
            return true
        default:
            return false
        }
    }

    /// unicode.ToUpper of one character: the simple mapping (a full
    /// mapping into several characters, ß → SS, leaves it as it is).
    static func upper(_ r: Unicode.Scalar) -> String {
        let m = r.properties.uppercaseMapping
        return m.unicodeScalars.count == 1 ? m : String(r)
    }

    /// contacts.DetailOf: the detail of a card; `format` writes the
    /// birthday.
    public static func detail(_ card: ContactCard, format: Strftime) -> Detail {
        let field = API.Limits.maxContactFieldBytes
        var d = Detail()
        d.title = title(card)
        d.readOnly = card.readOnly
        let jobTitle = clean(card.title, max: field)
        let org = clean(card.org, max: field)
        if !jobTitle.isEmpty && !org.isEmpty {
            d.subtitle = twoParts(jobTitle, org)
        } else if !jobTitle.isEmpty {
            d.subtitle = jobTitle
        } else {
            d.subtitle = org
        }
        if d.subtitle == d.title {
            d.subtitle = ""
        }
        var first = ""
        for e in card.emails {
            let a = clean(e.address, max: field)
            if a.isEmpty {
                continue
            }
            if first.isEmpty {
                first = a
            }
            let r = DetailRow(kind: .email, label: detailLabel(.email, e.label), value: a, address: a)
            d.rows.append(r)
            d.actions.emails.append(r)
        }
        d.initials = initials(d.title, address: first)
        if d.title == noName {
            d.initials = initials("", address: first)
        }
        for p in card.phones {
            let n = clean(p.number, max: field)
            if !n.isEmpty {
                d.rows.append(DetailRow(kind: .phone, label: detailLabel(.phone, p.label), value: n))
            }
        }
        for a in card.addresses {
            let l = addressLine(a)
            if !l.isEmpty {
                d.rows.append(DetailRow(kind: .address, label: detailLabel(.address, a.label), value: l))
            }
        }
        if let b = card.birthday {
            let t = birthdayText(b, format: format)
            if !t.isEmpty {
                // TRANSLATORS: a row label of the contact detail.
                d.rows.append(DetailRow(kind: .birthday, label: L10n.T("birthday"), value: t))
            }
        }
        for u in card.urls {
            let v = clean(u.url, max: field)
            if !v.isEmpty {
                d.rows.append(DetailRow(kind: .url, label: detailLabel(.url, u.label), value: v, url: openURL(v)))
            }
        }
        let nick = clean(card.nickname, max: API.Limits.maxContactNameBytes)
        if !nick.isEmpty {
            // TRANSLATORS: a row label of the contact detail.
            d.rows.append(DetailRow(kind: .nickname, label: L10n.T("nickname"), value: nick))
        }
        let note = cleanBlock(card.note, max: API.Limits.maxContactNoteBytes)
        if !note.isEmpty {
            // TRANSLATORS: a row label of the contact detail.
            d.rows.append(DetailRow(kind: .note, label: L10n.T("note"), value: note))
        }
        d.actions.writeEmail = !first.isEmpty
        if !first.isEmpty {
            d.actions.showMail = showMailQuery(first)
        }
        if d.actions.emails.count < 2 {
            d.actions.emails = []
        }
        d.actions.edit = !card.readOnly
        d.actions.delete = !card.readOnly
        return d
    }

    /// contacts.ShowMailQuery: the search of Show Mail for an address, in
    /// the syntax of search.query: from:address, the value in straight
    /// quotes when it has a space; the quote characters the parser reads
    /// („ “ ” ") dropped and control characters as spaces. "" for an
    /// empty address.
    public static func showMailQuery(_ address: String) -> String {
        var mapped = String.UnicodeScalarView()
        for r in address.unicodeScalars {
            switch r {
            case "\"", "\u{201E}", "\u{201C}", "\u{201D}":
                continue
            default:
                if r.properties.generalCategory == .control {
                    mapped.append(" ")
                } else {
                    mapped.append(r)
                }
            }
        }
        let v = fields(String(mapped)).joined(separator: " ")
        if v.isEmpty {
            return ""
        }
        if v.contains(" ") {
            return "from:\"" + v + "\""
        }
        return "from:" + v
    }

    /// contacts.CopyText: Copy, the contact as plain text, one line per
    /// row: the name, "Job · Company", then "label: value". Never vCard
    /// syntax: it is for pasting into a message.
    public static func copyText(_ card: ContactCard, format: Strftime) -> String {
        let d = detail(card, format: format)
        var lines = [d.title]
        if !d.subtitle.isEmpty {
            lines.append(d.subtitle)
        }
        for r in d.rows {
            lines.append(r.label + ": " + r.value)
        }
        return lines.joined(separator: "\n")
    }

    // MARK: Cleaning

    /// contacts.Clean: a server's string as one line of display text:
    /// control, bidi and invisible format characters dropped, white space
    /// runs as one space, trimmed, cut to `max` bytes on a character
    /// boundary (`max` ≤ 0: no cut).
    public static func clean(_ s: String, max: Int) -> String {
        var out = String.UnicodeScalarView()
        var space = false
        for r in s.unicodeScalars {
            if r.value == 0xFFFD || isBidiOrInvisible(r) {
                continue
            }
            if r.properties.isWhitespace || r.properties.generalCategory == .control {
                space = true
                continue
            }
            if space && !out.isEmpty {
                out.append(" ")
            }
            space = false
            out.append(r)
        }
        return cut(String(out), max)
    }

    /// contacts.CleanBlock: `clean` that keeps line breaks (a note): every
    /// line cleaned, at most two empty lines in a row, trimmed.
    public static func cleanBlock(_ s: String, max: Int) -> String {
        let unified = s.replacingOccurrences(of: "\r\n", with: "\n").replacingOccurrences(of: "\r", with: "\n")
        var out: [String] = []
        var empty = 0
        for line in unified.components(separatedBy: "\n") {
            let l = clean(line, max: 0)
            if l.isEmpty {
                empty += 1
                if empty > 2 {
                    continue
                }
            } else {
                empty = 0
            }
            out.append(l)
        }
        return cut(trimSpace(out.joined(separator: "\n")), max)
    }

    static func isBidiOrInvisible(_ r: Unicode.Scalar) -> Bool {
        switch r.value {
        case 0x202A...0x202E, 0x2066...0x2069, 0x200E, 0x200F, 0x061C:
            return true
        case 0xFEFF, 0x200B...0x200D, 0x2060, 0x00AD:
            return true
        default:
            return r.properties.generalCategory == .format
        }
    }

    /// contacts.cut: at most `max` UTF-8 bytes on a character boundary,
    /// trimmed; `max` ≤ 0 or a shorter string as it is.
    static func cut(_ s: String, _ max: Int) -> String {
        if max <= 0 || s.utf8.count <= max {
            return s
        }
        var out = String.UnicodeScalarView()
        var bytes = 0
        for r in s.unicodeScalars {
            let w = UTF8.width(r)
            if bytes + w > max {
                break
            }
            bytes += w
            out.append(r)
        }
        return trimSpace(String(out))
    }

    /// strings.TrimSpace: White_Space at both ends goes.
    static func trimSpace(_ s: String) -> String {
        let scalars = s.unicodeScalars
        guard let first = scalars.firstIndex(where: { !$0.properties.isWhitespace }),
              let last = scalars.lastIndex(where: { !$0.properties.isWhitespace }) else { return "" }
        return String(scalars[first...last])
    }

    /// strings.Fields: the runs between White_Space.
    static func fields(_ s: String) -> [String] {
        var out: [String] = []
        var cur = String.UnicodeScalarView()
        for r in s.unicodeScalars {
            if r.properties.isWhitespace {
                if !cur.isEmpty {
                    out.append(String(cur))
                    cur = String.UnicodeScalarView()
                }
            } else {
                cur.append(r)
            }
        }
        if !cur.isEmpty {
            out.append(String(cur))
        }
        return out
    }
}
