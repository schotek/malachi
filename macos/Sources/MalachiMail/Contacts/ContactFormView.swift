// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The contact form on the detail's card (W4): for a new contact the
/// address book first, then the initials beside the name fields (first and
/// last name, company, job title; prefix, middle name, suffix and nickname
/// only when the card has them), the e-mail addresses, phones, postal
/// addresses and websites as rows with a label menu and a remove button
/// and Add Email / Add Phone / … under each kind, the birthday, the note,
/// and at the bottom Delete Contact… · Cancel · Done. A problem of the
/// last Done stands in red under its field. The view only reports edits
/// (`onEdit`, a change of the controller's form) and structural ones
/// (`onRebuild`: a row added or removed, after which the view is built
/// again); the state is `ContactsDetailController`'s.
@MainActor
final class ContactFormView: NSView, NSTextFieldDelegate, NSTextViewDelegate {
    typealias Edit = (inout ContactBook.Form) -> Void

    var onEdit: ((@escaping Edit) -> Void)?
    var onRebuild: ((@escaping Edit, String) -> Void)?
    var onDone: (() -> Void)?
    var onCancel: (() -> Void)?
    var onDelete: (() -> Void)?

    private let form: ContactBook.Form
    private let problems: [ContactBook.Problem]
    private let scroll = NSScrollView()
    private let document = FlippedDocument()
    private let stack = NSStackView()
    private let doneButton = NSButton()
    private let cancelButton = NSButton()
    private let deleteButton = NSButton()
    private let errorLabel = NSTextField(wrappingLabelWithString: "")
    private let spinner = Spinner(size: 16)
    /// What each field writes, by its key.
    private var writers: [String: (String) -> Edit] = [:]
    private var fields: [String: NSView] = [:]
    private var noteView: NSTextView?

    static let inset: CGFloat = 24
    static let labelWidth: CGFloat = 110

    /// The key of a field: "given", "emails.2", "addresses.1.city".
    static func key(_ k: ContactBook.FieldKey, _ index: Int?, _ part: String = "") -> String {
        var s = "\(k)"
        if let index {
            s += ".\(index)"
        }
        if !part.isEmpty {
            s += ".\(part)"
        }
        return s
    }

