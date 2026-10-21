// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// Settings → AI → About You (docs/plans/assistant-profile.md §5, the
/// texts `Assistant.profileTexts()`): what the user tells the assistant
/// about themselves, the grammatical gender they write in, a note for each
/// mail and Jira account, whether MCP apps get the profile, and the notes
/// the assistant suggested. Everything goes through the application's
/// `ProfileStore`: the gender and the switch are written at once, the text
/// and the notes after a second of quiet and when the page goes, each only
/// when it changed. A store change refreshes what the user is not editing.
/// The controls wait until the store loaded; with a daemon that has no
/// profile the groups stay hidden. Every profile or proposal text is
/// plain text (`stringValue`, an NSTextView without rich text).
@MainActor
final class AboutYouSection: NSObject, NSTextViewDelegate, NSTextFieldDelegate {
    /// About You, and the assistant's suggestions under it (only with
    /// some).
    let group: PreferencesGroupView
    let proposalsGroup: PreferencesGroupView

    private let store: ProfileStore
    private let client: RPCClient?
    private let texts = Assistant.profileTexts()
    private let gender = NSPopUpButton(frame: .zero, pullsDown: false)
    private let share = NSSwitch()
    private let textRow: ProfileTextRow
    private var noteRows: [ProfileNoteRow] = []
    private var accounts: [Account] = []
    private var proposalRows: [String] = []
    private var genderRow: PreferenceRowView?
    private var shareRow: PreferenceRowView?
    private var token: BoardObserverToken?
    private var textTimer: Timer?
    private var noteTimers: [AccountID: Timer] = [:]
    /// The text or a note was typed since it was last written.
    private var textDirty = false
    private var dirtyNotes: Set<AccountID> = []
    private var accountsGeneration = 0
    private weak var window: NSWindow?

    init(store: ProfileStore, client: RPCClient?) {
        self.store = store
        self.client = client
        group = PreferencesGroupView(title: texts.group, description: texts.groupDescription)
        proposalsGroup = PreferencesGroupView(title: texts.pending)
        textRow = ProfileTextRow(placeholder: texts.textPlaceholder)
        super.init()
        gender.addItems(withTitles: ProfileGender.all.map(texts.genderLabel))
        gender.target = self
        gender.action = #selector(genderChanged(_:))
        share.target = self
        share.action = #selector(shareChanged(_:))
        textRow.textView.delegate = self
        token = store.observe { [weak self] in self?.refresh() }
        rebuildRows()
        refresh()
    }

    /// The page came up: the accounts and the profile are read again.
    func appeared(in window: NSWindow?) {
        self.window = window
        reloadAccounts()
        store.refresh()
    }

    /// The page goes: what was typed is written now.
    func disappearing() {
        flushText()
        for id in dirtyNotes {
            flushNote(id)
        }
    }

    // MARK: Rows

    private func rebuildRows() {
        let g = PreferenceRowView(title: texts.genderRow, trailing: gender)
        genderRow = g
        noteRows = accounts.map { a in
            let row = ProfileNoteRow(account: a, texts: texts)
            row.field.delegate = self
            row.onToggle = { [weak self] in self?.relayout() }
            return row
        }
        let s = PreferenceRowView(title: texts.shareRow, subtitle: texts.shareSubtitle, trailing: share)
        shareRow = s
        group.setRows([g, textRow] + noteRows + [s])
    }

    private func relayout() {
        group.needsLayout = true
    }

    /// Mail and Jira accounts, in account.list order.
    private func reloadAccounts() {
        guard let client else { return }
        accountsGeneration += 1
        let my = accountsGeneration
        Task { @MainActor [weak self] in
            guard let result = try? await client.call(API.AccountList.self, EmptyParams()),
                  let self, my == self.accountsGeneration else { return }
            let list = result.accounts.filter { [.imap, .graph, .jira].contains($0.config.protocolKind) }
            guard list.map(\.id) != self.accounts.map(\.id) || list.map(accountLabel) != self.accounts.map(accountLabel) else { return }
            let open = Set(self.noteRows.filter(\.expanded).map(\.accountID))
            self.accounts = list
            self.rebuildRows()
            for row in self.noteRows where open.contains(row.accountID) {
                row.setExpanded(true)
            }
            self.refresh()
        }
    }

