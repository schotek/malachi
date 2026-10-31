// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Contact photos (docs/plans/address-book.md §9, F8–F10), a port of
// ui/internal/contactsctl photos.go (`Photos`): one for the application,
// shared by the views that draw avatars. Each view tells it what it shows
// near its viewport (`show`: contacts by id for the Contacts window, sender
// addresses for mail); it asks the daemon only for those:
//
//   - senders: contact.lookup `withPhotos` for the addresses it has no
//     answer for, gathered for `lookupDelay` (`ContactsOptions.after`) and
//     sent in batches of `maxContactLookupAddresses`; every answer is kept
//     by address, also "no contact with a photo";
//   - photos: contact.photo for a contact whose photo is ready or remote
//     only while a view shows it, nearest first, at most
//     `maxPhotoFetches` at a time; "none" is remembered;
//   - the bytes (re-encoded by the daemon, a JPEG or PNG ≤ 256 px) of the
//     `maxPhotos` most recently used photos are kept; the views decode
//     them (and may keep their decoded image by `Photo.key`).
//
// notify.contactsChanged (`changed`) forgets what the changed books may
// have changed and asks again for what is shown; a view redraws on
// `onPhoto` (or an observer of `observe`). A failed call is not repeated
// until the next change.

import Foundation

@MainActor
public final class ContactsPhotosController {
    /// contactsctl.MaxPhotos: how many photos' bytes are kept (F9).
    public static let maxPhotos = 200
    /// contactsctl.MaxPhotoFetches: how many contact.photo calls run at
    /// once (F3).
    public static let maxPhotoFetches = 4
    /// contactsctl.LookupDelay: gathers the senders of a scrolled list into
    /// one contact.lookup.
    public static let lookupDelay: Duration = .milliseconds(150)

    /// contactsctl.Photo: a contact's photo as the daemon sent it.
    public struct Photo: Sendable, Equatable {
        public var contact: ContactID
        public var revision: String
        public var mime: String
        public var data: Data

        public init(contact: ContactID, revision: String, mime: String, data: Data) {
            self.contact = contact
            self.revision = revision
            self.mime = mime
            self.data = data
        }

        /// Names this version of the photo: a view's decoded image of the
        /// same key is still good.
        public var key: String { contact.rawValue + "\u{0}" + revision }
    }

    /// contactsctl.ContactRef: a contact a view shows, with the photo state
    /// its summary or card reports.
    public struct ContactRef: Sendable, Equatable {
        public var id: ContactID
        public var book: AddressBookID
        public var state: ContactBook.PhotoState

        public init(id: ContactID, book: AddressBookID, state: ContactBook.PhotoState = .none) {
            self.id = id
            self.book = book
            self.state = state
        }
    }

    /// contactsctl.ContactKey: the key `onPhoto` reports for a contact's
    /// avatars (by id).
    public nonisolated static func contactKey(_ id: ContactID) -> String { "contact:" + id.rawValue }
    /// contactsctl.SenderKey: the key of a sender's avatars (by address).
    public nonisolated static func senderKey(_ address: String) -> String { "sender:" + ContactBook.normalAddress(address) }

    /// An observer of `observe`; `cancel` ends it.
    public final class Token {
        private var cancelHandler: (@MainActor () -> Void)?

        init(_ cancel: @escaping @MainActor () -> Void) {
            cancelHandler = cancel
        }

        @MainActor public func cancel() {
            cancelHandler?()
            cancelHandler = nil
        }
    }

    public let options: ContactsOptions
    /// What the avatars of a key (`contactKey`, `senderKey`) show changed
    /// (a photo arrived, changed or went away); they redraw.
    public var onPhoto: (@MainActor (String) -> Void)?

    private var observers: [Int: @MainActor (String) -> Void] = [:]
    private var nextObserver = 0

    private struct View {
        var contacts: [ContactID] = []
        var senders: [String] = []
    }

