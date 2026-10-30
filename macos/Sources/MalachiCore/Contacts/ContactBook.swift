// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The Contacts window's pure logic and every text it shows
/// (docs/plans/address-book.md §5, wireframes W1–W6), a 1:1 port of the Go
/// reference ui/internal/contacts (text.go and model.go here, detail.go in
/// ContactBookDetail.swift, form.go in ContactBookForm.swift, add.go in
/// ContactBookAdd.swift), tests included: the sidebar of address books,
/// the list with its index sections, the contact's detail rows and actions,
/// the edit form with its validation and conflict rebase, Add to
/// Contacts… of an address chip, the preferences' texts, the errors by
/// code and the window's breakpoints.
///
/// Named `ContactBook`, not `Contacts`, so that it never meets Apple's
/// Contacts framework, which the application must not import (the plan's
/// §1: nothing of the system's address book). Texts go through `L10n` with
/// the GTK msgids as keys. Every string of a book or a contact is untrusted
/// display text the daemon cleaned; this cleans it again (`clean`) and
/// formats it into its msgids, never concatenating sentences; the views
/// show it as plain text only (`stringValue`).
public enum ContactBook {
    /// contacts.Strftime: `t` written with a strftime format (a msgid
    /// already translated) in UTC, the zone the birthdays are built in.
    /// The application passes `strftime(locale:)`, the tests
    /// `englishStrftime`.
    public typealias Strftime = @Sendable (_ t: Date, _ format: String) -> String

    static let utc = TimeZone(secondsFromGMT: 0)!

    /// The user's locale through `DateFormatter` (Strftime patterns), in
    /// UTC.
    public static func strftime(locale: Locale) -> Strftime {
        { t, f in
            let df = MalachiCore.Strftime.formatter(f, locale: locale)
            var cal = Foundation.Calendar(identifier: .gregorian)
            cal.timeZone = utc
            df.calendar = cal
            df.timeZone = utc
            return df.string(from: t)
        }
    }

    /// calendar.EnglishStrftime in UTC: the writer of the tests.
    public static let englishStrftime: Strftime = { t, f in Cal.englishStrftime(t, f, zone: utc) }

    // MARK: The window

    /// contacts.WindowTitle: the window's title, the menu item that opens
    /// it (Window ▸ Contacts), the Settings tab and the narrow window's
    /// back button.
    public static var windowTitle: String {
        // TRANSLATORS: the Contacts window (address books of the user's accounts), its menu item and its Preferences page.
        L10n.T("Contacts")
    }

    /// contacts.AllContacts: the sidebar's first row.
    public static var allContacts: String {
        // TRANSLATORS: the Contacts window's sidebar row that lists the contacts of every address book.
        L10n.T("All Contacts")
    }

    /// contacts.RecentlyUsed: the virtual book of the addresses the user
    /// wrote to.
    public static var recentlyUsed: String {
        // TRANSLATORS: the Contacts window's address book of the addresses the user has written to (read only).
        L10n.T("Recently Used")
    }

    /// contacts.OnThisComputer: the heading of Recently Used's group. The
    /// macOS window may say On This Mac instead (a macOS-only string of
    /// the AppKit layer).
    public static var onThisComputer: String {
        // TRANSLATORS: the Contacts window's sidebar heading of the address book kept on this computer (Recently Used).
        L10n.T("On This Computer")
    }

    /// contacts.SearchContacts: the search field's placeholder.
    public static var searchContacts: String { L10n.T("Search Contacts") }

    /// contacts.NewContact: the toolbar's "+", its tooltip and the form's
    /// title for a new contact.
    public static var newContact: String { L10n.T("New Contact") }

    /// contacts.ContactCount.
    public static func contactCount(_ n: Int) -> String {
        L10n.N("%d contact", "%d contacts", n)
    }

    /// contacts.AddressCount: Recently Used's count.
    public static func addressCount(_ n: Int) -> String {
        // TRANSLATORS: the number of recently used e-mail addresses.
        L10n.N("%d address", "%d addresses", n)
    }

    /// contacts.TwoParts: two parts of one line ("All Contacts · 412").
    public static func twoParts(_ a: String, _ b: String) -> String {
        // TRANSLATORS: two parts of one line, e.g. "All Contacts · 412" or "Contacts · Microsoft 365" (address book and account).
        L10n.T("%s · %s", a, b)
    }