    // MARK: Refresh

    private func refresh() {
        let hidden = store.loaded && !store.supported
        group.isHidden = hidden
        let usable = store.loaded && store.supported
        group.isEnabled = usable
        let p = store.stored
        if let i = ProfileGender.all.firstIndex(of: p.gender) {
            gender.selectItem(at: i)
        } else {
            gender.selectItem(at: 0)
        }
        share.state = p.shareWithMcp ? .on : .off
        textRow.limit = store.limits.text
        if !textDirty, !textRow.editing {
            textRow.setText(p.text)
        }
        textRow.textView.isEditable = usable
        for row in noteRows {
            row.limit = store.limits.accountNote
            let note = p.accounts.first { $0.accountId == row.accountID }?.note ?? ""
            if !dirtyNotes.contains(row.accountID), !row.editing {
                row.setNote(note)
            }
            row.field.isEnabled = usable
        }
        refreshProposals(hidden: hidden || !usable)
    }

    private func refreshProposals(hidden: Bool) {
        let props = hidden ? [] : store.proposals
        proposalsGroup.isHidden = props.isEmpty
        let ids = props.map(\.id)
        guard ids != proposalRows else { return }
        proposalRows = ids
        proposalsGroup.setRows(props.map { q in
            ProfileProposalRow(proposal: q, detail: detail(of: q), texts: texts) { [weak self] accept, edit in
                self?.resolve(q, accept: accept, edit: edit)
            }
        })
    }

    /// Where a proposal came from, and its account.
    private func detail(of q: ProfileProposal) -> String {
        var parts = [q.source == .panel ? texts.fromPanel : texts.fromOtherApp]
        if let id = q.accountId {
            let name = accounts.first { $0.id == id }.map(accountLabel) ?? id.rawValue
            parts.append(L10n.format(texts.accountProposal, [name]))
        }
        return parts.joined(separator: " · ")
    }

    // MARK: Writing

    @objc private func genderChanged(_ sender: Any?) {
        let i = gender.indexOfSelectedItem
        guard ProfileGender.all.indices.contains(i), ProfileGender.all[i] != store.stored.gender else { return }
        store.set(AssistantProfileSetParams(gender: ProfileGender.all[i])) { [weak self] error in
            if error != nil { self?.failed() }
        }
    }

    @objc private func shareChanged(_ sender: Any?) {
        let on = share.state == .on
        guard on != store.stored.shareWithMcp else { return }
        store.set(AssistantProfileSetParams(shareWithMcp: on)) { [weak self] error in
            if error != nil { self?.failed() }
        }
    }

    func textDidChange(_ notification: Foundation.Notification) {
        textDirty = true
        textRow.updateCounter()
        textTimer?.invalidate()
        textTimer = Timer.scheduledTimer(withTimeInterval: 1, repeats: false) { [weak self] _ in
            MainActor.assumeIsolated { self?.flushText() }
        }
    }

    /// Refuses typing beyond the limit (counted in characters, as the
    /// daemon counts them).
    func textView(_ textView: NSTextView, shouldChangeTextIn range: NSRange, replacementString text: String?) -> Bool {
        guard let text else { return true }
        let current = textView.string as NSString
        let next = current.replacingCharacters(in: range, with: text)
        return next.unicodeScalars.count <= textRow.limit || next.unicodeScalars.count <= current.length
    }

    func textDidEndEditing(_ notification: Foundation.Notification) {
        flushText()
    }

    func controlTextDidChange(_ obj: Foundation.Notification) {
        guard let field = obj.object as? NSTextField, let row = noteRows.first(where: { $0.field === field }) else { return }
        if row.field.stringValue.unicodeScalars.count > row.limit {
            row.field.stringValue = String(String.UnicodeScalarView(row.field.stringValue.unicodeScalars.prefix(row.limit)))
            NSSound.beep()
        }
        row.updateCounter()
        let id = row.accountID
        dirtyNotes.insert(id)
        noteTimers[id]?.invalidate()
        noteTimers[id] = Timer.scheduledTimer(withTimeInterval: 1, repeats: false) { [weak self] _ in
            MainActor.assumeIsolated { self?.flushNote(id) }
        }
    }

