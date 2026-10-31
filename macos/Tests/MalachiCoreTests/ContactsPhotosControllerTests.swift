// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/contacts avatar_test.go and ui/internal/contactsctl
// photos_test.go, ported: the avatar rules and the photos controller
// against a fake daemon that answers contact.lookup `withPhotos` from `refs`
// (address → contact with a photo) and contact.photo from `revs` (contact →
// revision; absent = none). Its answers come back on the main actor; a
// held daemon keeps contact.photo answers until the test releases them (the
// Go tests' one answer at a time). Fictional people only.

private struct PhotoHarnessTimeout: Error {}

private final class PhotoDaemon: ContactsCaller, @unchecked Sendable {
    private let lock = NSLock()
    private var calls: [(String, Data)] = []
    private var refsStore: [String: ContactPhotoRef] = [:]
    private var revsStore: [ContactID: String] = [:]
    private var failures: [String: RPCError] = [:]
    private var holding = false
    private var held: [CheckedContinuation<Void, Never>] = []

    func setRef(_ a: String, _ r: ContactPhotoRef) { lock.withLock { refsStore[a] = r } }
    func setRev(_ id: ContactID, _ rev: String?) { lock.withLock { revsStore[id] = rev } }
    func fail(_ method: String, _ e: RPCError) { lock.withLock { failures[method] = e } }

    func count(_ method: String) -> Int {
        lock.withLock { calls.filter { $0.0 == method }.count }
    }

    func params<T: Decodable>(_ method: String, as type: T.Type) -> [T] {
        lock.withLock { calls.filter { $0.0 == method }.map(\.1) }.compactMap { try? JSONCoding.decoder().decode(type, from: $0) }
    }

    func photosAsked() -> [ContactID] {
        params(API.ContactPhoto.name, as: ContactPhotoParams.self).map(\.contactId)
    }

    /// contact.photo answers wait for `release`.
    func hold() { lock.withLock { holding = true } }

    var heldCount: Int { lock.withLock { held.count } }

    /// Lets `n` held answers go (all when nil) and stops holding when all
    /// went.
    func release(_ n: Int? = nil) {
        let go = lock.withLock { () -> [CheckedContinuation<Void, Never>] in
            let k = min(n ?? held.count, held.count)
            let out = Array(held.prefix(k))
            held.removeFirst(k)
            if n == nil {
                holding = false
            }
            return out
        }
        for c in go {
            c.resume()
        }
    }

    func call<M: RPCMethod>(_ method: M.Type, _ params: M.Params, timeout: Duration?) async throws -> M.Result {
        let raw = try JSONCoding.encoder().encode(params)
        let (fail, wait) = lock.withLock { () -> (RPCError?, Bool) in
            calls.append((M.name, raw))
            return (failures[M.name], holding && M.name == API.ContactPhoto.name)
        }
        if wait {
            await withCheckedContinuation { (c: CheckedContinuation<Void, Never>) in
                let resumeNow = lock.withLock { () -> Bool in
                    if !holding {
                        return true
                    }
                    held.append(c)
                    return false
                }
                if resumeNow {
                    c.resume()
                }
            }
        }
        if let fail {
            throw fail
        }
        let out: any Encodable
        switch M.name {
        case API.ContactLookup.name:
            let p = try JSONCoding.decoder().decode(ContactLookupParams.self, from: raw)
            var r = ContactLookupResult()
            lock.withLock {
                for a in p.addresses {
                    if let ref = refsStore[a] {
                        r.known[a] = ref.contactId
                        if p.withPhotos {
                            r.photos[a] = ref
                        }
                    }
                }
            }
            out = r
        case API.ContactPhoto.name:
            let p = try JSONCoding.decoder().decode(ContactPhotoParams.self, from: raw)
            if let rev = lock.withLock({ revsStore[p.contactId] }) {
                out = ContactPhotoResult(state: .ready, mime: "image/jpeg", data: Data("jpeg:\(p.contactId.rawValue):\(rev)".utf8),
                                         revision: rev)
            } else {
                out = ContactPhotoResult(state: .none)
            }
        default:
            throw RPCError(code: .methodNotFound, message: M.name)
        }
        return try JSONCoding.decoder().decode(M.Result.self, from: JSONCoding.encoder().encode(out))
    }
}