    private struct SenderEntry {
        /// nil = no contact with a photo.
        var contact: ContactID?
        var state: ContactBook.PhotoState = .none
        /// The lookup failed; asked again after a change.
        var failed = false
        /// The books changed: shown until the new answer.
        var stale = false
    }

    private var views: [String: View] = [:]

    private var senders: [String: SenderEntry] = [:] // by normal address
    private var queued: [String] = [] // addresses waiting for the lookup
    private var lookupAt = false // a flush is scheduled
    private var asking: Set<String> = [] // addresses of lookups on their way

    private var states: [ContactID: ContactBook.PhotoState] = [:] // from refs and lookups
    private var books: [ContactID: AddressBookID] = [:] // known books of contacts
    private(set) var cache: [ContactID: Photo] = [:]
    private(set) var order: [ContactID] = [] // LRU, most recent last
    private var none: Set<ContactID> = []
    private var stale: Set<ContactID> = []
    private var failed: Set<ContactID> = []
    private var fetching: Set<ContactID> = []

    private var gen = 0

    public init(options: ContactsOptions) {
        self.options = options
    }

    /// Calls `f` with every key `onPhoto` reports, and with "" when
    /// `reset` forgot everything (every avatar redraws).
    public func observe(_ f: @escaping @MainActor (String) -> Void) -> Token {
        nextObserver += 1
        let id = nextObserver
        observers[id] = f
        return Token { [weak self] in self?.observers[id] = nil }
    }

    private func clear() {
        views = [:]
        senders = [:]
        asking = []
        states = [:]
        books = [:]
        cache = [:]
        order = []
        none = []
        stale = []
        failed = []
        fetching = []
    }

    /// contactsctl.Photos.Show: sets what `view` (any name: "contacts",
    /// "list", "conversation", …) shows near its viewport, nearest first:
    /// contacts by id with the state their summary or card reports (a ref
    /// without a photo is ignored) and sender addresses
    /// (`ContactBook.Avatar.sender`). It replaces the view's previous set;
    /// `show(view, [], [])` when the view closes.
    public func show(_ view: String, refs: [ContactRef] = [], senders addrs: [String] = []) {
        var v = View()
        for r in refs {
            if r.id.rawValue.isEmpty || ContactBook.isRecent(book: r.book, id: r.id) {
                continue
            }
            if !r.book.rawValue.isEmpty {
                books[r.id] = r.book
            }
            if let prev = states[r.id], prev != r.state {
                // The list reports a change the cache may not know of yet.
                if stale.contains(r.id) || cache[r.id] != nil || none.contains(r.id) {
                    stale.insert(r.id)
                }
            }
            states[r.id] = r.state
            if ContactBook.mayHave(r.state) && !v.contacts.contains(r.id) {
                v.contacts.append(r.id)
            }
        }
        for s in addrs {
            let n = ContactBook.normalAddress(s)
            if n.isEmpty || v.senders.contains(n) {
                continue
            }
            v.senders.append(n)
        }
        if v.contacts.isEmpty && v.senders.isEmpty {
            views[view] = nil
        } else {
            views[view] = v
        }
        queueSenders(v.senders)
        scheduleLookup()
        pump()
    }

    /// contactsctl.Photos.ForContact: the photo of a contact's avatar; nil
    /// shows initials.
    public func forContact(_ id: ContactID) -> Photo? {
        guard let ph = cache[id] else { return nil }
        touch(id)
        return ph
    }

    /// contactsctl.Photos.ForSender: the photo of a sender's avatar
    /// (`ContactBook.Avatar.sender`); nil shows initials.
    public func forSender(_ address: String) -> Photo? {
        guard let e = senders[ContactBook.normalAddress(address)], let c = e.contact else { return nil }
        return forContact(c)
    }

    /// contactsctl.Photos.For: the photo of an avatar; nil shows initials.
    public func photo(for a: ContactBook.Avatar) -> Photo? {
        if let c = a.contact, !c.rawValue.isEmpty {
            return forContact(c)
        }
        if !a.sender.isEmpty {
            return forSender(a.sender)
        }
        return nil
    }