    func controlTextDidEndEditing(_ obj: Foundation.Notification) {
        guard let field = obj.object as? NSTextField, let row = noteRows.first(where: { $0.field === field }) else { return }
        flushNote(row.accountID)
    }

    private func flushText() {
        textTimer?.invalidate()
        textTimer = nil
        guard textDirty else { return }
        textDirty = false
        let text = textRow.textView.string
        guard text != store.stored.text else { return }
        store.set(AssistantProfileSetParams(text: text)) { [weak self] error in
            if error != nil {
                self?.textDirty = true
                self?.failed()
            }
        }
    }

    private func flushNote(_ id: AccountID) {
        noteTimers[id]?.invalidate()
        noteTimers[id] = nil
        guard dirtyNotes.remove(id) != nil, let row = noteRows.first(where: { $0.accountID == id }) else { return }
        let note = row.field.stringValue
        let stored = store.stored.accounts.first { $0.accountId == id }?.note ?? ""
        guard note != stored else { return }
        store.set(AssistantProfileSetParams(accounts: [ProfileAccountNote(accountId: id, note: note)])) { [weak self] error in
            if error != nil {
                self?.dirtyNotes.insert(id)
                self?.failed()
            }
        }
    }

    private func resolve(_ q: ProfileProposal, accept: Bool, edit: Bool) {
        guard edit else {
            store.resolve(q.id, accept: accept) { [weak self] error in
                if error != nil { self?.failed() }
            }
            return
        }
        guard let window = window ?? group.window else { return }
        let alert = NSAlert()
        alert.messageText = texts.proposalTitle
        alert.addButton(withTitle: texts.save)
        alert.addButton(withTitle: wizardLabel("_Cancel"))
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 320, height: 64))
        field.stringValue = q.text
        field.usesSingleLineMode = false
        field.cell?.wraps = true
        field.cell?.isScrollable = false
        alert.accessoryView = field
        alert.window.initialFirstResponder = field
        alert.beginSheetModal(for: window) { [weak self] response in
            MainActor.assumeIsolated {
                guard response == .alertFirstButtonReturn, let self else { return }
                self.store.resolve(q.id, accept: true, text: field.stringValue) { [weak self] error in
                    if error != nil { self?.failed() }
                }
            }
        }
    }

    /// A refused write: the store keeps the daemon's profile, the page
    /// shows it again.
    private func failed() {
        NSSound.beep()
        refresh()
    }
}

/// The profile's text: a multi-line plain-text view with its placeholder
/// and a counter "n / limit" under it.
@MainActor
final class ProfileTextRow: NSView, PrefsGroupMember {
    let textView = NSTextView()
    var limit = API.Limits.maxProfileText {
        didSet { updateCounter() }
    }
    private let placeholder: NSTextField
    private let counter = NSTextField(labelWithString: "")

    /// The view has the keyboard.
    var editing: Bool { textView.window?.firstResponder === textView }

