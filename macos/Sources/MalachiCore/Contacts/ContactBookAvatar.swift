// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Contact photos, read only (docs/plans/address-book.md §9, F1–F11), a port
// of ui/internal/contacts avatar.go. An avatar shows a contact's photo
// instead of its initials when there is one: in the Contacts window's list
// and detail (F9) and wherever mail draws a sender's avatar (F10: the
// message list with show-avatars, the conversation's cards, the chat's
// bubbles of Mail and the Board). The bytes come from the daemon only
// (re-encoded by it, F5) through `ContactsPhotosController`; the views
// decode them and clip the circle. Without a photo the avatar is what it
// was.

import Foundation

extension ContactBook {
    /// contacts.PhotoState: a contact's photo as contact.list, contact.get
    /// and contact.lookup `withPhotos` report it. Any value other than
    /// `.ready` and `.remote` is no photo.
    public typealias PhotoState = ContactPhotoAvailability

    /// contacts.Avatar: what an avatar shows: `initials`, and the photo of
    /// `contact` (by id, the Contacts window) or of `sender`'s contact (by
    /// address, mail) when `ContactsPhotosController` has it. Both empty:
    /// initials only, never a photo.
    public struct Avatar: Sendable, Equatable {
        public var initials: String
        /// The contact whose photo replaces the initials.
        public var contact: ContactID?
        /// The From address (`normalAddress`) whose contact's photo
        /// replaces the initials; matched only by the exact address, never
        /// by name (F10). "" none.
        public var sender: String

        public init(initials: String = "", contact: ContactID? = nil, sender: String = "") {
            self.initials = initials
            self.contact = contact
            self.sender = sender
        }

        /// contacts.Avatar.MayShowPhoto: an avatar that asks the photos.
        public var mayShowPhoto: Bool { contact != nil || !sender.isEmpty }
    }

    /// contacts.NormalAddress: an address as contact.lookup's keys are:
    /// trimmed, lower case.
    public static func normalAddress(_ a: String) -> String {
        trimSpace(a).lowercased()
    }

    /// contacts.IsRecent: a contact of the recent book (its book, or the
    /// "r_" id contact.get takes): recent addresses never have a photo.
    public static func isRecent(book: AddressBookID, id: ContactID) -> Bool {
        book == .recent || id.rawValue.hasPrefix("r_")
    }

    /// contacts.AvatarOfRow: the avatar of a row of the list (`rows`) whose
    /// summary reports `photo`.
    public static func avatarOfRow(_ r: Row, photo: PhotoState) -> Avatar {
        var a = Avatar(initials: r.initials)
        if mayHave(photo), let c = r.contact, !c.id.rawValue.isEmpty, !isRecent(book: c.bookId, id: c.id) {
            a.contact = c.id
        }
        return a
    }

    /// contacts.AvatarOfDetail: the avatar of the detail `d` of `card`,
    /// whose card reports `photo`.
    public static func avatarOfDetail(_ card: ContactCard, _ d: Detail, photo: PhotoState) -> Avatar {
        var a = Avatar(initials: d.initials)
        if mayHave(photo), !card.id.rawValue.isEmpty, !isRecent(book: card.bookId, id: card.id) {
            a.contact = card.id
        }
        return a
    }

    /// contacts.AvatarOfSender: the avatar of a message's sender: initials
    /// of `name` (else of `address`) and the photo of the contact with
    /// exactly `address`. The user's own messages (`mine`: the row of a
    /// sent or outbox folder, a bubble on the right) never get a photo; the
    /// "You" avatar stays (F10).
    public static func avatarOfSender(address: String, name: String, mine: Bool) -> Avatar {
        var a = Avatar(initials: initials(name, address: address))
        let n = normalAddress(address)
        if !mine && n.contains("@") {
            a.sender = n
        }
        return a
    }

    /// contacts.PhotoState.MayHave: a state whose contact has, or may
    /// have, a photo.
    public static func mayHave(_ s: PhotoState) -> Bool { s == .ready || s == .remote }

    /// contacts.Monochrome: whether the monochrome-avatars setting greys
    /// this avatar: it applies to initials only, never to a photo shown.
    public static func monochrome(setting: Bool, photoShown: Bool) -> Bool { setting && !photoShown }

    /// contacts.NearRows: the range `from..<to` of rows whose photos are
    /// asked for: the visible rows `first...last` and as many again above
    /// and below (one screen each way, as the chat asks for bodies near
    /// the visible part), clamped to `count` rows. Nothing visible
    /// (`last < first`) is an empty range.
    public static func nearRows(first: Int, last: Int, count: Int) -> (from: Int, to: Int) {
        if count <= 0 || last < first {
            return (0, 0)
        }
        let f = max(first, 0)
        let l = min(last, count - 1)
        if l < f {
            return (0, 0)
        }
        let span = l - f + 1
        return (max(f - span, 0), min(l + span + 1, count))
    }
}
