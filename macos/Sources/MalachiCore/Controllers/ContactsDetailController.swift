// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The contact shown in the detail and edited on its card (W1, W4), a port
// of ui/internal/contactsctl contact.go (`ContactController`): the card from
// contact.get, the form (`ContactBook.Form`) while editing, Done as
// contact.create or contact.update with the card's revision, Delete
// Contact… with its question and contact.delete. Writes go to the server
// (online only): a failure keeps the form open with the sentence (offline,
// read only, a refused card), a contactConflict asks
// `ContactBook.conflictQuestion` and Reload rebases the form on the
// server's version keeping what the user changed; nothing is overwritten
// blindly. An unchanged form's Done sends nothing.

import Foundation

@MainActor
public final class ContactsDetailController {
    /// contactsctl.BookResolver: a book and its account
    /// (`ContactsListController.book`).
    public typealias BookResolver = @MainActor (AddressBookID) -> (book: AddressBook, account: Account?)?

    public let options: ContactsOptions
    /// Resolves the card's book for the source line, the question's
    /// account and New Contact's book; nil knows none.
    public var books: BookResolver?

    // MARK: Outputs

    /// The card, the form or a state changed.
    public var onChange: (@MainActor () -> Void)?
    /// Asks; true is the question's confirm button, false (or nil) Cancel.
    public var onQuestion: (@MainActor @Sendable (ContactBook.Question) async -> Bool)?
    /// Done created the contact (the window selects it and reloads).
    public var onCreated: (@MainActor (ContactID) -> Void)?
    /// The contact was deleted (or was gone already).
    public var onDeleted: (@MainActor (ContactID) -> Void)?

    // MARK: State

    /// The shown contact (nil none or a new one).
    public private(set) var id: ContactID?
    /// The shown card (nil before contact.get answers).
    public private(set) var card: ContactCard?
    /// The form while editing (nil when not editing); the view changes it
    /// through `updateForm`.
    public private(set) var form: ContactBook.Form?
    /// The form's problems of the last Done.
    public private(set) var problems: [ContactBook.Problem] = []
    /// A contact.get without its answer.
    public private(set) var loading = false
    /// Done without its answer.
    public private(set) var saving = false
    /// Delete without its answer.
    public private(set) var deleting = false
    /// The last failure's sentence ("" none).
    public private(set) var error = ""

    private var gen = 0
    /// Counts the forms opened, so that a late answer meets only the form
    /// it was asked for (Go compares the form's pointer).
    private var formGen = 0
    private var closed = false

    public init(options: ContactsOptions, books: BookResolver? = nil) {
        self.options = options
        self.books = books
    }

    /// Ends the controller: no answer is taken any more.
    public func stop() { closed = true }

    private func changed() {
        if !closed {
            onChange?()
        }
    }

    // MARK: Derived

    /// Editing (the form is open).
    public var editing: Bool { form != nil }

    /// The detail of the shown card.
    public var detail: ContactBook.Detail? {
        card.map { ContactBook.detail($0, format: options.format) }
    }

    /// The card's (or the new contact's) book and account label.
    private func account(_ book: AddressBookID?) -> (book: AddressBook?, label: String) {
        guard let book, let r = books?(book) else { return (nil, "") }
        return (r.book, ContactBook.accountLabel(r.account))
    }

    /// The detail's footer ("Address book Contacts · Microsoft 365 (…)");
    /// Recently Used says On This Computer.
    public var sourceLine: String {
        guard let card else { return "" }
        let (b, label) = account(card.bookId)
        guard let b else { return "" }
        let acc = b.id == .recent || label.isEmpty ? ContactBook.onThisComputer : label
        return ContactBook.sourceLine(book: ContactBook.bookName(b), account: acc)
    }

    /// Copy's text.
    public var copyText: String {
        card.map { ContactBook.copyText($0, format: options.format) } ?? ""
    }

    /// Edit: a card of a writable book.
    public var canEdit: Bool { (card.map { !$0.readOnly } ?? false) && form == nil && !deleting }

    /// Delete Contact…: a card of a writable book.
    public var canDelete: Bool { (card.map { !$0.readOnly } ?? false) && !saving && !deleting }

