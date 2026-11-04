// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Contacts window's right column (W1, W4, W5): the selected contact's
/// card — initials, name, "Job · Company", Write Email / Show Mail / Copy,
/// the rows of label and value, the source line with Edit or Read Only —
/// or its form while editing (`ContactFormView`), or an empty state (No
/// Contact Selected; No Address Books with Open Preferences…). In the
/// narrow window it covers the list and starts with ‹ Contacts. Everything
/// a contact holds is plain text through `stringValue`; a website opens
/// only through "Open This Link?", a phone number can only be selected and
/// copied (A13).
@MainActor
final class ContactsDetailViewController: NSViewController {
    weak var window: ContactsWindowController?

    private let back = NSButton()
    private let backBar = NSView()
    private let host = NSView()
    private let empty = StatusPageView(illustration: .none, title: "", description: "")
    private let cardScroll = NSScrollView()
    private var card: ContactCardView?
    private var shownCard: ContactCardView.Model?
    private var form: ContactFormView?
    /// The controller's form as this view last wrote or built it: another
    /// value means the form changed under it (opened, reloaded, rebased).
    private var mirror: ContactBook.Form?
    private var shownProblems: [ContactBook.Problem] = []
    private var shownChoices: [ContactBook.BookChoice] = []

    override func loadView() {
        let root = NSView()
        back.title = ContactBook.windowTitle
        back.image = NSImage(systemSymbolName: "chevron.left", accessibilityDescription: nil)
        back.imagePosition = .imageLeading
        back.isBordered = false
        back.contentTintColor = .controlAccentColor
        back.font = Typo.bodyMedium
        back.target = self
        back.action = #selector(goBack(_:))
        back.translatesAutoresizingMaskIntoConstraints = false
        backBar.addSubview(back)
        let backLine = NSBox()
        backLine.boxType = .separator
        backLine.translatesAutoresizingMaskIntoConstraints = false
        backBar.addSubview(backLine)
        backBar.isHidden = true
        cardScroll.drawsBackground = false
        cardScroll.hasVerticalScroller = true
        cardScroll.autohidesScrollers = true
        let stack = FillStackView()
        stack.spacing = 0
        stack.addArrangedSubview(backBar)
        stack.addArrangedSubview(host)
        stack.translatesAutoresizingMaskIntoConstraints = false
        root.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: root.safeAreaLayoutGuide.topAnchor),
            stack.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            stack.bottomAnchor.constraint(equalTo: root.bottomAnchor),
            backBar.heightAnchor.constraint(equalToConstant: 36),
            back.leadingAnchor.constraint(equalTo: backBar.leadingAnchor, constant: 12),
            back.centerYAnchor.constraint(equalTo: backBar.centerYAnchor),
            backLine.leadingAnchor.constraint(equalTo: backBar.leadingAnchor),
            backLine.trailingAnchor.constraint(equalTo: backBar.trailingAnchor),
            backLine.bottomAnchor.constraint(equalTo: backBar.bottomAnchor),
        ])
        view = root
        show(empty)
    }

    /// ‹ Contacts above the column (the narrow window's detail).
    func setBackButton(_ on: Bool) {
        _ = view
        backBar.isHidden = !on
    }

    @objc private func goBack(_ sender: Any?) {
        window?.backToList()
    }

    private func show(_ v: NSView) {
        guard v.superview !== host else { return }
        for s in host.subviews {
            s.removeFromSuperview()
        }
        v.translatesAutoresizingMaskIntoConstraints = false
        host.addSubview(v)
        NSLayoutConstraint.activate([
            v.topAnchor.constraint(equalTo: host.topAnchor),
            v.leadingAnchor.constraint(equalTo: host.leadingAnchor),
            v.trailingAnchor.constraint(equalTo: host.trailingAnchor),
            v.bottomAnchor.constraint(equalTo: host.bottomAnchor),
        ])
    }

    // MARK: Photo

    /// The photos' name of the card (`ContactsPhotosController.show`).
    static let photoView = "contacts-detail"
    private var shownPhoto: ContactsPhotosController.ContactRef?

    /// Tells the photos which contact the card shows (nil: none).
    private func reportPhoto(_ c: ContactCard?) {
        let ref = c.map { ContactsPhotosController.ContactRef(id: $0.id, book: $0.bookId, state: $0.photo) }
        guard ref != shownPhoto else { return }
        shownPhoto = ref
        AppContacts.shared?.photos.show(Self.photoView, refs: ref.map { [$0] } ?? [])
    }

    override func viewDidDisappear() {
        super.viewDidDisappear()
        shownPhoto = nil
        AppContacts.shared?.photos.show(Self.photoView)
    }

    // MARK: Update

    func update() {
        guard let w = window else { return }
        _ = view
        let d = w.detail
        if let f = d.form {
            updateForm(f, d, w)
            return
        }
        form = nil
        mirror = nil
        shownProblems = []
        if let detail = d.detail, let c = d.card {
            let m = ContactCardView.Model(
                detail: detail, source: d.sourceLine, canEdit: d.canEdit, readOnly: c.readOnly, error: d.error,
                deleting: d.deleting, avatar: ContactBook.avatarOfDetail(c, detail, photo: c.photo))
            reportPhoto(c)
            if m != shownCard || card == nil {
                shownCard = m
                let v = ContactCardView(model: m)
                v.onAction = { [weak self] a in self?.perform(a) }
                card = v
                cardScroll.documentView = v.scrollDocument
                v.scrollDocument.widthAnchor.constraint(equalTo: cardScroll.contentView.widthAnchor).isActive = true
            }
            show(cardScroll)
            return
        }
        shownCard = nil
        card = nil
        reportPhoto(nil)
        if d.loading {
            empty.illustration = .spinner
            empty.title = ""
            empty.descriptionText = ""
            empty.setChild(nil)
            show(empty)
            return
        }
        let e = w.list.detailEmpty
        let (title, body) = ContactBook.emptyText(e == .none && !d.error.isEmpty ? .selection : e)
        empty.illustration = e == .noBooks ? .symbol("book.closed") : .symbol("person.crop.circle")
        empty.title = e == .none && d.error.isEmpty ? "" : title
        empty.descriptionText = d.error.isEmpty ? body : d.error
        if e == .noBooks {
            let open = NSButton(title: ContactBook.openPreferences, target: self, action: #selector(openPreferences(_:)))
            open.bezelStyle = .push
            open.keyEquivalent = "\r"
            empty.setChild(open)
        } else {
            empty.setChild(nil)
        }
        if e == .none && d.error.isEmpty {
            empty.illustration = .none
        }
        show(empty)
    }

    @objc private func openPreferences(_ sender: Any?) {
        window?.contacts.openPreferences()
    }

    private func updateForm(_ f: ContactBook.Form, _ d: ContactsDetailController, _ w: ContactsWindowController) {
        shownCard = nil
        card = nil
        let choices = f.isNew ? ContactBook.bookChoices(w.list.books, accounts: w.list.accounts) : []
        if form == nil || f != mirror || d.problems != shownProblems || choices != shownChoices {
            let focus = form?.focusedKey
            let v = ContactFormView(form: f, problems: d.problems, choices: choices)
            v.onEdit = { [weak self] edit in
                guard let self, let d = self.window?.detail else { return }
                d.updateForm(edit)
                self.mirror = d.form
            }
            v.onRebuild = { [weak self] edit, focus in
                guard let self, let d = self.window?.detail else { return }
                d.updateForm(edit)
                self.mirror = nil
                self.update()
                self.form?.focus(focus)
            }
            v.onDone = { [weak self] in self?.window?.detail.save() }
            v.onCancel = { [weak self] in self?.window?.detail.cancelEdit() }
            v.onDelete = { [weak self] in self?.window?.detail.delete() }
            form = v
            mirror = f
            shownProblems = d.problems
            shownChoices = choices
            show(v)
            if let first = d.problems.first {
                v.focus(ContactFormView.key(first.key, first.index))
            } else if let focus {
                v.focus(focus)
            }
        }
        form?.setState(saving: d.saving, deleting: d.deleting, canDelete: !f.isNew && d.canDelete, error: d.error)
    }

    /// The form's first field takes the keyboard (New Contact).
    func focusFirstField() {
        update()
        form?.focus(ContactFormView.key(.given, nil))
    }

    // MARK: Actions

    private func perform(_ a: ContactCardView.Action) {
        guard let w = window, let card = w.detail.card else { return }
        switch a {
        case .write(let address):
            let title = w.detail.detail?.title ?? ""
            let name = title == ContactBook.noName ? "" : title
            w.contacts.writeEmail(to: address, name: name, account: w.list.book(card.bookId)?.account?.id)
        case .showMail(let query):
            w.contacts.showMail?(query)
        case .copy:
            let text = w.detail.copyText
            guard !text.isEmpty else { return }
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(text, forType: .string)
            w.toast(ContactBook.copied)
        case .edit:
            w.detail.beginEdit()
            form?.focus(ContactFormView.key(.given, nil))
        case .open(let url):
            openWebsite(url)
        }
    }

    /// A website of the card: "Open This Link?" with its destination first.
    private func openWebsite(_ href: String) {
        guard let url = URL(string: href), url.scheme == "https" || url.scheme == "http" else { return }
        let alert = NSAlert()
        alert.alertStyle = .warning
        alert.messageText = L10n.T("Open This Link?")
        // TRANSLATORS: %s is the link's real destination.
        alert.informativeText = L10n.T("This link leads to %s.", href)
        let cancel = alert.addButton(withTitle: mn(L10n.T("_Cancel")))
        cancel.keyEquivalent = "\u{1b}"
        let open = alert.addButton(withTitle: mn(L10n.T("_Open Link")))
        open.keyEquivalent = ""
        guard let window = view.window else { return }
        alert.beginSheetModal(for: window) { response in
            if response == .alertSecondButtonReturn {
                NSWorkspace.shared.open(url)
            }
        }
    }
}