/// loop.After and loop.fire.
@MainActor
private final class PhotoTimers {
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

@MainActor
private func waitFor(_ cond: @MainActor () -> Bool) async throws {
    let deadline = ContinuousClock.now + .seconds(5)
    while !cond() {
        try await Task.sleep(for: .milliseconds(2))
        if ContinuousClock.now > deadline {
            throw PhotoHarnessTimeout()
        }
    }
}

/// loop.run: waits until no call runs.
@MainActor
private func settle(_ o: ContactsOptions) async throws {
    try await Task.sleep(for: .milliseconds(2))
    try await waitFor { o.calls.pending == 0 }
}

/// loop.fire: the timers, then what they started.
@MainActor
private func fire(_ t: PhotoTimers, _ o: ContactsOptions) async throws {
    t.fire()
    try await settle(o)
    // An answer may have scheduled another lookup or started photos.
    while !t.waiting.isEmpty {
        t.fire()
        try await settle(o)
    }
}

@MainActor
private func newPhotos(_ d: PhotoDaemon, _ t: PhotoTimers) -> (ContactsPhotosController, Keys) {
    let o = ContactsOptions(caller: d, format: ContactBook.englishStrftime, after: { delay, f in t.after(delay, f) })
    let p = ContactsPhotosController(options: o)
    let keys = Keys()
    p.onPhoto = { keys.list.append($0) }
    return (p, keys)
}

@MainActor
private final class Keys {
    var list: [String] = []
}

private typealias Photos = ContactsPhotosController
private typealias Ref = ContactsPhotosController.ContactRef

// MARK: - avatar_test.go

@Suite struct ContactAvatarTests {
    @Test func avatar() {
        let row = ContactBook.Row(contact: ContactSummary(id: "p_1", bookId: "b_1"), initials: "JN")
        let a = ContactBook.avatarOfRow(row, photo: .ready)
        #expect(a.contact == "p_1" && a.initials == "JN" && a.mayShowPhoto)
        #expect(ContactBook.avatarOfRow(row, photo: .remote).contact == "p_1")
        let none = ContactBook.avatarOfRow(row, photo: .none)
        #expect(!none.mayShowPhoto && none.initials == "JN")
        #expect(!ContactBook.avatarOfRow(row, photo: "bogus").mayShowPhoto, "an unknown state")
        let recent = ContactBook.Row(contact: ContactSummary(id: "r_1", bookId: .recent))
        #expect(!ContactBook.avatarOfRow(recent, photo: .ready).mayShowPhoto, "a recent contact with a photo")
        var d = ContactBook.Detail()
        d.initials = "A"
        let detail = ContactBook.avatarOfDetail(ContactCard(id: "p_2", bookId: "b_1"), d, photo: .ready)
        #expect(detail.contact == "p_2" && detail.initials == "A")
        #expect(!ContactBook.avatarOfDetail(ContactCard(id: "r_9"), ContactBook.Detail(), photo: .ready).mayShowPhoto, "recent detail")

        // Senders: by the exact address, never for the user's own messages.
        let s = ContactBook.avatarOfSender(address: " Jana@Firma.CZ ", name: "Jana Nová", mine: false)
        #expect(s.sender == "jana@firma.cz" && s.initials == "JN" && s.contact == nil)
        let own = ContactBook.avatarOfSender(address: "jana@firma.cz", name: "Jana", mine: true)
        #expect(!own.mayShowPhoto && own.initials == "J")
        #expect(!ContactBook.avatarOfSender(address: "", name: "Jana", mine: false).mayShowPhoto)
        #expect(!ContactBook.avatarOfSender(address: "undisclosed", name: "", mine: false).mayShowPhoto)

        // monochrome-avatars greys initials, never a photo.
        #expect(ContactBook.monochrome(setting: true, photoShown: false))
        #expect(!ContactBook.monochrome(setting: true, photoShown: true))
        #expect(!ContactBook.monochrome(setting: false, photoShown: false))
    }