    /// contacts.SyncedLine: the status bar's text; `last` is the oldest
    /// lastSyncAt of the visible books (`syncedAt`).
    public static func syncedLine(_ last: Date, now: Date) -> String {
        // TRANSLATORS: the Contacts window's status bar; %s is a relative time such as "3 minutes ago".
        L10n.T("Contacts synchronized %s", Board.Text.relativeTime(last, now: now))
    }

    // MARK: The detail

    /// contacts.WriteEmail.
    public static var writeEmail: String {
        // TRANSLATORS: the contact detail's button that writes a new message to the contact.
        L10n.T("Write Email")
    }

    /// contacts.ShowMail.
    public static var showMail: String { L10n.T("Show Mail") }

    /// contacts.Copy.
    public static var copy: String {
        // TRANSLATORS: the contact detail's button that copies the whole contact as plain text.
        L10n.T("Copy")
    }

    /// contacts.Edit.
    public static var edit: String { L10n.T("Edit") }

    /// contacts.Done: the form's button that saves to the server.
    public static var done: String {
        // TRANSLATORS: the contact form's button that saves the contact.
        L10n.C("contacts", "Done")
    }

    /// contacts.Cancel.
    public static var cancel: String { L10n.T("Cancel") }

    /// contacts.DeleteContact.
    public static var deleteContact: String { L10n.T("Delete Contact…") }

    /// contacts.Delete: the delete question's button.
    public static var delete: String { L10n.T("Delete") }

    /// contacts.ReadOnly: the capsule in place of Edit.
    public static var readOnly: String {
        // TRANSLATORS: a capsule beside a contact whose address book cannot be changed.
        L10n.T("Read Only")
    }

    /// contacts.NoName.
    public static var noName: String {
        // TRANSLATORS: a contact without any name.
        L10n.T("No Name")
    }

    /// contacts.SourceLine: the detail's footer (book, account).
    public static func sourceLine(book: String, account: String) -> String {
        // TRANSLATORS: the contact detail's footer; the first %s is the address book's name, the second the account it belongs to.
        L10n.T("Address book %s · %s", book, account)
    }

    // MARK: Address chips and suggestions

    /// contacts.ShowContact.
    public static var showContact: String { L10n.T("Show Contact") }

    /// contacts.AddToContacts.
    public static var addToContacts: String { L10n.T("Add to Contacts…") }

    /// contacts.AddTitle: the Add to Contacts popover's heading.
    public static var addTitle: String { addToContacts }

    /// contacts.Add: the Add to Contacts popover's button.
    public static var add: String {
        // TRANSLATORS: the Add to Contacts popover's button that saves the new contact.
        L10n.C("contacts", "Add")
    }

    /// contacts.MoreDetails.
    public static var moreDetails: String { L10n.T("More Details…") }

    /// contacts.SenderNameWarning: under the prefilled name of Add to
    /// Contacts (the sender wrote it).
    public static var senderNameWarning: String {
        // TRANSLATORS: under the name of a new contact taken from a message's header: the sender wrote it, so it may be false.
        L10n.T("The sender wrote this name — check it.")
    }

    /// contacts.SuggestionTooltip: a recipient suggestion's tooltip, the
    /// book and the account's label ("" when unknown).
    public static func suggestionTooltip(book: String, account: String) -> String {
        switch (book.isEmpty, account.isEmpty) {
        case (true, true):
            // TRANSLATORS: tooltip of a recipient suggestion that came from a system address book whose name is unknown.
            return L10n.T("Address book")
        case (false, true):
            return book
        case (true, false):
            return account
        case (false, false):
            return twoParts(book, account)
        }
    }

    // MARK: Questions, errors and states

    /// contacts.Question: what a dialog asks; `confirm` is its default
    /// button.
    public struct Question: Sendable, Equatable {
        public var heading: String
        public var body: String
        public var confirm: String
        /// Confirm deletes.
        public var destructive: Bool

        public init(heading: String, body: String, confirm: String, destructive: Bool = false) {
            self.heading = heading
            self.body = body
            self.confirm = confirm
            self.destructive = destructive
        }
    }

    /// contacts.DeleteQuestion: CardDAV has no trash, so it is always
    /// asked; `account` is the `accountLabel`.
    public static func deleteQuestion(name: String, account: String) -> Question {
        Question(
            // TRANSLATORS: the question before a contact is deleted; %s is its name.
            heading: L10n.T("Delete contact %s?", name),
            // TRANSLATORS: %s is the account, e.g. "Microsoft 365 (jana@example.org)".
            body: L10n.T("The contact will also be deleted on the server %s and on every device that synchronizes it.", account),
            confirm: delete,
            destructive: true)
    }