    init(placeholder text: String) {
        placeholder = NSTextField(labelWithString: text)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        textView.isRichText = false
        textView.importsGraphics = false
        textView.allowsUndo = true
        textView.font = .systemFont(ofSize: 13)
        textView.textContainerInset = NSSize(width: 4, height: 6)
        textView.isAutomaticQuoteSubstitutionEnabled = false
        textView.isAutomaticDashSubstitutionEnabled = false
        textView.isAutomaticTextReplacementEnabled = false
        textView.autoresizingMask = [.width]
        textView.isVerticallyResizable = true
        textView.textContainer?.widthTracksTextView = true
        let scroll = NSScrollView()
        scroll.translatesAutoresizingMaskIntoConstraints = false
        scroll.hasVerticalScroller = true
        scroll.borderType = .bezelBorder
        scroll.documentView = textView
        placeholder.textColor = .placeholderTextColor
        placeholder.font = .systemFont(ofSize: 13)
        placeholder.lineBreakMode = .byTruncatingTail
        placeholder.translatesAutoresizingMaskIntoConstraints = false
        counter.font = .monospacedDigitSystemFont(ofSize: 11, weight: .regular)
        counter.textColor = .secondaryLabelColor
        counter.alignment = .right
        counter.translatesAutoresizingMaskIntoConstraints = false
        addSubview(scroll)
        addSubview(placeholder)
        addSubview(counter)
        let inset = PreferenceRowView.inset
        NSLayoutConstraint.activate([
            scroll.topAnchor.constraint(equalTo: topAnchor, constant: inset),
            scroll.leadingAnchor.constraint(equalTo: leadingAnchor, constant: inset),
            scroll.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -inset),
            scroll.heightAnchor.constraint(equalToConstant: 96),
            placeholder.topAnchor.constraint(equalTo: scroll.topAnchor, constant: 7),
            placeholder.leadingAnchor.constraint(equalTo: scroll.leadingAnchor, constant: 10),
            placeholder.trailingAnchor.constraint(lessThanOrEqualTo: scroll.trailingAnchor, constant: -10),
            counter.topAnchor.constraint(equalTo: scroll.bottomAnchor, constant: 4),
            counter.trailingAnchor.constraint(equalTo: scroll.trailingAnchor),
            counter.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -8),
        ])
        NotificationCenter.default.addObserver(
            forName: NSText.didChangeNotification, object: textView, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated { self?.updateCounter() }
        }
        updateCounter()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func setText(_ s: String) {
        guard textView.string != s else { return }
        textView.string = s
        updateCounter()
    }

    func updateCounter() {
        counter.stringValue = Assistant.profileCounter(textView.string.unicodeScalars.count, limit: limit)
        placeholder.isHidden = !textView.string.isEmpty
    }

    func setGroupEnabled(_ enabled: Bool) {
        textView.isSelectable = enabled
        textView.textColor = enabled ? .labelColor : .tertiaryLabelColor
    }
}

/// An account's note: the account's name and, at the end, the start of
/// its note or "No Note"; the disclosure opens the field in place, with
/// its counter.
@MainActor
final class ProfileNoteRow: NSView, PrefsGroupMember {
    let accountID: AccountID
    let field = NSTextField()
    var limit = API.Limits.maxProfileAccountNote {
        didSet { updateCounter() }
    }
    var onToggle: (() -> Void)?
    private(set) var expanded = false
    private let disclosure = NSButton()
    private let summary = NSTextField(labelWithString: "")
    private let counter = NSTextField(labelWithString: "")
    private let editor: NSStackView
    private let noNote: String

    /// The field has the keyboard.
    var editing: Bool { field.currentEditor() != nil }