    /// contactsctl.Photos.Changed: notify.contactsChanged. The answers the
    /// changed books may have changed are asked for again (every sender's,
    /// as lookups name no book, every "no photo"; all of them when
    /// `bookIds` is empty) for what the views show; until the new answer
    /// the old one stays on screen. A change of the recent book only
    /// changes nothing (recent contacts have no photos).
    public func changed(_ n: ContactsChangedNotification) {
        if (n.accountId?.rawValue ?? "").isEmpty && !n.bookIds.isEmpty && !n.bookIds.contains(where: { $0 != .recent }) {
            return
        }
        let books = self.books
        let affected: (ContactID) -> Bool = { id in
            if n.bookIds.isEmpty {
                return true
            }
            guard let b = books[id] else { return true }
            return n.bookIds.contains(b)
        }
        gen += 1
        asking = []
        // Photos on their way still count against maxPhotoFetches; their
        // answers are dropped and the contacts asked again.
        for id in fetching where affected(id) {
            stale.insert(id)
        }
        for (a, e) in senders {
            // A contact may have been added to any book: "no contact" is
            // always asked again.
            if e.contact == nil || affected(e.contact!) {
                var e = e
                e.stale = true
                senders[a] = e
            }
        }
        none = none.filter { !affected($0) }
        failed = failed.filter { !affected($0) }
        for id in cache.keys where affected(id) {
            stale.insert(id)
        }
        queued = []
        for name in views.keys.sorted() {
            queueSenders(views[name]?.senders ?? [])
        }
        scheduleLookup()
        pump()
    }

    /// contactsctl.Photos.Reset: forgets everything (the daemon
    /// restarted); the views show again. Observers hear "".
    public func reset() {
        gen += 1
        queued = []
        clear()
        for f in observers.values {
            f("")
        }
    }

    // MARK: Lookups

    /// Queues the addresses without a current answer that are not on their
    /// way.
    private func queueSenders(_ addrs: [String]) {
        for a in addrs {
            if let e = senders[a], !e.stale {
                continue
            }
            if asking.contains(a) || queued.contains(a) {
                continue
            }
            queued.append(a)
        }
    }

    private func scheduleLookup() {
        if queued.isEmpty || lookupAt {
            return
        }
        lookupAt = true
        options.after(Self.lookupDelay) { [weak self] in
            guard let self else { return }
            self.lookupAt = false
            self.flush()
        }
    }

    private func flush() {
        let shown = shownSenders()
        // Only what a view still shows.
        let addrs = queued.filter { shown.contains($0) }
        queued = []
        let size = API.Limits.maxContactLookupAddresses
        for start in stride(from: 0, to: addrs.count, by: size) {
            let part = Array(addrs[start..<min(start + size, addrs.count)])
            for a in part {
                asking.insert(a)
            }
            let gen = gen
            options.call(API.ContactLookup.self, ContactLookupParams(addresses: part, withPhotos: true)) { [weak self] r in
                self?.answered(gen, part, r)
            }
        }
    }

    private func answered(_ gen: Int, _ asked: [String], _ r: Result<ContactLookupResult, any Error>) {
        guard gen == self.gen else { return }
        var got: [String: ContactPhotoRef] = [:]
        var failedCall = false
        switch r {
        case .success(let res):
            for (a, ref) in res.photos {
                got[ContactBook.normalAddress(a)] = ref
            }
        case .failure:
            failedCall = true
        }
        for a in asked {
            asking.remove(a)
            let prev = senders[a]
            var next = SenderEntry()
            if failedCall {
                next = SenderEntry(failed: true)
            } else if let ref = got[a], ContactBook.mayHave(ref.state), !ref.contactId.rawValue.isEmpty,
                      !ContactBook.isRecent(book: "", id: ref.contactId) {
                next = SenderEntry(contact: ref.contactId, state: ref.state)
                if let old = states[ref.contactId], old != ref.state {
                    if stale.contains(ref.contactId) || cache[ref.contactId] != nil || none.contains(ref.contactId) {
                        stale.insert(ref.contactId)
                    }
                }
                states[ref.contactId] = ref.state
            }
            senders[a] = next
            if let prev, prev.contact != next.contact {
                report(Self.senderKey(a))
            }
        }
        pump()
    }