    /// contacts.ConflictQuestion: the server's contact changed since the
    /// form opened (contactConflict).
    public static var conflictQuestion: Question {
        Question(
            heading: L10n.T("The contact changed meanwhile"),
            body: L10n.T("Someone changed the contact on another device. Load the new version; the details you changed stay in the form."),
            // TRANSLATORS: the button that loads the server's new version of a contact into the form.
            confirm: L10n.T("Reload"))
    }

    /// contacts.ConsentBanner: a Microsoft account's sign-in lacks
    /// contacts access; `account` is its address.
    public static func consentBanner(_ account: String) -> String {
        L10n.T("Microsoft needs your permission to access the contacts of %s.", account)
    }

    /// contacts.Allow: the consent banner's button.
    public static var allow: String {
        // TRANSLATORS: the button that signs in again to give Microsoft's permission to the contacts.
        L10n.T("Allow…")
    }

    /// contacts.ConsentOnline: the advice for an account of GNOME Online
    /// Accounts (no Allow…); never shown on macOS, kept for parity.
    public static var consentOnline: String {
        L10n.T("Allow contacts access in the desktop’s Online Accounts settings.")
    }

    /// contacts.Empty: an empty state of the window.
    public enum Empty: Int, Sendable, CaseIterable {
        /// Something to show.
        case none
        /// No address book but Recently Used (W5 a), with Open Settings….
        case noBooks
        /// The selected book has no contact.
        case book
        /// The search found nothing.
        case search
        /// No contact selected (the detail).
        case selection
    }

    /// contacts.EmptyText: an empty state's title and body ("" none).
    public static func emptyText(_ e: Empty) -> (title: String, body: String) {
        switch e {
        case .noBooks:
            return (L10n.T("No Address Books"),
                    L10n.T("Turn on contacts from Microsoft 365 or a CardDAV server in Preferences. Until then, here are the addresses you have written to."))
        case .book:
            return (L10n.T("No Contacts"), "")
        case .search:
            return (L10n.T("No Contacts Found"), L10n.T("Try another name, company or address."))
        case .selection:
            return (L10n.T("No Contact Selected"), "")
        case .none:
            return ("", "")
        }
    }

    /// contacts.OpenPreferences: `.noBooks`' button.
    public static var openPreferences: String { L10n.T("Open Preferences…") }

    /// contacts.ErrorText: the sentence of a failed contacts call by its
    /// code; nil when the contacts have none (the caller then writes its
    /// usual sentence of a failed call). `account` is the `accountLabel` of
    /// the contact's account ("" unknown). Never the daemon's message.
    public static func errorText(_ code: ErrorCode, account: String) -> String? {
        switch code {
        case .contactNotFound:
            return L10n.T("The contact no longer exists.")
        case .addressBookNotFound:
            return L10n.T("The address book no longer exists.")
        case .addressBookReadOnly:
            return L10n.T("This address book cannot be changed.")
        case .contactConflict:
            return conflictQuestion.heading
        case .contactsConsentRequired:
            if !account.isEmpty {
                return consentBanner(account)
            }
            return L10n.T("Microsoft needs your permission to access these contacts.")
        case .offline:
            return L10n.T("Contacts cannot be changed while offline.")
        case .invalidArgument:
            return L10n.T("The contact could not be saved. Check its details.")
        default:
            return nil
        }
    }

    // MARK: Settings → Contacts and the CalDAV wizard

    /// contacts.SyncContacts: a Microsoft account's switch.
    public static var syncContacts: String { L10n.T("Sync Contacts") }

    /// contacts.CardDAVNote: under a CardDAV account's books.
    public static var cardDAVNote: String {
        L10n.T("Edit the server, password and certificate on the Calendar page.")
    }

    /// contacts.SuggestionsGroup: the group of contacts-suggest-scope.
    public static var suggestionsGroup: String {
        // TRANSLATORS: the Preferences group that decides which address books complete the recipients of a new message.
        L10n.T("Recipient Suggestions")
    }

    /// contacts.ScopeText: a choice of Recipient Suggestions.
    public static func scopeText(_ s: ContactSearchScope) -> String {
        if s == .all {
            return L10n.T("All Address Books")
        }
        return L10n.T("Address Books of the Account You Write From")
    }