    /// A form with a change (closing or selecting another contact asks
    /// first).
    public var dirty: Bool { form?.dirty ?? false }

    // MARK: Showing

    /// Shows a contact (nil clears the detail); a form being edited is
    /// dropped (the window asks before it selects another contact).
    public func open(_ id: ContactID?) {
        gen += 1
        formGen += 1
        self.id = id
        card = nil
        form = nil
        problems = []
        error = ""
        saving = false
        deleting = false
        guard id != nil else {
            loading = false
            changed()
            return
        }
        load(quiet: false)
        changed()
    }

    /// Asks contact.get; `quiet` keeps the shown card while it loads.
    private func load(quiet: Bool) {
        guard let id else { return }
        let gen = gen
        loading = !quiet
        options.call(API.ContactGet.self, ContactGetParams(contactId: id)) { [weak self] r in
            guard let self, !self.closed, gen == self.gen else { return }
            self.loading = false
            switch r {
            case .success(let res):
                self.card = res.contact
            case .failure(let e):
                if ContactsOptions.asRPCError(e).code == .contactNotFound {
                    self.card = nil
                }
                self.error = self.options.errorMessage(e)
            }
            self.changed()
        }
    }

    /// contactsctl.ContactController.ContactsChanged: the shown card is
    /// asked again when its book (or any of its account) changed and no
    /// form is open (a form meets the change as a conflict on Done).
    public func contactsChanged(_ n: ContactsChangedNotification) {
        guard let card, form == nil, !saving, !deleting, id != nil else { return }
        let r = books?(card.bookId)
        let changedAccount = n.accountId.flatMap { $0.rawValue.isEmpty ? nil : $0 }
        if let a = r?.account, let changedAccount, changedAccount != a.id {
            return
        }
        if r?.account == nil, let changedAccount, r?.book.accountId != changedAccount {
            return
        }
        if !n.bookIds.isEmpty && !n.bookIds.contains(card.bookId) {
            return
        }
        load(quiet: true)
    }

    // MARK: Editing

    /// Opens the form over the shown card.
    public func beginEdit() {
        guard canEdit, let card else { return }
        formGen += 1
        form = ContactBook.Form.edit(card)
        problems = []
        error = ""
        changed()
    }

    /// Opens the form of a new contact in `book` (the detail shows no card
    /// meanwhile); `form`, when given, is prefilled (Add to Contacts' More
    /// Details…).
    public func beginNew(book: AddressBookID?, form prefilled: ContactBook.Form? = nil) {
        gen += 1
        formGen += 1
        id = nil
        card = nil
        loading = false
        saving = false
        deleting = false
        form = prefilled ?? ContactBook.Form.new(book: book)
        problems = []
        error = ""
        changed()
    }

    /// Changes the open form (the view's fields); nothing without a form.
    public func updateForm(_ edit: (inout ContactBook.Form) -> Void) {
        guard var f = form else { return }
        edit(&f)
        form = f
    }

    /// Closes the form without saving (Cancel).
    public func cancelEdit() {
        guard form != nil, !saving else { return }
        formGen += 1
        form = nil
        problems = []
        error = ""
        changed()
    }

    /// Done: the form validated, then created or updated on the server.
    public func save() {
        guard var f = form, !saving else { return }
        // A Microsoft book's limits are checked before anything is sent.
        if let b = account(f.book).book {
            f.kind = b.kind
            form = f
        }
        problems = f.validate()
        if !problems.isEmpty {
            changed()
            return
        }
        if !f.isNew && !f.dirty {
            formGen += 1
            form = nil
            error = ""
            changed()
            return
        }
        saving = true
        error = ""
        let gen = gen
        let token = formGen
        let acc = account(f.book).label
        if f.isNew {
            let p = ContactCreateParams(bookId: f.book ?? "", card: f.card)
            options.call(API.ContactCreate.self, p, timeout: ContactsOptions.writeTimeout) { [weak self] r in
                guard let self, !self.closed, gen == self.gen else { return }
                self.saving = false
                switch r {
                case .failure(let e):
                    self.refused(e, account: acc)
                    self.changed()
                case .success(let res):
                    var cur = self.formGen == token ? self.form ?? f : f
                    cur.saved(id: res.contactId, revision: res.revision)
                    self.formGen += 1
                    self.id = res.contactId
                    self.card = cur.original
                    self.form = nil
                    self.load(quiet: true)
                    self.changed()
                    self.onCreated?(res.contactId)
                }
            }
            changed()
            return
        }
        let p = ContactUpdateParams(contactId: f.original.id, revision: f.original.revision, card: f.card)
        options.call(API.ContactUpdate.self, p, timeout: ContactsOptions.writeTimeout) { [weak self] r in
            guard let self, !self.closed, gen == self.gen else { return }
            self.saving = false
            switch r {
            case .failure(let e):
                self.saveFailed(e, account: acc, original: f.original.id, token: token)
            case .success(let res):
                var cur = self.formGen == token ? self.form ?? f : f
                cur.saved(id: nil, revision: res.revision)
                self.formGen += 1
                self.card = cur.original
                self.form = nil
                self.load(quiet: true)
                self.changed()
            }
        }
        changed()
    }