    private func shownSenders() -> Set<String> {
        var out = Set<String>()
        for v in views.values {
            out.formUnion(v.senders)
        }
        return out
    }

    // MARK: Photos

    /// The contacts whose photo a view shows and the cache lacks (or holds
    /// stale), nearest first.
    private func wanted() -> [ContactID] {
        shownContacts().filter { id in
            if fetching.contains(id) || failed.contains(id) {
                return false
            }
            let have = cache[id] != nil
            return !((have || none.contains(id)) && !stale.contains(id))
        }
    }

    /// The contacts with a photo the views show, in their order (each
    /// view's nearest first, views by name), at most `maxPhotos`: a photo
    /// shown is never evicted for another one shown.
    private func shownContacts() -> [ContactID] {
        var out: [ContactID] = []
        func add(_ id: ContactID?) {
            guard let id, !id.rawValue.isEmpty, out.count < Self.maxPhotos, ContactBook.mayHave(states[id] ?? .none),
                  !out.contains(id) else { return }
            out.append(id)
        }
        for name in views.keys.sorted() {
            guard let v = views[name] else { continue }
            for id in v.contacts {
                add(id)
            }
            for a in v.senders {
                add(senders[a]?.contact)
            }
        }
        return out
    }

    private func pump() {
        for id in wanted() {
            if fetching.count >= Self.maxPhotoFetches {
                return
            }
            fetch(id)
        }
    }

    private func fetch(_ id: ContactID) {
        fetching.insert(id)
        let gen = gen
        options.call(API.ContactPhoto.self, ContactPhotoParams(contactId: id)) { [weak self] r in
            guard let self else { return }
            self.fetching.remove(id)
            if gen == self.gen {
                self.fetched(id, r)
            }
            self.pump()
        }
    }

    private func fetched(_ id: ContactID, _ r: Result<ContactPhotoResult, any Error>) {
        stale.remove(id)
        let old = cache[id]
        let res: ContactPhotoResult
        switch r {
        case .failure:
            // Keep what is shown; not asked again until a change.
            failed.insert(id)
            return
        case .success(let v):
            res = v
        }
        if res.state != .ready || res.data.isEmpty {
            none.insert(id)
            drop(id)
            if old != nil {
                reportContact(id)
            }
            return
        }
        none.remove(id)
        let ph = Photo(contact: id, revision: res.revision, mime: res.mime, data: res.data)
        cache[id] = ph
        touch(id)
        if order.count > Self.maxPhotos {
            // The least recently used photo no view shows.
            let shown = Set(shownContacts())
            var i = 0
            while i < order.count && order.count > Self.maxPhotos {
                let c = order[i]
                if !shown.contains(c) {
                    drop(c)
                    continue
                }
                i += 1
            }
        }
        if old == nil || old!.key != ph.key || old!.data != ph.data {
            reportContact(id)
        }
    }

    private func touch(_ id: ContactID) {
        order.removeAll { $0 == id }
        order.append(id)
    }

    private func drop(_ id: ContactID) {
        cache[id] = nil
        order.removeAll { $0 == id }
    }

    /// Reports the contact's key and the key of every sender whose contact
    /// it is.
    private func reportContact(_ id: ContactID) {
        report(Self.contactKey(id))
        for a in senders.filter({ $0.value.contact == id }).keys.sorted() {
            report(Self.senderKey(a))
        }
    }

    private func report(_ key: String) {
        onPhoto?(key)
        for f in observers.values {
            f(key)
        }
    }
}