    /// contacts.Scopes: Recipient Suggestions' choices in their order (the
    /// gschema's ContactsSuggestScope); the first is the default.
    public static let scopes: [ContactSearchScope] = [.account, .all]

    /// contacts.NoContactAccounts: Settings → Contacts without an account
    /// that can have contacts.
    public static var noContactAccounts: String {
        L10n.T("Add a Microsoft 365 account or a CardDAV server to see its contacts here.")
    }

    /// contacts.AddressBooksHeading: the CalDAV wizard's found books.
    public static var addressBooksHeading: String {
        // TRANSLATORS: the heading of the address books a CardDAV server has.
        L10n.T("Address Books")
    }

    /// contacts.CalendarsHeading: the CalDAV wizard's found calendars.
    public static var calendarsHeading: String { L10n.T("Calendars") }

    /// contacts.WizardTitle: the CalDAV wizard's title once it also adds
    /// contacts.
    public static var wizardTitle: String { L10n.T("Add Calendar and Contacts (CalDAV/CardDAV)") }
}

// MARK: - The sidebar and the list (model.go)

extension ContactBook {
    // The window's sidebar and list (W1, W5): All Contacts, then the visible
    // books grouped by account in the order of account.list (each account a
    // heading with its name and kind capsule), then On This Computer with
    // Recently Used last; hidden books are chosen in Settings → Contacts.
    // The list is the books' contacts as contact.list orders them, under
    // index headings (`ContactSummary.section`).

    /// The kind capsules: brand and protocol names, never translated.
    public static let badgeGraph = "M365"
    public static let badgeCardDAV = "CARDDAV"
    public static let badgeEway = "EWAY"

    /// The brand's name, never translated.
    static let microsoft365 = "Microsoft 365"

    /// contacts.Badge: a book's kind capsule ("" for Recently Used and an
    /// unknown kind).
    public static func badge(_ k: AddressBookKind) -> String {
        switch k {
        case .graph: return badgeGraph
        case .carddav: return badgeCardDAV
        case AddressBookKind("eway"): return badgeEway
        default: return ""
        }
    }

    /// contacts.AccountKindBadge: M365 for graph, CARDDAV for caldav, EWAY
    /// for eway.
    public static func accountKindBadge(_ a: Account) -> String {
        switch a.config.protocolKind {
        case .graph: return badgeGraph
        case .caldav: return badgeCardDAV
        case .eway: return badgeEway
        default: return ""
        }
    }

    /// contacts.AccountName: an account's heading, its address, else its
    /// name.
    public static func accountName(_ a: Account) -> String {
        let e = clean(a.config.email, max: API.Limits.maxContactFieldBytes)
        if !e.isEmpty {
            return e
        }
        return clean(a.config.name, max: API.Limits.maxContactFieldBytes)
    }

    /// contacts.AccountLabel: an account in a sentence and the detail's
    /// source line: "Microsoft 365 (jana@firma.cz)" for graph, the name
    /// (else the address) of a CardDAV server; "" for nil.
    public static func accountLabel(_ a: Account?) -> String {
        guard let a else { return "" }
        if a.config.protocolKind == .graph {
            let e = accountName(a)
            return e.isEmpty ? microsoft365 : bookChoiceText(microsoft365, account: e)
        }
        let n = clean(a.config.name, max: API.Limits.maxContactFieldBytes)
        return n.isEmpty ? accountName(a) : n
    }

    /// contacts.BookChoiceText: "Contacts (jana@firma.cz)".
    public static func bookChoiceText(_ book: String, account: String) -> String {
        if account.isEmpty {
            return book
        }
        // TRANSLATORS: an address book with its account, e.g. "Contacts (jana@example.org)"; also a brand with an address.
        return trimSpace(L10n.T("%s (%s)", book, account))
    }

    /// contacts.BookName: Recently Used translated, an empty name as the
    /// kind's default.
    public static func bookName(_ b: AddressBook) -> String {
        if b.id == .recent || b.kind == .recent {
            return recentlyUsed
        }
        let n = clean(b.name, max: API.Limits.maxContactFieldBytes)
        return n.isEmpty ? windowTitle : n
    }

    /// contacts.ItemKind: a sidebar row's kind.
    public enum ItemKind: Int, Sendable {
        case all
        /// An account's heading (not selectable).
        case account
        case book
        /// The heading On This Computer (not selectable).
        case local
        case recent
    }