    init(form f: ContactBook.Form, problems: [ContactBook.Problem], choices: [ContactBook.BookChoice]) {
        form = f
        self.problems = problems
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        build(choices)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: Building

    private func build(_ choices: [ContactBook.BookChoice]) {
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 8
        stack.detachesHiddenViews = true
        stack.edgeInsets = NSEdgeInsets(top: Self.inset, left: Self.inset, bottom: Self.inset, right: Self.inset)
        stack.translatesAutoresizingMaskIntoConstraints = false
        document.translatesAutoresizingMaskIntoConstraints = false
        document.addSubview(stack)
        scroll.documentView = document
        scroll.drawsBackground = false
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.translatesAutoresizingMaskIntoConstraints = false

        if form.isNew {
            addBookChoice(choices)
        }
        addNames()
        for k in ContactBook.FieldKind.allCases {
            addRows(k)
        }
        addBirthday()
        addNote()

        let bar = buildBar()
        addSubview(scroll)
        addSubview(bar)
        NSLayoutConstraint.activate([
            scroll.topAnchor.constraint(equalTo: topAnchor),
            scroll.leadingAnchor.constraint(equalTo: leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: trailingAnchor),
            scroll.bottomAnchor.constraint(equalTo: bar.topAnchor),
            bar.leadingAnchor.constraint(equalTo: leadingAnchor),
            bar.trailingAnchor.constraint(equalTo: trailingAnchor),
            bar.bottomAnchor.constraint(equalTo: bottomAnchor),
            document.topAnchor.constraint(equalTo: scroll.contentView.topAnchor),
            document.leadingAnchor.constraint(equalTo: scroll.contentView.leadingAnchor),
            document.widthAnchor.constraint(equalTo: scroll.contentView.widthAnchor),
            stack.topAnchor.constraint(equalTo: document.topAnchor),
            stack.leadingAnchor.constraint(equalTo: document.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: document.trailingAnchor),
            stack.bottomAnchor.constraint(equalTo: document.bottomAnchor),
        ])
    }

    /// A full-width arranged view.
    private func add(_ v: NSView, spacingAfter: CGFloat? = nil) {
        stack.addArrangedSubview(v)
        v.widthAnchor.constraint(equalTo: stack.widthAnchor, constant: -2 * Self.inset).isActive = true
        if let spacingAfter {
            stack.setCustomSpacing(spacingAfter, after: v)
        }
    }

    private func problemLabel(_ k: ContactBook.FieldKey, _ index: Int?) -> NSTextField? {
        let texts = problems.filter { $0.key == k && $0.index == index }.map(\.text)
        guard !texts.isEmpty else { return nil }
        let l = NSTextField(wrappingLabelWithString: texts.joined(separator: "\n"))
        l.font = Typo.caption
        l.textColor = .systemRed
        return l
    }

    private func textField(_ key: String, _ value: String, placeholder: String, write: @escaping (String) -> Edit) -> NSTextField {
        let f = NSTextField(string: value)
        f.placeholderString = placeholder
        f.font = Typo.body
        f.lineBreakMode = .byTruncatingTail
        f.usesSingleLineMode = true
        f.delegate = self
        f.identifier = NSUserInterfaceItemIdentifier(key)
        f.setAccessibilityLabel(placeholder)
        f.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        writers[key] = write
        fields[key] = f
        return f
    }

    private func addBookChoice(_ choices: [ContactBook.BookChoice]) {
        let label = NSTextField(labelWithString: ContactBook.bookLabel)
        label.font = Typo.body
        label.textColor = .secondaryLabelColor
        let pop = NSPopUpButton(frame: .zero, pullsDown: false)
        for c in choices {
            pop.addItem(withTitle: c.text)
            pop.lastItem?.representedObject = c.book.rawValue
        }
        if let i = choices.firstIndex(where: { $0.book == form.book }) {
            pop.selectItem(at: i)
        }
        pop.target = self
        pop.action = #selector(bookChosen(_:))
        pop.setAccessibilityLabel(ContactBook.bookLabel)
        pop.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        let row = NSStackView(views: [label, pop])
        row.orientation = .horizontal
        row.spacing = 8
        add(row, spacingAfter: 16)
    }

    @objc private func bookChosen(_ sender: NSPopUpButton) {
        guard let raw = sender.selectedItem?.representedObject as? String else { return }
        onEdit? { $0.book = AddressBookID(rawValue: raw) }
    }

    private func addNames() {
        let avatar = ContactInitialsView(size: 64)
        let title = ContactBook.composeName(ContactName(given: form.given, family: form.family))
        avatar.initials = ContactBook.initials(title.isEmpty ? form.org : title, address: form.emails.first?.value ?? "")
        let column = NSStackView()
        column.orientation = .vertical
        column.alignment = .leading
        column.spacing = 6
        typealias Part = (ContactBook.FieldKey, WritableKeyPath<ContactBook.Form, String>)
        let always: [Part] = [(.given, \.given), (.family, \.family), (.org, \.org), (.title, \.title)]
        let extra: [Part] = [(.prefix, \.prefix), (.middle, \.middle), (.suffix, \.suffix), (.nickname, \.nickname)]
        // The rarer name parts only when the card has them.
        let parts = always + extra.filter { !form[keyPath: $0.1].isEmpty }
        for (k, path) in parts {
            let f = textField(Self.key(k, nil), form[keyPath: path], placeholder: ContactBook.placeholder(k)) { v in { $0[keyPath: path] = v } }
            column.addArrangedSubview(f)
            f.widthAnchor.constraint(equalTo: column.widthAnchor).isActive = true
            if let p = problemLabel(k, nil) {
                column.addArrangedSubview(p)
            }
        }
        let row = NSStackView(views: [avatar, column])
        row.orientation = .horizontal
        row.alignment = .top
        row.spacing = 18
        add(row, spacingAfter: 18)
    }

    /// The rows of a kind with their label menus, then Add ….
    private func addRows(_ kind: ContactBook.FieldKind) {
        let key: ContactBook.FieldKey
        switch kind {
        case .email: key = .emails
        case .phone: key = .phones
        case .address: key = .addresses
        case .url: key = .urls
        }
        for i in 0..<form.count(kind) {
            let remove = NSButton(image: NSImage(systemSymbolName: "minus.circle.fill", accessibilityDescription: ContactBook.removeRowText) ?? NSImage(),
                                  target: self, action: #selector(removeRow(_:)))
            remove.isBordered = false
            remove.title = ""
            remove.imagePosition = .imageOnly
            remove.contentTintColor = .systemRed
            remove.toolTip = ContactBook.removeRowText
            remove.setAccessibilityLabel(ContactBook.removeRowText)
            remove.identifier = NSUserInterfaceItemIdentifier("\(kind.rawValue).\(i)")
            remove.setContentHuggingPriority(.required, for: .horizontal)
            let current = label(kind, i)
            let menu = NSPopUpButton(frame: .zero, pullsDown: false)
            menu.isBordered = false
            menu.font = Typo.body
            menu.contentTintColor = .controlAccentColor
            for l in ContactBook.labelChoices(kind, current: current) {
                menu.addItem(withTitle: ContactBook.labelText(kind, l))
                menu.lastItem?.representedObject = l.rawValue
            }
            menu.selectItem(at: ContactBook.labelChoices(kind, current: current).firstIndex(of: current) ?? 0)
            menu.identifier = NSUserInterfaceItemIdentifier("\(kind.rawValue).\(i)")
            menu.target = self
            menu.action = #selector(labelChosen(_:))
            menu.setAccessibilityLabel(ContactBook.labelText(kind, current))
            menu.widthAnchor.constraint(equalToConstant: Self.labelWidth).isActive = true
            let value: NSView
            if kind == .address {
                value = addressFields(i)
            } else {
                let path: WritableKeyPath<ContactBook.Form, [ContactBook.LabelledValue]> =
                    kind == .email ? \.emails : kind == .phone ? \.phones : \.urls
                value = textField(Self.key(key, i), form[keyPath: path][i].value, placeholder: ContactBook.placeholder(key)) { v in
                    { f in
                        if f[keyPath: path].indices.contains(i) {
                            f[keyPath: path][i].value = v
                        }
                    }
                }
            }
            let row = NSStackView(views: [remove, menu, value])
            row.orientation = .horizontal
            row.alignment = kind == .address ? .top : .centerY
            row.spacing = 6
            add(row)
            if let p = problemLabel(key, i) {
                add(indented(p))
            }
        }
        if let p = problemLabel(key, nil) {
            add(indented(p))
        }
        let addButton = NSButton(title: ContactBook.addRowText(kind), target: self, action: #selector(addRow(_:)))
        addButton.image = NSImage(systemSymbolName: "plus.circle.fill", accessibilityDescription: nil)
        addButton.imagePosition = .imageLeading
        addButton.isBordered = false
        addButton.contentTintColor = .systemGreen
        addButton.font = Typo.body
        addButton.identifier = NSUserInterfaceItemIdentifier("\(kind.rawValue)")
        addButton.isEnabled = form.canAdd(kind)
        let holder = NSStackView(views: [addButton])
        holder.orientation = .horizontal
        add(holder, spacingAfter: 14)
    }

    private func indented(_ v: NSView) -> NSView {
        let row = NSStackView(views: [v])
        row.edgeInsets = NSEdgeInsets(top: 0, left: Self.labelWidth + 28, bottom: 0, right: 0)
        row.orientation = .horizontal
        return row
    }

    private func label(_ kind: ContactBook.FieldKind, _ i: Int) -> ContactLabel {
        switch kind {
        case .email: form.emails[i].label
        case .phone: form.phones[i].label
        case .address: form.addresses[i].label
        case .url: form.urls[i].label
        }
    }

    /// A postal address: street; postal code and city; region and country.
    private func addressFields(_ i: Int) -> NSView {
        let a = form.addresses[i]
        func field(_ part: ContactBook.AddressPart, _ path: WritableKeyPath<ContactAddress, String>) -> NSTextField {
            textField(Self.key(.addresses, i, "\(part)"), a[keyPath: path], placeholder: ContactBook.addressPlaceholder(part)) { v in
                { f in
                    if f.addresses.indices.contains(i) {
                        f.addresses[i][keyPath: path] = v
                    }
                }
            }
        }
        let street = field(.street, \.street)
        let postcode = field(.postcode, \.postcode)
        postcode.widthAnchor.constraint(equalToConstant: 90).isActive = true
        let city = field(.city, \.city)
        let region = field(.region, \.region)
        let country = field(.country, \.country)
        let line2 = NSStackView(views: [postcode, city])
        line2.orientation = .horizontal
        line2.spacing = 6
        let line3 = NSStackView(views: [region, country])
        line3.orientation = .horizontal
        line3.spacing = 6
        line3.distribution = .fillEqually
        let column = NSStackView(views: [street, line2, line3])
        column.orientation = .vertical
        column.alignment = .leading
        column.spacing = 6
        for v in [street, line2, line3] {
            v.widthAnchor.constraint(equalTo: column.widthAnchor).isActive = true
        }
        column.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        return column
    }

    private func addBirthday() {
        guard let b = form.birthday else {
            let addButton = NSButton(title: ContactBook.addBirthday, target: self, action: #selector(addBirthday(_:)))
            addButton.image = NSImage(systemSymbolName: "plus.circle.fill", accessibilityDescription: nil)
            addButton.imagePosition = .imageLeading
            addButton.isBordered = false
            addButton.contentTintColor = .systemGreen
            addButton.font = Typo.body
            let holder = NSStackView(views: [addButton])
            holder.orientation = .horizontal
            add(holder, spacingAfter: 14)
            return
        }
        let remove = NSButton(image: NSImage(systemSymbolName: "minus.circle.fill", accessibilityDescription: ContactBook.removeRowText) ?? NSImage(),
                              target: self, action: #selector(removeBirthday(_:)))
        remove.isBordered = false
        remove.title = ""
        remove.imagePosition = .imageOnly
        remove.contentTintColor = .systemRed
        remove.toolTip = ContactBook.removeRowText
        remove.setAccessibilityLabel(ContactBook.removeRowText)
        let label = NSTextField(labelWithString: ContactBook.placeholder(.birthday))
        label.font = Typo.body
        label.textColor = .secondaryLabelColor
        label.alignment = .right
        label.widthAnchor.constraint(equalToConstant: Self.labelWidth).isActive = true
        let day = textField(Self.key(.birthday, nil, "day"), b.day > 0 ? String(b.day) : "", placeholder: "") { v in
            { f in f.birthday?.day = Int(v.trimmingCharacters(in: .whitespaces)) ?? 0 }
        }
        day.widthAnchor.constraint(equalToConstant: 44).isActive = true
        let month = NSPopUpButton(frame: .zero, pullsDown: false)
        let names = DateFormatter().standaloneMonthSymbols ?? []
        for (i, n) in names.enumerated() {
            month.addItem(withTitle: n)
            month.lastItem?.tag = i + 1
        }
        month.selectItem(withTag: b.month)
        month.target = self
        month.action = #selector(monthChosen(_:))
        let year = textField(Self.key(.birthday, nil, "year"), b.year > 0 ? String(b.year) : "", placeholder: "") { v in
            { f in f.birthday?.year = Int(v.trimmingCharacters(in: .whitespaces)) ?? 0 }
        }
        year.widthAnchor.constraint(equalToConstant: 64).isActive = true
        let row = NSStackView(views: [remove, label, day, month, year])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 6
        add(row)
        if let p = problemLabel(.birthday, nil) {
            add(indented(p))
        }
        stack.setCustomSpacing(14, after: stack.arrangedSubviews.last!)
    }

    private func addNote() {
        let label = NSTextField(labelWithString: ContactBook.placeholder(.note))
        label.font = Typo.body
        label.textColor = .secondaryLabelColor
        add(label, spacingAfter: 4)
        let scrollNote = NSTextView.scrollableTextView()
        scrollNote.borderType = .bezelBorder
        scrollNote.hasVerticalScroller = true
        scrollNote.autohidesScrollers = true
        if let tv = scrollNote.documentView as? NSTextView {
            tv.string = form.note
            tv.font = Typo.body
            tv.isRichText = false
            tv.importsGraphics = false
            tv.allowsUndo = true
            tv.isAutomaticQuoteSubstitutionEnabled = false
            tv.delegate = self
            tv.setAccessibilityLabel(ContactBook.placeholder(.note))
            noteView = tv
            fields[Self.key(.note, nil)] = tv
        }
        scrollNote.heightAnchor.constraint(equalToConstant: 72).isActive = true
        add(scrollNote)
        if let p = problemLabel(.note, nil) {
            add(p)
        }
    }

    private func buildBar() -> NSView {
        let bar = NSView()
        bar.translatesAutoresizingMaskIntoConstraints = false
        let line = NSBox()
        line.boxType = .separator
        line.translatesAutoresizingMaskIntoConstraints = false
        deleteButton.title = ContactBook.deleteContact
        deleteButton.bezelStyle = .push
        deleteButton.hasDestructiveAction = true
        deleteButton.contentTintColor = .systemRed
        deleteButton.target = self
        deleteButton.action = #selector(delete(_:))
        cancelButton.title = ContactBook.cancel
        cancelButton.bezelStyle = .push
        cancelButton.keyEquivalent = "\u{1b}"
        cancelButton.target = self
        cancelButton.action = #selector(cancel(_:))
        doneButton.title = ContactBook.done
        doneButton.bezelStyle = .push
        doneButton.keyEquivalent = "\r"
        doneButton.target = self
        doneButton.action = #selector(done(_:))
        errorLabel.font = Typo.caption
        errorLabel.textColor = .systemRed
        errorLabel.isHidden = true
        errorLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        spinner.isHidden = true
        let spacer = NSView()
        spacer.setContentHuggingPriority(.init(1), for: .horizontal)
        let row = NSStackView(views: [deleteButton, spacer, errorLabel, spinner, cancelButton, doneButton])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 8
        row.detachesHiddenViews = true
        row.translatesAutoresizingMaskIntoConstraints = false
        bar.addSubview(line)
        bar.addSubview(row)
        NSLayoutConstraint.activate([
            line.topAnchor.constraint(equalTo: bar.topAnchor),
            line.leadingAnchor.constraint(equalTo: bar.leadingAnchor),
            line.trailingAnchor.constraint(equalTo: bar.trailingAnchor),
            row.topAnchor.constraint(equalTo: bar.topAnchor, constant: 10),
            row.bottomAnchor.constraint(equalTo: bar.bottomAnchor, constant: -10),
            row.leadingAnchor.constraint(equalTo: bar.leadingAnchor, constant: Self.inset - 4),
            row.trailingAnchor.constraint(equalTo: bar.trailingAnchor, constant: -(Self.inset - 4)),
        ])
        return bar
    }

    // MARK: State

    /// Saving, deleting and the last failure's sentence.
    func setState(saving: Bool, deleting: Bool, canDelete: Bool, error: String) {
        let busy = saving || deleting
        doneButton.isEnabled = !busy
        cancelButton.isEnabled = !busy
        deleteButton.isHidden = !canDelete && !deleting
        deleteButton.isEnabled = canDelete && !busy
        spinner.isHidden = !busy
        errorLabel.stringValue = error
        errorLabel.isHidden = error.isEmpty
    }

    /// The key of the field with the keyboard (to give it back after a
    /// rebuild).
    var focusedKey: String? {
        guard let r = window?.firstResponder else { return nil }
        for (k, v) in fields {
            if v === r || (r as? NSTextView)?.delegate === v as? NSTextField || (r as? NSView)?.isDescendant(of: v) == true {
                return k
            }
        }
        return nil
    }

    /// Gives the keyboard to a field by its key.
    func focus(_ key: String?) {
        guard let key, let v = fields[key] else { return }
        DispatchQueue.main.async { [weak self, weak v] in
            guard let v, let w = self?.window else { return }
            w.makeFirstResponder(v)
            v.scrollToVisible(v.bounds)
        }
    }

    // MARK: Edits

    func controlTextDidChange(_ obj: Foundation.Notification) {
        guard let f = obj.object as? NSTextField, let key = f.identifier?.rawValue, let write = writers[key] else { return }
        onEdit?(write(f.stringValue))
    }

    func textDidChange(_ notification: Foundation.Notification) {
        guard let tv = notification.object as? NSTextView, tv === noteView else { return }
        let text = tv.string
        onEdit? { $0.note = text }
    }

    private static func kindAndIndex(_ v: NSView) -> (ContactBook.FieldKind, Int)? {
        let parts = (v.identifier?.rawValue ?? "").split(separator: ".")
        guard let k = parts.first.flatMap({ Int($0) }).flatMap(ContactBook.FieldKind.init(rawValue:)) else { return nil }
        return (k, parts.count > 1 ? Int(parts[1]) ?? -1 : -1)
    }

    private static func fieldKey(_ k: ContactBook.FieldKind) -> ContactBook.FieldKey {
        switch k {
        case .email: .emails
        case .phone: .phones
        case .address: .addresses
        case .url: .urls
        }
    }

    @objc private func addRow(_ sender: NSButton) {
        guard let (kind, _) = Self.kindAndIndex(sender) else { return }
        let index = form.count(kind)
        let focus = kind == .address ? Self.key(.addresses, index, "\(ContactBook.AddressPart.street)") : Self.key(Self.fieldKey(kind), index)
        onRebuild?({ _ = $0.addRow(kind) }, focus)
    }

    @objc private func removeRow(_ sender: NSButton) {
        guard let (kind, i) = Self.kindAndIndex(sender), i >= 0 else { return }
        onRebuild?({ $0.removeRow(kind, at: i) }, "")
    }

    @objc private func labelChosen(_ sender: NSPopUpButton) {
        guard let (kind, i) = Self.kindAndIndex(sender), i >= 0,
              let raw = sender.selectedItem?.representedObject as? String
        else { return }
        let l = ContactLabel(rawValue: raw)
        sender.setAccessibilityLabel(ContactBook.labelText(kind, l))
        onEdit? { f in
            switch kind {
            case .email: if f.emails.indices.contains(i) { f.emails[i].label = l }
            case .phone: if f.phones.indices.contains(i) { f.phones[i].label = l }
            case .address: if f.addresses.indices.contains(i) { f.addresses[i].label = l }
            case .url: if f.urls.indices.contains(i) { f.urls[i].label = l }
            }
        }
    }

    @objc private func addBirthday(_ sender: Any?) {
        onRebuild?({ $0.birthday = ContactBirthday(month: 1, day: 1) }, Self.key(.birthday, nil, "day"))
    }

    @objc private func removeBirthday(_ sender: Any?) {
        onRebuild?({ $0.birthday = nil }, "")
    }

    @objc private func monthChosen(_ sender: NSPopUpButton) {
        let m = sender.selectedTag()
        onEdit? { $0.birthday?.month = m }
    }

    @objc private func done(_ sender: Any?) {
        window?.makeFirstResponder(nil)
        onDone?()
    }

    @objc private func cancel(_ sender: Any?) {
        onCancel?()
    }

    @objc private func delete(_ sender: Any?) {
        onDelete?()
    }
}
