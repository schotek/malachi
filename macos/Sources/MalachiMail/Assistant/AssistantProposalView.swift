// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// A note the assistant proposed for the user's profile
/// (`AssistantPanelController.Content.proposal`, docs/plans/
/// assistant-profile.md P16): "The assistant suggests remembering:", the
/// note as plain text (read from the `ProfileStore`, never from the
/// model's answer), the account it is for, and Save, Edit… and Dismiss.
/// Save adds it to the profile as it is, Edit… lets the user change it
/// first; Dismissed it goes. A proposal the store no longer has (resolved
/// in Settings or another window) leaves the card without buttons.
@MainActor
final class AssistantProposalView: NSView, AssistantItemView {
    private let symbol = CalloutCard.symbolView()
    private let title = assistantLabel(size: 12, color: .secondaryLabelColor)
    private let text = assistantLabel()
    private let detail = assistantLabel(size: 11, color: .secondaryLabelColor)
    private let saveButton: NSButton
    private let editButton: NSButton
    private let dismissButton: NSButton
    private let buttons: NSStackView

    private enum Outcome { case pending, saved, dismissed }

    private var id: String
    private weak var store: ProfileStore?
    private var token: BoardObserverToken?
    private var outcome = Outcome.pending
    private var busy = false

    init(_ content: AssistantPanelController.Content, store: ProfileStore?) {
        let t = Assistant.profileTexts()
        saveButton = NSButton(title: t.save, target: nil, action: nil)
        editButton = NSButton(title: t.edit, target: nil, action: nil)
        dismissButton = NSButton(title: t.dismiss, target: nil, action: nil)
        buttons = NSStackView(views: [saveButton, editButton, dismissButton])
        if case .proposal(let id) = content {
            self.id = id
        } else {
            self.id = ""
        }
        self.store = store
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        CalloutCard.show("person.text.rectangle", .info, in: symbol)
        title.stringValue = t.proposalTitle
        for (b, action) in [(saveButton, #selector(save(_:))), (editButton, #selector(edit(_:))), (dismissButton, #selector(dismiss(_:)))] {
            b.bezelStyle = .push
            b.controlSize = .small
            b.target = self
            b.action = action
        }
        buttons.orientation = .horizontal
        buttons.spacing = 6
        let head = NSStackView(views: [symbol, title])
        head.orientation = .horizontal
        head.alignment = .centerY
        head.spacing = CalloutCard.spacing
        let column = NSStackView(views: [head, text, detail, buttons])
        column.orientation = .vertical
        column.alignment = .leading
        column.spacing = 6
        for v in [text, detail] {
            v.widthAnchor.constraint(equalTo: column.widthAnchor).isActive = true
        }
        CalloutCard.install(column, in: self, margins: NSEdgeInsets())
        token = store?.observe { [weak self] in self?.refresh() }
        refresh()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func accepts(_ content: AssistantPanelController.Content) -> Bool {
        if case .proposal = content { return true }
        return false
    }

    func update(_ content: AssistantPanelController.Content) {
        guard case .proposal(let id) = content, id != self.id else { return }
        self.id = id
        outcome = .pending
        refresh()
    }

    private func refresh() {
        let t = Assistant.profileTexts()
        isHidden = outcome == .dismissed
        guard outcome == .pending, let q = store?.proposal(id) else {
            buttons.isHidden = true
            detail.isHidden = outcome != .saved
            detail.stringValue = outcome == .saved ? t.proposalOK : ""
            return
        }
        text.stringValue = q.text
        if let acc = q.accountId,
           let note = store?.profile.accounts.first(where: { $0.accountID == acc.rawValue }) ?? nil {
            detail.stringValue = L10n.format(t.accountProposal, [note.name.isEmpty ? note.email : note.name])
            detail.isHidden = false
        } else if let acc = q.accountId {
            detail.stringValue = L10n.format(t.accountProposal, [acc.rawValue])
            detail.isHidden = false
        } else {
            detail.isHidden = true
        }
        buttons.isHidden = false
        for b in [saveButton, editButton, dismissButton] {
            b.isEnabled = !busy
        }
    }

    private func resolve(accept: Bool, text edited: String? = nil) {
        guard let store, !busy else { return }
        busy = true
        refresh()
        store.resolve(id, accept: accept, text: edited) { [weak self] error in
            guard let self else { return }
            self.busy = false
            if error == nil {
                self.outcome = accept ? .saved : .dismissed
            } else {
                NSSound.beep()
            }
            self.refresh()
        }
    }

    @objc private func save(_ sender: Any?) {
        resolve(accept: true)
    }

    @objc private func dismiss(_ sender: Any?) {
        resolve(accept: false)
    }

    /// The proposal's text in a sheet; Save adds the edited text.
    @objc private func edit(_ sender: Any?) {
        guard let window, let q = store?.proposal(id) else { return }
        let t = Assistant.profileTexts()
        let alert = NSAlert()
        alert.messageText = t.proposalTitle
        alert.addButton(withTitle: t.save)
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
                guard response == .alertFirstButtonReturn else { return }
                self?.resolve(accept: true, text: field.stringValue)
            }
        }
    }
}