    /// contactsctl.refused: a card the daemon refused for one field marks
    /// that field (`ContactBook.problemOf`), anything else is the sentence.
    private func refused(_ e: any Error, account acc: String) {
        if let p = ContactBook.problemOf(e) {
            error = ""
            problems = [p]
            return
        }
        error = options.errorMessage(e, account: acc)
    }

    /// Keeps the form: a conflict asks for Reload, anything else shows its
    /// sentence.
    private func saveFailed(_ e: any Error, account acc: String, original: ContactID, token: Int) {
        refused(e, account: acc)
        guard ContactsOptions.asRPCError(e).code == .contactConflict else {
            changed()
            return
        }
        changed()
        if let server = ContactsOptions.conflictCard(e) {
            askReload(server, token: token)
            return
        }
        // A conflict without the server's version: ask contact.get for it.
        let gen = gen
        options.call(API.ContactGet.self, ContactGetParams(contactId: original)) { [weak self] r in
            guard let self, !self.closed, gen == self.gen, case .success(let res) = r else { return }
            self.askReload(res.contact, token: token)
        }
    }

    /// ConflictQuestion; Reload rebases the form on `server`.
    private func askReload(_ server: ContactCard, token: Int) {
        guard let ask = onQuestion else { return }
        let gen = gen
        let q = ContactBook.conflictQuestion
        options.spawn { [weak self] in
            let reload = await ask(q)
            guard let self, reload, !self.closed, gen == self.gen, token == self.formGen, var f = self.form else { return }
            f.rebase(server)
            self.form = f
            self.card = server
            self.error = ""
            self.changed()
        }
    }

    // MARK: Deleting

    /// Asks `ContactBook.deleteQuestion` and then deletes the contact on
    /// the server.
    public func delete() {
        guard canDelete, let ask = onQuestion, let card else { return }
        let acc = account(card.bookId).label
        let gen = gen
        let q = ContactBook.deleteQuestion(name: ContactBook.title(card), account: acc)
        options.spawn { [weak self] in
            let yes = await ask(q)
            guard let self, yes, !self.closed, gen == self.gen, !self.deleting else { return }
            self.deleting = true
            self.error = ""
            self.changed()
            let p = ContactDeleteParams(contactId: card.id, revision: card.revision)
            self.options.call(API.ContactDelete.self, p, timeout: ContactsOptions.writeTimeout) { [weak self] r in
                guard let self, !self.closed, gen == self.gen else { return }
                self.deleting = false
                let code = ContactsOptions.code(r)
                if code == nil || code == .contactNotFound {
                    self.gen += 1
                    self.formGen += 1
                    self.id = nil
                    self.card = nil
                    self.form = nil
                    self.changed()
                    self.onDeleted?(card.id)
                    return
                }
                guard case .failure(let e) = r else { return }
                if code == .contactConflict {
                    // Somebody changed it meanwhile: show the new version,
                    // the user decides again.
                    if let s = ContactsOptions.conflictCard(e) {
                        self.card = s
                    } else {
                        self.load(quiet: true)
                    }
                }
                self.error = self.options.errorMessage(e, account: acc)
                self.changed()
            }
        }
    }
}