// MARK: - The card

/// A contact's card (W1): heading, actions, rows, source line and Edit.
@MainActor
final class ContactCardView: NSView {
    struct Model: Equatable {
        var detail: ContactBook.Detail
        var source: String
        var canEdit: Bool
        var readOnly: Bool
        var error: String
        var deleting: Bool
        /// The heading's avatar: initials, or the contact's photo (F9).
        var avatar: ContactBook.Avatar
    }

    enum Action {
        case write(String)
        case showMail(String)
        case copy
        case edit
        case open(String)
    }

    var onAction: ((Action) -> Void)?
    /// The scroll view's document (top-down).
    let scrollDocument = FlippedDocument()
    private let model: Model
    private var emails: [String] = []
    private var urls: [String] = []

    static let avatarSize: CGFloat = 72
    static let inset: CGFloat = 28

    init(model m: Model) {
        model = m
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        scrollDocument.translatesAutoresizingMaskIntoConstraints = false
        scrollDocument.addSubview(self)
        NSLayoutConstraint.activate([
            topAnchor.constraint(equalTo: scrollDocument.topAnchor),
            leadingAnchor.constraint(equalTo: scrollDocument.leadingAnchor),
            trailingAnchor.constraint(equalTo: scrollDocument.trailingAnchor),
            bottomAnchor.constraint(equalTo: scrollDocument.bottomAnchor),
        ])
        let d = m.detail

        let avatar = ContactInitialsView(size: Self.avatarSize)
        avatar.initials = d.initials
        avatar.photoSource = m.avatar
        let name = NSTextField(wrappingLabelWithString: d.title)
        name.font = Typo.title1
        name.isSelectable = true
        let subtitle = NSTextField(wrappingLabelWithString: d.subtitle)
        subtitle.font = Typo.body
        subtitle.textColor = .secondaryLabelColor
        subtitle.isSelectable = true
        subtitle.isHidden = d.subtitle.isEmpty
        let names = NSStackView(views: [name, subtitle])
        names.orientation = .vertical
        names.alignment = .leading
        names.spacing = 4
        names.detachesHiddenViews = true
        names.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        let heading = NSStackView(views: [avatar, names])
        heading.orientation = .horizontal
        heading.alignment = .centerY
        heading.spacing = 18

        // Write Email (a menu of the addresses when there are several),
        // Show Mail, Copy.
        let write: NSButton
        if d.actions.emails.count > 1 {
            let pop = NSPopUpButton(frame: .zero, pullsDown: true)
            pop.addItem(withTitle: ContactBook.writeEmail)
            for r in d.actions.emails {
                let item = NSMenuItem(title: ContactBook.twoParts(r.label, r.address), action: #selector(writeChosen(_:)), keyEquivalent: "")
                item.target = self
                item.representedObject = r.address
                pop.menu?.addItem(item)
            }
            write = pop
        } else {
            write = NSButton(title: ContactBook.writeEmail, target: self, action: #selector(writeFirst(_:)))
        }
        emails = d.rows.filter { $0.kind == .email }.map(\.address)
        write.bezelStyle = .push
        write.isEnabled = d.actions.writeEmail
        let showMail = NSButton(title: ContactBook.showMail, target: self, action: #selector(showMail(_:)))
        showMail.bezelStyle = .push
        showMail.isEnabled = !d.actions.showMail.isEmpty
        let copy = NSButton(title: ContactBook.copy, target: self, action: #selector(copy(_:)))
        copy.bezelStyle = .push
        let actions = NSStackView(views: [write, showMail, copy])
        actions.orientation = .horizontal
        actions.spacing = 8

        // The rows: label right-aligned, value as selectable text.
        var gridRows: [[NSView]] = []
        for r in d.rows {
            let label = NSTextField(labelWithString: r.label)
            label.font = Typo.body
            label.textColor = .secondaryLabelColor
            label.alignment = .right
            let value: NSView
            if r.kind == .url, !r.url.isEmpty {
                let link = NSButton(title: r.value, target: self, action: #selector(openURL(_:)))
                link.isBordered = false
                link.contentTintColor = .linkColor
                link.font = Typo.body
                link.alignment = .left
                link.tag = urls.count
                link.lineBreakMode = .byTruncatingMiddle
                link.toolTip = r.url
                link.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
                urls.append(r.url)
                value = link
            } else {
                let v = NSTextField(wrappingLabelWithString: r.value)
                v.font = Typo.body
                v.isSelectable = true
                v.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
                value = v
            }
            gridRows.append([label, value])
        }
        let grid = NSGridView(views: gridRows)
        grid.rowSpacing = 10
        grid.columnSpacing = 14
        grid.rowAlignment = .firstBaseline
        if grid.numberOfColumns == 2 {
            grid.column(at: 0).xPlacement = .trailing
            grid.column(at: 1).xPlacement = .leading
            // The labels' column as wide as its widest label (W1); the
            // values take the rest.
            grid.column(at: 0).width = gridRows.map { $0[0].intrinsicContentSize.width }.max() ?? 0
        }
        grid.isHidden = d.rows.isEmpty

        // The source line and Edit (or Read Only).
        let bookIcon = NSImageView(image: NSImage(systemSymbolName: "book.closed", accessibilityDescription: nil) ?? NSImage())
        bookIcon.contentTintColor = .secondaryLabelColor
        bookIcon.setContentHuggingPriority(.required, for: .horizontal)
        let source = NSTextField(wrappingLabelWithString: m.source)
        source.font = Typo.caption
        source.textColor = .secondaryLabelColor
        source.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        let sourceRow = NSStackView(views: [bookIcon, source])
        sourceRow.orientation = .horizontal
        sourceRow.alignment = .firstBaseline
        sourceRow.spacing = 6
        sourceRow.isHidden = m.source.isEmpty
        let edit: NSView
        if m.readOnly {
            let pill = PillLabel()
            pill.font = Typo.chip
            pill.textColor = .secondaryLabelColor
            pill.stringValue = ContactBook.readOnly
            edit = pill
        } else {
            let b = NSButton(title: ContactBook.edit, target: self, action: #selector(edit(_:)))
            b.bezelStyle = .push
            b.isEnabled = m.canEdit
            edit = b
        }
        let error = NSTextField(wrappingLabelWithString: m.error)
        error.font = Typo.caption
        error.textColor = .systemRed
        error.isHidden = m.error.isEmpty

        let stack = NSStackView(views: [heading, actions, separator(), grid, separator(), sourceRow, edit, error])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 16
        stack.detachesHiddenViews = true
        stack.setCustomSpacing(22, after: heading)
        stack.setCustomSpacing(10, after: sourceRow)
        stack.edgeInsets = NSEdgeInsets(top: Self.inset, left: Self.inset, bottom: Self.inset, right: Self.inset)
        stack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: topAnchor),
            stack.leadingAnchor.constraint(equalTo: leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: trailingAnchor),
            stack.bottomAnchor.constraint(equalTo: bottomAnchor),
        ])
        for v in stack.arrangedSubviews where v is NSBox || v === heading || v === grid {
            v.widthAnchor.constraint(lessThanOrEqualTo: stack.widthAnchor, constant: -2 * Self.inset).isActive = true
        }
        for v in stack.arrangedSubviews where v is NSBox {
            v.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -2 * Self.inset).isActive = true
        }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private func separator() -> NSBox {
        let b = NSBox()
        b.boxType = .separator
        return b
    }

    @objc private func writeFirst(_ sender: Any?) {
        if let a = emails.first {
            onAction?(.write(a))
        }
    }

    @objc private func writeChosen(_ sender: NSMenuItem) {
        if let a = sender.representedObject as? String {
            onAction?(.write(a))
        }
    }

    @objc private func showMail(_ sender: Any?) {
        onAction?(.showMail(model.detail.actions.showMail))
    }

    @objc private func copy(_ sender: Any?) {
        onAction?(.copy)
    }

    @objc private func edit(_ sender: Any?) {
        onAction?(.edit)
    }

    @objc private func openURL(_ sender: NSButton) {
        guard urls.indices.contains(sender.tag) else { return }
        onAction?(.open(urls[sender.tag]))
    }
}

/// A scroll view's document laid out from the top.
@MainActor
final class FlippedDocument: NSView {
    override var isFlipped: Bool { true }
}