    /// contacts.Item: a sidebar row.
    public struct Item: Sendable, Equatable {
        public var kind: ItemKind
        /// What the row lists (`.all` for All Contacts); nil for a heading.
        public var book: AddressBookID?
        public var accountId: AccountID?
        public var title = ""
        /// An account heading's kind capsule.
        public var badge = ""
        /// The row's number of contacts (-1 for a heading).
        public var count = 0
        /// The book cannot be changed (Recently Used, a read-only book;
        /// All Contacts never).
        public var readOnly = false

        public init(
            kind: ItemKind, book: AddressBookID? = nil, accountId: AccountID? = nil, title: String = "", badge: String = "",
            count: Int = 0, readOnly: Bool = false
        ) {
            self.kind = kind
            self.book = book
            self.accountId = accountId
            self.title = title
            self.badge = badge
            self.count = count
            self.readOnly = readOnly
        }

        /// contacts.Item.Selectable: a row that lists contacts.
        public var selectable: Bool { kind == .all || kind == .book || kind == .recent }
    }

    /// contacts.Sidebar: the sidebar of books (addressBook.list) and
    /// accounts (account.list, for the order and the headings). All
    /// Contacts is there only with a visible book besides Recently Used.
    public static func sidebar(_ books: [AddressBook], accounts: [Account]) -> [Item] {
        var byAccount: [AccountID: [AddressBook]] = [:]
        var order: [AccountID] = []
        var recent: AddressBook?
        var total = 0
        for b in books {
            if b.id == .recent || b.kind == .recent {
                recent = b
                continue
            }
            if !b.visible {
                continue
            }
            let key = b.accountId ?? ""
            if byAccount[key] == nil {
                order.append(key)
            }
            byAccount[key, default: []].append(b)
            total += max(b.count, 0)
        }
        var out: [Item] = []
        if !order.isEmpty {
            out.append(Item(kind: .all, book: .all, title: allContacts, count: total))
        }
        var known: [AccountID: Account] = [:]
        var accountOrder: [AccountID] = []
        for a in accounts {
            known[a.id] = a
            if byAccount[a.id] != nil {
                accountOrder.append(a.id)
            }
        }
        for id in order where known[id] == nil {
            accountOrder.append(id)
        }
        for id in accountOrder {
            let accountId: AccountID? = id.rawValue.isEmpty ? nil : id
            var h = Item(kind: .account, accountId: accountId, count: -1)
            if let a = known[id] {
                h.title = accountName(a)
                h.badge = accountKindBadge(a)
            } else if let first = byAccount[id]?.first {
                h.badge = badge(first.kind)
            }
            out.append(h)
            for b in byAccount[id] ?? [] {
                out.append(Item(
                    kind: .book, book: b.id, accountId: accountId, title: bookName(b), count: max(b.count, 0),
                    readOnly: b.readOnly || b.searchOnly || !knownKind(b.kind)))
            }
        }
        if let recent {
            out.append(Item(kind: .local, title: onThisComputer, count: -1))
            out.append(Item(kind: .recent, book: .recent, title: recentlyUsed, count: max(recent.count, 0), readOnly: true))
        }
        return out
    }

    static func knownKind(_ k: AddressBookKind) -> Bool {
        k == .graph || k == .carddav
    }

    /// contacts.HasBooks: a sidebar with a book besides Recently Used.
    public static func hasBooks(_ items: [Item]) -> Bool {
        items.contains { $0.kind == .book }
    }

    /// contacts.DefaultSelection: the row selected when the window opens or
    /// the selected row is gone: All Contacts, else Recently Used, else
    /// nothing.
    public static func defaultSelection(_ items: [Item]) -> AddressBookID? {
        if let all = items.first(where: { $0.kind == .all }) {
            return all.book
        }
        return items.first(where: \.selectable)?.book
    }

    /// contacts.Find: the selectable row of a book.
    public static func find(_ items: [Item], _ book: AddressBookID?) -> Item? {
        guard let book else { return nil }
        return items.first { $0.selectable && $0.book == book }
    }

    /// contacts.ListSubtitle: "All Contacts · 412", "Recently Used · 318
    /// addresses", "Family · 82 contacts"; `total` is contact.list's.
    public static func listSubtitle(_ item: Item, total: Int) -> String {
        switch item.kind {
        case .all:
            return twoParts(item.title, String(total))
        case .recent:
            return twoParts(item.title, addressCount(total))
        default:
            return twoParts(item.title, contactCount(total))
        }
    }