    @Test func nearRows() {
        let cases: [(Int, Int, Int, Int, Int)] = [
            (10, 19, 100, 0, 30), (40, 49, 100, 30, 60), (90, 99, 100, 80, 100), (0, 4, 3, 0, 3),
            (5, 4, 100, 0, 0), (0, 0, 0, 0, 0), (-3, 2, 10, 0, 6),
        ]
        for c in cases {
            let r = ContactBook.nearRows(first: c.0, last: c.1, count: c.2)
            #expect(r.from == c.3 && r.to == c.4, "nearRows(\(c.0), \(c.1), \(c.2))")
        }
    }
}

// MARK: - photos_test.go

@MainActor
@Suite(.serialized) struct ContactsPhotosControllerTests {
    @Test func bySender() async throws {
        let d = PhotoDaemon()
        let t = PhotoTimers()
        d.setRef("jana@firma.cz", ContactPhotoRef(contactId: "p_1", state: .ready))
        d.setRef("petr@firma.cz", ContactPhotoRef(contactId: "p_2", state: .remote))
        d.setRef("eva@firma.cz", ContactPhotoRef(contactId: "p_3", state: .none)) // a contact without a photo
        d.setRev("p_1", "e1")
        let (p, keys) = newPhotos(d, t)

        p.show("list", senders: ["Jana@Firma.cz", "petr@firma.cz", "eva@firma.cz", "x@y.cz"])
        p.show("conversation", senders: ["jana@firma.cz"])
        try await settle(p.options)
        #expect(d.count(API.ContactLookup.name) == 0, "asked before the debounce")
        try await fire(t, p.options)
        let lps = d.params(API.ContactLookup.name, as: ContactLookupParams.self)
        #expect(lps.count == 1)
        #expect(lps.first?.withPhotos == true)
        #expect(lps.first?.addresses == ["jana@firma.cz", "petr@firma.cz", "eva@firma.cz", "x@y.cz"])
        // p_1 ready, p_2 remote: both asked lazily; p_3 and x@y.cz not.
        #expect(Set(d.photosAsked()) == ["p_1", "p_2"] && d.photosAsked().count == 2)
        let ph = try #require(p.forSender("JANA@firma.cz"))
        #expect(ph.data == Data("jpeg:p_1:e1".utf8) && ph.mime == "image/jpeg" && ph.key == "p_1\u{0}e1")
        #expect(p.forSender("petr@firma.cz") == nil, "a remote contact the daemon has no photo of")
        #expect(p.photo(for: ContactBook.avatarOfSender(address: "eva@firma.cz", name: "Eva", mine: false)) == nil)
        #expect(p.photo(for: ContactBook.avatarOfSender(address: "jana@firma.cz", name: "Jana", mine: false))?.contact == "p_1")
        #expect(keys.list == [Photos.contactKey("p_1"), Photos.senderKey("jana@firma.cz")])

        // Answers are kept, also the negative ones: nothing is asked again.
        p.show("list", senders: ["x@y.cz", "eva@firma.cz", "jana@firma.cz"])
        try await fire(t, p.options)
        #expect(d.count(API.ContactLookup.name) == 1 && d.count(API.ContactPhoto.name) == 2, "asked again")
    }

