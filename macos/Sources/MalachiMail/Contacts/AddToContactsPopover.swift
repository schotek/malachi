// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// Add to Contacts… of an address chip (W4 ④, A12): a popover under the
/// chip with the name the sender wrote — selected, with the warning under
/// it, because the sender wrote it —, the address, the address book (the
/// message's account's first, `ContactBook.AddFromChip`), More Details…
/// (the Contacts window with the whole form) and Add. Nothing is saved
/// before Add; Add runs contact.create through a `ContactsDetailController`
/// of its own (validation, the server-first write and its errors), and the
/// popover closes once the server took the contact.
@MainActor
final class AddToContactsPopover: NSViewController, NSPopoverDelegate, NSTextFieldDelegate {
    private let contacts: AppContacts
    private var state: ContactBook.AddFromChip
    private let saver: ContactsDetailController
    private let popover = NSPopover()
    private let nameField = NSTextField()
    private let bookPopup = NSPopUpButton(frame: .zero, pullsDown: false)
    private let addButton = NSButton()
    private let errorLabel = NSTextField(wrappingLabelWithString: "")
    private let spinner = Spinner(size: 16)
    private var saving = false

    /// The open one (a second chip's replaces it).
    private static var open: AddToContactsPopover?

    static let width: CGFloat = 340

    static func show(address: Address, account: AccountID?, contacts: AppContacts, relativeTo view: NSView) {
        open?.popover.close()
        let p = AddToContactsPopover(address: address, account: account, contacts: contacts)
        open = p
        p.popover.show(relativeTo: view.bounds, of: view, preferredEdge: .maxY)
        p.focusName()
    }

    private init(address: Address, account: AccountID?, contacts: AppContacts) {
        self.contacts = contacts
        state = ContactBook.AddFromChip(address: address, account: account, books: contacts.books, accounts: contacts.accounts)
        saver = ContactsDetailController(options: contacts.options)
        super.init(nibName: nil, bundle: nil)
        popover.contentViewController = self
        popover.behavior = .transient
        popover.delegate = self
        saver.onChange = { [weak self] in self?.saverChanged() }
        saver.onCreated = { [weak self] _ in self?.created() }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        let title = NSTextField(labelWithString: ContactBook.addTitle)
        title.font = Typo.heading

        nameField.stringValue = state.name
        nameField.placeholderString = ContactBook.nameLabel
        nameField.font = Typo.body
        nameField.delegate = self
        nameField.setAccessibilityLabel(ContactBook.nameLabel)
        let warning = NSTextField(wrappingLabelWithString: state.warning)
        warning.font = Typo.caption
        warning.textColor = .secondaryLabelColor
        let warnIcon = NSImageView(image: NSImage(systemSymbolName: "info.circle", accessibilityDescription: nil) ?? NSImage())
        warnIcon.contentTintColor = .secondaryLabelColor
        warnIcon.setContentHuggingPriority(.required, for: .horizontal)
        let warnRow = NSStackView(views: [warnIcon, warning])
        warnRow.orientation = .horizontal
        warnRow.alignment = .firstBaseline
        warnRow.spacing = 4
        warnRow.isHidden = state.warning.isEmpty
        let nameColumn = NSStackView(views: [nameField, warnRow])
        nameColumn.orientation = .vertical
        nameColumn.alignment = .leading
        nameColumn.spacing = 4
        nameColumn.detachesHiddenViews = true
        nameField.widthAnchor.constraint(equalTo: nameColumn.widthAnchor).isActive = true

        let address = NSTextField(labelWithString: state.address)
        address.font = Typo.body
        address.isSelectable = true
        address.lineBreakMode = .byTruncatingMiddle

        for c in state.books {
            bookPopup.addItem(withTitle: c.text)
            bookPopup.lastItem?.representedObject = c.book.rawValue
        }
        if let i = state.books.firstIndex(where: { $0.book == state.book }) {
            bookPopup.selectItem(at: i)
        }
        bookPopup.target = self
        bookPopup.action = #selector(bookChosen(_:))
        bookPopup.setAccessibilityLabel(ContactBook.bookLabel)

        func label(_ s: String) -> NSTextField {
            let l = NSTextField(labelWithString: s)
            l.font = Typo.body
            l.textColor = .secondaryLabelColor
            l.alignment = .right
            return l
        }
        let grid = NSGridView(views: [
            [label(ContactBook.nameLabel), nameColumn],
            [label(ContactBook.emailLabel), address],
            [label(ContactBook.bookLabel), bookPopup],
        ])
        grid.rowSpacing = 10
        grid.columnSpacing = 10
        grid.rowAlignment = .firstBaseline
        grid.column(at: 0).xPlacement = .trailing
        grid.column(at: 1).xPlacement = .fill

        let more = NSButton(title: ContactBook.moreDetails, target: self, action: #selector(moreDetails(_:)))
        more.isBordered = false
        more.contentTintColor = .linkColor
        more.font = Typo.bodyMedium
        addButton.title = ContactBook.add
        addButton.bezelStyle = .push
        addButton.keyEquivalent = "\r"
        addButton.target = self
        addButton.action = #selector(add(_:))
        spinner.isHidden = true
        let spacer = NSView()
        spacer.setContentHuggingPriority(.init(1), for: .horizontal)
        let bar = NSStackView(views: [more, spacer, spinner, addButton])
        bar.orientation = .horizontal
        bar.alignment = .centerY
        bar.spacing = 8
        bar.detachesHiddenViews = true

        errorLabel.font = Typo.caption
        errorLabel.textColor = .systemRed
        errorLabel.isHidden = true

        let stack = NSStackView(views: [title, grid, errorLabel, bar])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 14
        stack.detachesHiddenViews = true
        stack.edgeInsets = NSEdgeInsets(top: 16, left: 16, bottom: 14, right: 16)
        for v in [grid, bar, errorLabel] as [NSView] {
            v.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -32).isActive = true
        }
        stack.widthAnchor.constraint(equalToConstant: Self.width).isActive = true
        view = stack
        refresh()
    }