    /// contacts.SyncedAt: the oldest lastSyncAt of the visible books of the
    /// accounts; nil when no book has one.
    public static func syncedAt(_ books: [AddressBook]) -> Date? {
        var oldest: Date?
        for b in books where b.visible && b.id != .recent {
            guard let t = b.lastSyncAt else { continue }
            if oldest == nil || t < oldest! {
                oldest = t
            }
        }
        return oldest
    }

    /// contacts.StatusLine: `syncedLine` with the oldest synchronisation,
    /// "" without one.
    public static func statusLine(_ books: [AddressBook], now: Date) -> String {
        guard let t = syncedAt(books) else { return "" }
        return syncedLine(t, now: now)
    }

    /// contacts.Writable: a book a contact can be created in (visible or
    /// not): not Recently Used, not read only, not search only, of a known
    /// kind.
    public static func writable(_ b: AddressBook) -> Bool {
        b.id != .recent && !b.readOnly && !b.searchOnly && knownKind(b.kind)
    }

    /// contacts.Row: a row of the list, a section heading or a contact.
    public struct Row: Sendable, Equatable {
        /// A section's letter ("" for a contact's row).
        public var heading = ""
        /// nil for a heading.
        public var contact: ContactSummary?
        /// The contact's name (`summaryTitle`); `detail` its company, else
        /// its address.
        public var title = ""
        public var detail = ""
        public var initials = ""

        public init(heading: String = "", contact: ContactSummary? = nil, title: String = "", detail: String = "", initials: String = "") {
            self.heading = heading
            self.contact = contact
            self.title = title
            self.detail = detail
            self.initials = initials
        }

        public var isHeading: Bool { !heading.isEmpty }
    }

    /// contacts.Rows: a heading before the first contact of each section
    /// (consecutive contacts with the same section, in contact.list's
    /// order; an empty section is "#"); a search's rows have no headings.
    public static func rows(_ list: [ContactSummary], headings: Bool) -> [Row] {
        var out: [Row] = []
        var last = "\u{0}"
        for c in list {
            let sec = sectionOf(c)
            if headings && sec != last {
                out.append(Row(heading: sec))
                last = sec
            }
            let title = summaryTitle(c)
            var detail = clean(c.org, max: API.Limits.maxContactFieldBytes)
            if detail.isEmpty || detail == title {
                detail = clean(c.email, max: API.Limits.maxContactFieldBytes)
            }
            if detail == title {
                detail = ""
            }
            out.append(Row(contact: c, title: title, detail: detail, initials: initials(title, address: c.email)))
        }
        return out
    }

    /// contacts.SectionOf: the section cleaned to one upper case
    /// character, "#" when empty.
    public static func sectionOf(_ c: ContactSummary) -> String {
        guard let r = clean(c.section, max: 16).unicodeScalars.first else { return "#" }
        return upper(r)
    }

    // MARK: Layout

    /// contacts.Breakpoints: below `sidebar` the sidebar collapses, below
    /// `detail` the detail covers the list with a back button.
    public struct Breakpoints: Sendable, Equatable {
        public var sidebar: Double
        public var detail: Double

        public init(sidebar: Double, detail: Double) {
            self.sidebar = sidebar
            self.detail = detail
        }
    }

    /// contacts.MacBreakpoints, in points (W5 d).
    public static let macBreakpoints = Breakpoints(sidebar: 720, detail: 520)
    /// contacts.GTKBreakpoints.
    public static let gtkBreakpoints = Breakpoints(sidebar: 900, detail: 600)

    /// contacts.Fold: how a window of a width is laid out.
    public struct Fold: Sendable, Equatable {
        /// The sidebar is hidden (shown over the list on demand).
        public var sidebarCollapsed = false
        /// One column; a selected contact's detail covers the list.
        public var detailOverList = false

        public init(sidebarCollapsed: Bool = false, detailOverList: Bool = false) {
            self.sidebarCollapsed = sidebarCollapsed
            self.detailOverList = detailOverList
        }
    }

    /// contacts.FoldFor.
    public static func foldFor(width: Double, _ bp: Breakpoints = macBreakpoints) -> Fold {
        Fold(sidebarCollapsed: width < bp.sidebar, detailOverList: width < bp.detail)
    }
}
