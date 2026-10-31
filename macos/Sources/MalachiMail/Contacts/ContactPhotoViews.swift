// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

// Contact photos in the views (docs/plans/address-book.md §9, F9, F10): an
// avatar view that may show a photo has a `photoSource`
// (`ContactBook.Avatar`: a contact by id, or a sender by address) and
// registers with the application's `AppContacts`, which hands it the
// decoded image of `ContactsPhotosController`'s photo and tells it when the
// photo of its key arrived, changed or went away. The view draws the photo
// clipped to its circle; without one (none, not yet, or bytes that do not
// decode) it draws its initials as before. The bytes are the daemon's own
// re-encoded JPEG or PNG (F5); nothing here reads mail or a server.

/// A view that shows a contact photo instead of its initials when there is
/// one (`AvatarView`, `ContactInitialsView`).
@MainActor
protocol ContactPhotoDisplay: AnyObject {
    var photoSource: ContactBook.Avatar? { get }
    /// The photo of `key` changed ("" = every photo); the view redraws when
    /// it is its own.
    func contactPhotoChanged(_ key: String)
}

extension ContactPhotoDisplay {
    /// Whether `key` (`ContactsPhotosController.contactKey`/`senderKey`, ""
    /// = all) names this view's photo.
    func photoKeyMatches(_ key: String) -> Bool {
        guard let a = photoSource, a.mayShowPhoto else { return false }
        if key.isEmpty {
            return true
        }
        if let c = a.contact, key == ContactsPhotosController.contactKey(c) {
            return true
        }
        return !a.sender.isEmpty && key == ContactsPhotosController.senderKey(a.sender)
    }
}

/// Draws a photo filling `rect`, clipped to its circle (a centred square
/// of the image, should a photo ever not be square).
@MainActor
enum ContactPhotoDrawing {
    static func draw(_ image: NSImage, in rect: NSRect) {
        guard let ctx = NSGraphicsContext.current else { return }
        ctx.saveGraphicsState()
        defer { ctx.restoreGraphicsState() }
        NSBezierPath(ovalIn: rect).addClip()
        ctx.imageInterpolation = .high
        let s = image.size
        let side = min(s.width, s.height)
        let from = NSRect(x: (s.width - side) / 2, y: (s.height - side) / 2, width: side, height: side)
        image.draw(in: rect, from: from, operation: .sourceOver, fraction: 1, respectFlipped: true, hints: nil)
    }
}

/// The application's photos as the views use them: the controller, the
/// registered avatar views (weakly), the decoded images by `Photo.key` and
/// the sets the views last showed (asked again after a reset).
@MainActor
final class ContactPhotoViews {
    /// How many decoded images are kept (the controller keeps the bytes of
    /// as many photos).
    static let maxImages = ContactsPhotosController.maxPhotos

    let photos: ContactsPhotosController
    private let views = NSHashTable<AnyObject>.weakObjects()
    private var images: [String: NSImage] = [:]
    private var imageOrder: [String] = []
    /// Photos whose bytes did not decode (initials instead), by key.
    private var undecodable: Set<String> = []
    private var shown: [String: (refs: [ContactsPhotosController.ContactRef], senders: [String])] = [:]
    private var token: ContactsPhotosController.Token?

    init(options: ContactsOptions) {
        photos = ContactsPhotosController(options: options)
        token = photos.observe { [weak self] key in self?.broadcast(key) }
    }

    /// Registers a view to hear about its photo (kept weakly; a view
    /// registers once).
    func register(_ v: any ContactPhotoDisplay) {
        if !views.contains(v as AnyObject) {
            views.add(v as AnyObject)
        }
    }

    /// `ContactsPhotosController.show`, remembered for a reset.
    func show(_ view: String, refs: [ContactsPhotosController.ContactRef] = [], senders: [String] = []) {
        if refs.isEmpty && senders.isEmpty {
            shown[view] = nil
        } else {
            shown[view] = (refs, senders)
        }
        photos.show(view, refs: refs, senders: senders)
    }

    /// notify.contactsChanged.
    func changed(_ n: ContactsChangedNotification) {
        photos.changed(n)
    }

    /// The daemon restarted: everything is forgotten, every avatar draws
    /// its initials, and what the views showed is asked for again.
    func reset() {
        let again = shown
        images = [:]
        imageOrder = []
        undecodable = []
        photos.reset()
        for name in again.keys.sorted() {
            if let s = again[name] {
                photos.show(name, refs: s.refs, senders: s.senders)
            }
        }
    }

    /// The decoded photo of an avatar; nil draws the initials.
    func image(for a: ContactBook.Avatar?) -> NSImage? {
        guard let a, a.mayShowPhoto, let ph = photos.photo(for: a) else { return nil }
        let key = ph.key
        if let img = images[key] {
            imageOrder.removeAll { $0 == key }
            imageOrder.append(key)
            return img
        }
        if undecodable.contains(key) {
            return nil
        }
        // A photo the image decoders refuse is no photo (initials).
        guard ph.data.count <= API.Limits.maxContactPhotoBytes, let img = NSImage(data: ph.data), img.isValid,
              img.size.width > 0, img.size.height > 0 else {
            undecodable.insert(key)
            return nil
        }
        images[key] = img
        imageOrder.append(key)
        while imageOrder.count > Self.maxImages {
            images[imageOrder.removeFirst()] = nil
        }
        return img
    }

    private func broadcast(_ key: String) {
        if key.isEmpty {
            images = [:]
            imageOrder = []
            undecodable = []
        }
        for case let v as any ContactPhotoDisplay in views.allObjects where v.photoKeyMatches(key) {
            v.contactPhotoChanged(key)
        }
    }
}