    private func focusName() {
        DispatchQueue.main.async { [weak self] in
            guard let self, let w = self.view.window else { return }
            w.makeFirstResponder(self.nameField)
            if self.state.selectName {
                self.nameField.currentEditor()?.selectAll(nil)
            }
        }
    }

    private func refresh() {
        addButton.isEnabled = state.canAdd && !saving
        spinner.isHidden = !saving
    }

    func controlTextDidChange(_ obj: Foundation.Notification) {
        state.name = nameField.stringValue
        refresh()
    }

    @objc private func bookChosen(_ sender: NSPopUpButton) {
        guard let raw = sender.selectedItem?.representedObject as? String else { return }
        state.book = AddressBookID(rawValue: raw)
        refresh()
    }

    @objc private func add(_ sender: Any?) {
        guard state.canAdd, !saving, let book = state.book else { return }
        state.name = nameField.stringValue
        saving = true
        errorLabel.isHidden = true
        refresh()
        saver.beginNew(book: book, form: state.form)
        saver.save()
    }

    private func saverChanged() {
        guard saving, !saver.saving else { return }
        // Refused: validation or the server; the popover stays with the
        // sentence.
        let text = saver.problems.first?.text ?? saver.error
        if !text.isEmpty {
            saving = false
            errorLabel.stringValue = text
            errorLabel.isHidden = false
            refresh()
        }
    }

    private func created() {
        saving = false
        contacts.lookup.invalidate()
        contacts.refresh()
        popover.close()
        contacts.state.toasts.show(ContactBook.contactAdded)
    }

    @objc private func moreDetails(_ sender: Any?) {
        state.name = nameField.stringValue
        let form = state.form
        popover.close()
        contacts.newContact(form)
    }

    func popoverDidClose(_ notification: Foundation.Notification) {
        saver.stop()
        if Self.open === self {
            Self.open = nil
        }
    }
}