    init(account: Account, texts: Assistant.ProfileStrings) {
        accountID = account.id
        noNote = texts.noNote
        field.placeholderString = texts.notePlaceholder
        field.usesSingleLineMode = false
        field.cell?.wraps = true
        field.cell?.isScrollable = false
        field.lineBreakMode = .byWordWrapping
        counter.font = .monospacedDigitSystemFont(ofSize: 11, weight: .regular)
        counter.textColor = .secondaryLabelColor
        counter.alignment = .right
        editor = NSStackView(views: [field, counter])
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        disclosure.bezelStyle = .disclosure
        disclosure.setButtonType(.pushOnPushOff)
        disclosure.title = ""
        disclosure.target = self
        disclosure.action = #selector(toggle(_:))
        let name = NSTextField(labelWithString: accountLabel(account))
        name.lineBreakMode = .byTruncatingTail
        name.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        summary.textColor = .secondaryLabelColor
        summary.lineBreakMode = .byTruncatingTail
        summary.alignment = .right
        summary.setContentCompressionResistancePriority(.defaultLow - 1, for: .horizontal)
        let head = NSStackView(views: [disclosure, name, NSView(), summary])
        head.orientation = .horizontal
        head.alignment = .centerY
        head.spacing = 6
        editor.orientation = .vertical
        editor.alignment = .trailing
        editor.spacing = 4
        editor.isHidden = true
        field.widthAnchor.constraint(equalTo: editor.widthAnchor).isActive = true
        field.heightAnchor.constraint(greaterThanOrEqualToConstant: 44).isActive = true
        let column = NSStackView(views: [head, editor])
        column.orientation = .vertical
        column.alignment = .leading
        column.spacing = 8
        column.translatesAutoresizingMaskIntoConstraints = false
        for v in [head, editor] {
            v.widthAnchor.constraint(equalTo: column.widthAnchor).isActive = true
        }
        addSubview(column)
        let inset = PreferenceRowView.inset
        NSLayoutConstraint.activate([
            column.topAnchor.constraint(equalTo: topAnchor, constant: 10),
            column.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -10),
            column.leadingAnchor.constraint(equalTo: leadingAnchor, constant: inset),
            column.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -inset),
            heightAnchor.constraint(greaterThanOrEqualToConstant: PreferenceRowView.minHeight),
        ])
        updateCounter()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func setNote(_ note: String) {
        if field.stringValue != note {
            field.stringValue = note
        }
        updateCounter()
    }

    func setExpanded(_ on: Bool) {
        expanded = on
        disclosure.state = on ? .on : .off
        editor.isHidden = !on
        onToggle?()
    }

    @objc private func toggle(_ sender: Any?) {
        setExpanded(disclosure.state == .on)
        if expanded {
            window?.makeFirstResponder(field)
        }
    }

    func updateCounter() {
        let note = field.stringValue
        counter.stringValue = Assistant.profileCounter(note.unicodeScalars.count, limit: limit)
        let words = note.split(whereSeparator: \.isWhitespace).joined(separator: " ")
        summary.stringValue = words.isEmpty ? noNote : (words.count > 40 ? String(words.prefix(40)) + "…" : words)
    }

    func setGroupEnabled(_ enabled: Bool) {
        disclosure.isEnabled = enabled
        field.isEnabled = enabled
    }
}

/// A note the assistant suggested: its text, where it came from, and Save,
/// Edit… and Dismiss.
@MainActor
final class ProfileProposalRow: NSView, PrefsGroupMember {
    private let buttons: [NSButton]
    private let act: (_ accept: Bool, _ edit: Bool) -> Void

    init(proposal q: ProfileProposal, detail: String, texts: Assistant.ProfileStrings, act: @escaping (_ accept: Bool, _ edit: Bool) -> Void) {
        self.act = act
        let save = NSButton(title: texts.save, target: nil, action: nil)
        let edit = NSButton(title: texts.edit, target: nil, action: nil)
        let dismiss = NSButton(title: texts.dismiss, target: nil, action: nil)
        buttons = [save, edit, dismiss]
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        let text = PrefsWrappingLabel(q.text)
        let meta = PrefsWrappingLabel(detail, size: 11, color: .secondaryLabelColor)
        for (b, action) in [(save, #selector(saveClicked(_:))), (edit, #selector(editClicked(_:))), (dismiss, #selector(dismissClicked(_:)))] {
            b.bezelStyle = .push
            b.controlSize = .small
            b.target = self
            b.action = action
        }
        let row = NSStackView(views: buttons)
        row.orientation = .horizontal
        row.spacing = 6
        let column = prefsColumn(spacing: 6)
        prefsAddFilling(text, to: column)
        prefsAddFilling(meta, to: column)
        column.addArrangedSubview(row)
        column.translatesAutoresizingMaskIntoConstraints = false
        addSubview(column)
        let inset = PreferenceRowView.inset
        NSLayoutConstraint.activate([
            column.topAnchor.constraint(equalTo: topAnchor, constant: 10),
            column.bottomAnchor.constraint(equalTo: bottomAnchor, constant: -10),
            column.leadingAnchor.constraint(equalTo: leadingAnchor, constant: inset),
            column.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -inset),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func setGroupEnabled(_ enabled: Bool) {
        for b in buttons {
            b.isEnabled = enabled
        }
    }

    @objc private func saveClicked(_ sender: Any?) { act(true, false) }
    @objc private func editClicked(_ sender: Any?) { act(true, true) }
    @objc private func dismissClicked(_ sender: Any?) { act(false, false) }
}