    @Test func batches() async throws {
        let d = PhotoDaemon()
        let t = PhotoTimers()
        let (p, _) = newPhotos(d, t)
        let many = (0..<450).map { "a\($0)@x.cz" }
        p.show("list", senders: Array(many[..<100]))
        p.show("list", senders: many)
        try await fire(t, p.options)
        #expect(d.count(API.ContactLookup.name) == 3)
        #expect(d.params(API.ContactLookup.name, as: ContactLookupParams.self).allSatisfy {
            $0.addresses.count <= API.Limits.maxContactLookupAddresses
        }, "a batch over the limit")
        // What a view no longer shows when the debounce runs is not asked.
        p.show("list", senders: ["gone@x.cz"])
        p.show("list")
        try await fire(t, p.options)
        #expect(d.count(API.ContactLookup.name) == 3, "asked for an address no view shows")
        // An address on its way is not asked twice.
        p.show("list", senders: ["b@x.cz"])
        t.fire()
        p.show("other", senders: ["b@x.cz"])
        try await fire(t, p.options)
        #expect(d.count(API.ContactLookup.name) == 4, "asked twice")
    }

    @Test func byContact() async throws {
        let d = PhotoDaemon()
        let t = PhotoTimers()
        d.setRev("p_1", "e1")
        let (p, keys) = newPhotos(d, t)
        p.show("contacts", refs: [
            Ref(id: "p_1", book: "b_1", state: .ready),
            Ref(id: "p_2", book: "b_1", state: .remote),
            Ref(id: "p_3", book: "b_1"),
            Ref(id: "r_1", book: .recent, state: .ready),
        ])
        try await settle(p.options)
        #expect(Set(d.photosAsked()) == ["p_1", "p_2"] && d.photosAsked().count == 2)
        #expect(p.forContact("p_1") != nil)
        #expect(p.forContact("p_2") == nil, "p_2 has none")
        #expect(keys.list == [Photos.contactKey("p_1")])
        // "none" is remembered.
        p.show("contacts", refs: [Ref(id: "p_2", book: "b_1", state: .remote)])
        try await settle(p.options)
        #expect(d.count(API.ContactPhoto.name) == 2, "none asked again")
        // The list reports a new state: asked again.
        d.setRev("p_2", "e7")
        p.show("contacts", refs: [Ref(id: "p_2", book: "b_1", state: .ready)])
        try await settle(p.options)
        #expect(p.forContact("p_2")?.revision == "e7", "new state")
    }

    @Test func concurrency() async throws {
        let d = PhotoDaemon()
        let t = PhotoTimers()
        var refs: [Ref] = []
        for i in 0..<10 {
            let id = ContactID("p_\(i)")
            d.setRev(id, "e")
            refs.append(Ref(id: id, book: "b_1", state: .remote))
        }
        let (p, _) = newPhotos(d, t)
        d.hold()
        p.show("contacts", refs: refs)
        #expect(p.options.calls.pending == Photos.maxPhotoFetches, "at once")
        try await waitFor { d.heldCount == Photos.maxPhotoFetches }
        // Nearest first; the next start as the first answers come.
        #expect(Set(d.photosAsked()) == ["p_0", "p_1", "p_2", "p_3"])
        d.release(1)
        try await waitFor { d.count(API.ContactPhoto.name) == Photos.maxPhotoFetches + 1 }
        #expect(d.photosAsked().last == "p_4")
        // What scrolled away is not asked.
        p.show("contacts", refs: Array(refs[..<6]))
        d.release()
        try await settle(p.options)
        #expect(d.count(API.ContactPhoto.name) == 6)
    }

    @Test func changed() async throws {
        let d = PhotoDaemon()
        let t = PhotoTimers()
        d.setRef("jana@firma.cz", ContactPhotoRef(contactId: "p_1", state: .ready))
        d.setRev("p_1", "e1")
        d.setRev("p_9", "e1")
        let (p, keys) = newPhotos(d, t)
        p.show("list", senders: ["jana@firma.cz", "x@y.cz"])
        p.show("contacts", refs: [Ref(id: "p_9", book: "b_2", state: .ready)])
        try await fire(t, p.options)
        #expect(d.count(API.ContactPhoto.name) == 2, "setup")
        keys.list = []

        // The recent book only: nothing.
        p.changed(ContactsChangedNotification(bookIds: [.recent]))
        try await fire(t, p.options)
        #expect(d.count(API.ContactLookup.name) == 1 && d.count(API.ContactPhoto.name) == 2, "the recent book changed something")

        // Book b_1 changed: senders (book unknown) and negatives are asked
        // again, p_9 of b_2 is not; the photo stays shown meanwhile.
        d.setRef("x@y.cz", ContactPhotoRef(contactId: "p_5", state: .ready))
        d.setRev("p_5", "e1")
        p.changed(ContactsChangedNotification(accountId: "acc_m", bookIds: ["b_1"]))
        #expect(p.forSender("jana@firma.cz") != nil, "the photo went away before the new answer")
        try await fire(t, p.options)
        let lp = d.params(API.ContactLookup.name, as: ContactLookupParams.self).last
        #expect(lp?.addresses == ["jana@firma.cz", "x@y.cz"], "asked again")
        #expect(Set(d.photosAsked().dropFirst(2)) == ["p_1", "p_5"] && d.photosAsked().count == 4)
        // p_1's revision did not change: no redraw; x@y.cz got a photo.
        #expect(keys.list == [Photos.senderKey("x@y.cz"), Photos.contactKey("p_5"), Photos.senderKey("x@y.cz")])

        // All books: a new revision redraws; a removed photo too.
        keys.list = []
        d.setRev("p_1", "e2")
        d.setRev("p_9", nil)
        p.changed(ContactsChangedNotification(accountId: "acc_m"))
        try await fire(t, p.options)
        #expect(p.forSender("jana@firma.cz")?.revision == "e2", "new revision")
        #expect(p.forContact("p_9") == nil, "removed photo kept")
        #expect(keys.list.contains(Photos.contactKey("p_1")) && keys.list.contains(Photos.senderKey("jana@firma.cz"))
            && keys.list.contains(Photos.contactKey("p_9")))

        // An answer on its way when the books change is dropped and asked
        // again.
        p.changed(ContactsChangedNotification())
        let n = d.count(API.ContactLookup.name)
        t.fire() // the flush
        p.changed(ContactsChangedNotification())
        try await settle(p.options)
        #expect(p.forSender("jana@firma.cz") != nil, "photo lost")
        try await fire(t, p.options)
        #expect(d.count(API.ContactLookup.name) == n + 2, "stale answer not asked again")
    }

    @Test func failures() async throws {
        let d = PhotoDaemon()
        let t = PhotoTimers()
        d.fail(API.ContactLookup.name, RPCError(code: .unavailable, message: "x"))
        let (p, _) = newPhotos(d, t)
        p.show("list", senders: ["jana@firma.cz"])
        try await fire(t, p.options)
        p.show("list", senders: ["jana@firma.cz"])
        try await fire(t, p.options)
        #expect(d.count(API.ContactLookup.name) == 1, "a failed lookup repeated")
        d.fail(API.ContactPhoto.name, RPCError(code: .unavailable, message: "x"))
        p.show("contacts", refs: [Ref(id: "p_1", book: "b_1", state: .remote)])
        try await settle(p.options)
        p.show("contacts", refs: [Ref(id: "p_1", book: "b_1", state: .remote)])
        try await settle(p.options)
        #expect(d.count(API.ContactPhoto.name) == 1, "a failed photo repeated")
        // A change asks again.
        p.changed(ContactsChangedNotification())
        try await fire(t, p.options)
        #expect(d.count(API.ContactLookup.name) == 2 && d.count(API.ContactPhoto.name) == 2, "not asked after a change")
    }

    @Test func lru() async throws {
        let d = PhotoDaemon()
        let t = PhotoTimers()
        let (p, _) = newPhotos(d, t)
        func ref(_ i: Int) -> Ref {
            let id = ContactID("p_\(i)")
            d.setRev(id, "e")
            return Ref(id: id, book: "b_1", state: .ready)
        }
        // Pages of 50 shown one after another: the least recently used
        // photos go, those shown never.
        for page in 0..<5 {
            p.show("contacts", refs: (page * 50..<page * 50 + 50).map(ref))
            try await settle(p.options)
        }
        #expect(p.cache.count == Photos.maxPhotos && p.order.count == Photos.maxPhotos, "kept \(p.cache.count)")
        #expect(p.forContact("p_0") == nil, "the oldest kept")
        #expect(p.forContact("p_249") != nil, "a shown one evicted")
        // A shown photo is used again: kept over older ones.
        _ = p.forContact("p_50")
        p.show("contacts", refs: [ref(300)])
        try await settle(p.options)
        #expect(p.forContact("p_50") != nil, "recently used evicted")
        #expect(p.forContact("p_51") == nil, "LRU")
        // More shown than kept: no endless refetching.
        let all = (0..<(Photos.maxPhotos + 20)).map { ref(1000 + $0) }
        let n = d.count(API.ContactPhoto.name)
        p.show("contacts", refs: all)
        try await settle(p.options)
        #expect(d.count(API.ContactPhoto.name) - n == Photos.maxPhotos)
    }

    @Test func ownAndReset() async throws {
        let d = PhotoDaemon()
        let t = PhotoTimers()
        d.setRef("jana@firma.cz", ContactPhotoRef(contactId: "p_1", state: .ready))
        d.setRev("p_1", "e1")
        let (p, _) = newPhotos(d, t)
        // The user's own message never asks (its avatar has no sender).
        let own = ContactBook.avatarOfSender(address: "jana@firma.cz", name: "Jana", mine: true)
        p.show("list", senders: [own.sender])
        try await fire(t, p.options)
        #expect(d.count(API.ContactLookup.name) == 0, "own message asked")
        #expect(p.photo(for: own) == nil, "own message has a photo")
        p.show("list", senders: ["jana@firma.cz"])
        try await fire(t, p.options)
        #expect(p.forSender("jana@firma.cz") != nil)
        let heard = Keys()
        let token = p.observe { heard.list.append($0) }
        p.reset()
        #expect(p.forSender("jana@firma.cz") == nil, "not reset")
        #expect(heard.list == [""], "observers redraw everything")
        token.cancel()
    }
}
