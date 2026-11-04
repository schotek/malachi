// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Save to eWay-CRM dialog (docs/eway.md §2.2, the approved wireframe;
/// GTK `crm_dialog.blp`) as a sheet over the window, not an alert: the
/// title (Save to eWay-CRM, or Edit Links over a saved message), one line
/// naming the messages, the search field ("Search eWay-CRM", crm.search
/// after a pause in typing), the items in the groups Contacts, Companies,
/// Deals and Projects as check boxes with their detail under them, the
/// Superior Item marker on deals and projects, the Private check box with
/// its hint, the hint when eWay-CRM did not answer in time, and Cancel and
/// Save in the Mac's order (Save the default button, off until
/// `CRM.Dialog.canSave`). A spinner shows while crm.lookup runs; a failed
/// one shows its sentence and leaves only Cancel. Everything that came
/// from the CRM is plain text (`stringValue`, button titles); the model is
/// `CRMDialogSession` / `CRM.Dialog`.
@MainActor
final class CRMDialogController: NSWindowController, NSSearchFieldDelegate {
    private static var active: [ObjectIdentifier: CRMDialogController] = [:]
    static let size = NSSize(width: 520, height: 600)

    let session: CRMDialogSession
    private let titleLabel = NSTextField(labelWithString: "")
    private let summaryLabel = NSTextField(labelWithString: "")
    private let searchField = NSSearchField()
    private let searchSpinner = NSProgressIndicator()
    private let searchProblem = NSTextField(wrappingLabelWithString: "")
    private let list = NSStackView()
    private let scroll = NSScrollView()
    private let loading = NSStackView()
    private let loadingSpinner = NSProgressIndicator()
    private let message = NSTextField(wrappingLabelWithString: "")
    private let partial = NSTextField(wrappingLabelWithString: "")
    private let searchHint = NSTextField(wrappingLabelWithString: "")
    private let privateBox = NSButton(checkboxWithTitle: "", target: nil, action: nil)
    private let privateHint = NSTextField(wrappingLabelWithString: "")
    private let cancelButton = NSButton(title: "", target: nil, action: nil)
    private let saveButton = NSButton(title: "", target: nil, action: nil)
    /// The rows by item, while the listed items stay the same.
    private var rows: [CrmItemRef: (check: NSButton, superior: NSButton?)] = [:]
    private var listed: [CrmItemRef] = []

    /// Opens the dialog for `session` as a sheet on `parent`; the sheet
    /// closes when the session ends (Save, Cancel, Escape).
    static func present(_ session: CRMDialogSession, on parent: NSWindow) {
        let c = CRMDialogController(session: session)
        guard let sheet = c.window else { return }
        active[ObjectIdentifier(c)] = c
        session.onChange = { [weak c] in c?.render() }
        session.onClose = { [weak c] in c?.close() }
        parent.beginSheet(sheet) { _ in
            MainActor.assumeIsolated {
                session.cancel()
                active[ObjectIdentifier(c)] = nil
            }
        }
        c.render()
        sheet.makeFirstResponder(c.searchField)
    }

    private init(session: CRMDialogSession) {
        self.session = session
        let window = WizardSheetWindow(
            contentRect: NSRect(origin: .zero, size: Self.size), styleMask: [.titled], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        super.init(window: window)
        window.onCancel = { [weak self] in self?.session.cancel() }
        window.contentView = build()
        window.title = session.texts.title
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func close() {
        guard let window else { return }
        if let parent = window.sheetParent {
            parent.endSheet(window)
        } else {
            window.close()
        }
    }

    // MARK: Building

    private func build() -> NSView {
        let texts = session.texts
        titleLabel.font = .systemFont(ofSize: 15, weight: .semibold)
        summaryLabel.font = .systemFont(ofSize: 12)
        summaryLabel.textColor = .secondaryLabelColor
        summaryLabel.lineBreakMode = .byTruncatingTail
        summaryLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        summaryLabel.stringValue = session.summary
        summaryLabel.toolTip = session.summary

        searchField.placeholderString = texts.search
        searchField.delegate = self
        searchField.target = self
        searchField.action = #selector(searchChanged(_:))
        searchField.sendsSearchStringImmediately = true
        searchSpinner.style = .spinning
        searchSpinner.controlSize = .small
        searchSpinner.isDisplayedWhenStopped = false
        let searchRow = NSStackView(views: [searchField, searchSpinner])
        searchRow.orientation = .horizontal
        searchRow.spacing = 6
        searchProblem.font = .systemFont(ofSize: 11)
        searchProblem.textColor = .systemRed
        searchProblem.isHidden = true

        list.orientation = .vertical
        list.alignment = .leading
        list.spacing = 4
        list.edgeInsets = NSEdgeInsets(top: 4, left: 2, bottom: 8, right: 2)
        list.translatesAutoresizingMaskIntoConstraints = false
        let document = FlippedView()
        document.translatesAutoresizingMaskIntoConstraints = false
        document.addSubview(list)
        scroll.documentView = document
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.drawsBackground = false
        scroll.borderType = .noBorder
        scroll.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            list.topAnchor.constraint(equalTo: document.topAnchor),
            list.leadingAnchor.constraint(equalTo: document.leadingAnchor),
            list.trailingAnchor.constraint(equalTo: document.trailingAnchor),
            list.bottomAnchor.constraint(equalTo: document.bottomAnchor),
            document.widthAnchor.constraint(equalTo: scroll.contentView.widthAnchor),
        ])

        loadingSpinner.style = .spinning
        loadingSpinner.controlSize = .small
        let loadingLabel = NSTextField(labelWithString: CRM.actionText(.lookup))
        loadingLabel.textColor = .secondaryLabelColor
        loading.orientation = .horizontal
        loading.spacing = 6
        loading.addArrangedSubview(loadingSpinner)
        loading.addArrangedSubview(loadingLabel)

        for l in [message, partial, searchHint, privateHint] {
            l.font = .systemFont(ofSize: 12)
            l.textColor = .secondaryLabelColor
            l.isSelectable = false
        }
        partial.stringValue = texts.partial
        searchHint.stringValue = texts.searchHint
        privateBox.title = texts.private
        privateBox.target = self
        privateBox.action = #selector(privateToggled(_:))
        privateHint.stringValue = texts.privateHint
        privateHint.font = .systemFont(ofSize: 11)

        cancelButton.title = L10n.T("Cancel")
        cancelButton.bezelStyle = .rounded
        cancelButton.keyEquivalent = "\u{1b}"
        cancelButton.target = self
        cancelButton.action = #selector(cancelClicked(_:))
        saveButton.title = mn(texts.save)
        saveButton.bezelStyle = .rounded
        saveButton.keyEquivalent = "\r"
        saveButton.target = self
        saveButton.action = #selector(saveClicked(_:))
        let buttons = NSStackView(views: [cancelButton, saveButton])
        buttons.orientation = .horizontal
        buttons.spacing = 12

        let privateColumn = NSStackView(views: [privateBox, privateHint])
        privateColumn.orientation = .vertical
        privateColumn.alignment = .leading
        privateColumn.spacing = 2
        privateHint.setContentHuggingPriority(.defaultLow, for: .horizontal)

        let column = NSStackView(views: [titleLabel, summaryLabel, searchRow, searchProblem, loading, message, scroll, searchHint, partial, privateColumn])
        column.orientation = .vertical
        column.alignment = .leading
        column.spacing = 10
        column.setCustomSpacing(2, after: titleLabel)
        column.translatesAutoresizingMaskIntoConstraints = false
        buttons.translatesAutoresizingMaskIntoConstraints = false
        let root = NSView(frame: NSRect(origin: .zero, size: Self.size))
        root.addSubview(column)
        root.addSubview(buttons)
        NSLayoutConstraint.activate([
            column.topAnchor.constraint(equalTo: root.topAnchor, constant: 20),
            column.leadingAnchor.constraint(equalTo: root.leadingAnchor, constant: 20),
            column.trailingAnchor.constraint(equalTo: root.trailingAnchor, constant: -20),
            buttons.topAnchor.constraint(equalTo: column.bottomAnchor, constant: 16),
            buttons.trailingAnchor.constraint(equalTo: root.trailingAnchor, constant: -20),
            buttons.bottomAnchor.constraint(equalTo: root.bottomAnchor, constant: -20),
            searchRow.widthAnchor.constraint(equalTo: column.widthAnchor),
            scroll.widthAnchor.constraint(equalTo: column.widthAnchor),
            summaryLabel.widthAnchor.constraint(lessThanOrEqualTo: column.widthAnchor),
            message.widthAnchor.constraint(equalTo: column.widthAnchor),
            partial.widthAnchor.constraint(equalTo: column.widthAnchor),
            searchHint.widthAnchor.constraint(equalTo: column.widthAnchor),
            searchProblem.widthAnchor.constraint(equalTo: column.widthAnchor),
            privateHint.widthAnchor.constraint(lessThanOrEqualTo: column.widthAnchor),
            root.widthAnchor.constraint(equalToConstant: Self.size.width),
            root.heightAnchor.constraint(equalToConstant: Self.size.height),
        ])
        scroll.setContentHuggingPriority(.defaultLow, for: .vertical)
        scroll.setContentCompressionResistancePriority(.defaultLow, for: .vertical)
        return root
    }

    // MARK: Rendering

    /// Draws the session's state: the phase, the groups (rebuilt only when
    /// the listed items change, so the keyboard focus stays), the checks
    /// and markers, Private, the hints and Save.
    private func render() {
        let texts = session.texts
        titleLabel.stringValue = texts.title
        window?.title = texts.title
        let dialog = session.dialog
        switch session.phase {
        case .loading:
            loading.isHidden = false
            loadingSpinner.startAnimation(nil)
            message.isHidden = true
        case .ready:
            loading.isHidden = true
            loadingSpinner.stopAnimation(nil)
            message.stringValue = texts.empty
            message.isHidden = !(dialog?.isEmpty ?? true)
        case .failed(let text):
            loading.isHidden = true
            loadingSpinner.stopAnimation(nil)
            message.stringValue = text
            message.isHidden = false
        }
        let ready = session.phase == .ready
        searchField.isEnabled = ready
        if session.searching {
            searchSpinner.startAnimation(nil)
        } else {
            searchSpinner.stopAnimation(nil)
        }
        searchProblem.stringValue = session.searchProblem ?? ""
        searchProblem.isHidden = session.searchProblem == nil
        partial.isHidden = !(dialog?.partial ?? false)
        searchHint.isHidden = !(ready && (dialog?.offersSearchHint ?? false))
        privateBox.isEnabled = ready
        privateBox.state = dialog?.isPrivate == true ? .on : .off
        saveButton.isEnabled = ready && (dialog?.canSave ?? false)
        renderList(dialog)
    }

    private func renderList(_ dialog: CRM.Dialog?) {
        let groups = dialog?.groups ?? []
        let refs = groups.flatMap { $0.entries.map(\.ref) }
        if refs != listed {
            rebuild(groups)
            listed = refs
        }
        for g in groups {
            for e in g.entries {
                guard let row = rows[e.ref] else { continue }
                row.check.state = e.checked ? .on : .off
                row.superior?.state = e.superior ? .on : .off
            }
        }
    }

    private func rebuild(_ groups: [CRM.DialogGroup]) {
        for v in list.arrangedSubviews {
            list.removeArrangedSubview(v)
            v.removeFromSuperview()
        }
        rows = [:]
        let superiorTitle = session.texts.superiorItem
        for g in groups {
            let heading = NSTextField(labelWithString: CRM.groupTitle(g.kind))
            heading.font = .systemFont(ofSize: 12, weight: .semibold)
            heading.textColor = .secondaryLabelColor
            list.addArrangedSubview(heading)
            list.setCustomSpacing(4, after: heading)
            for e in g.entries {
                let check = NSButton(checkboxWithTitle: e.item.name, target: self, action: #selector(checkToggled(_:)))
                check.identifier = NSUserInterfaceItemIdentifier(Self.key(e.ref))
                check.lineBreakMode = .byTruncatingTail
                check.toolTip = e.item.name
                var parts: [NSView] = [check]
                var superior: NSButton?
                if e.canBeSuperior {
                    let radio = NSButton(radioButtonWithTitle: superiorTitle, target: self, action: #selector(superiorChosen(_:)))
                    radio.identifier = NSUserInterfaceItemIdentifier(Self.key(e.ref))
                    radio.controlSize = .small
                    radio.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
                    parts.append(radio)
                    superior = radio
                }
                let line = NSStackView(views: parts)
                line.orientation = .horizontal
                line.spacing = 12
                let entry = NSStackView(views: [line])
                entry.orientation = .vertical
                entry.alignment = .leading
                entry.spacing = 0
                if let detail = e.item.detail, !detail.isEmpty {
                    let d = NSTextField(labelWithString: detail)
                    d.font = .systemFont(ofSize: 11)
                    d.textColor = .secondaryLabelColor
                    d.lineBreakMode = .byTruncatingTail
                    d.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
                    let indent = NSStackView(views: [d])
                    indent.edgeInsets = NSEdgeInsets(top: 0, left: 20, bottom: 0, right: 0)
                    entry.addArrangedSubview(indent)
                }
                list.addArrangedSubview(entry)
                rows[e.ref] = (check, superior)
            }
            if let last = list.arrangedSubviews.last {
                list.setCustomSpacing(12, after: last)
            }
        }
    }

    private static func key(_ r: CrmItemRef) -> String {
        r.kind.rawValue + "/" + r.guid
    }

    private func ref(of sender: NSButton) -> CrmItemRef? {
        guard let id = sender.identifier?.rawValue else { return nil }
        return rows.keys.first { Self.key($0) == id }
    }

    // MARK: Actions

    @objc private func checkToggled(_ sender: NSButton) {
        guard let r = ref(of: sender) else { return }
        session.toggle(r, sender.state == .on)
        // A refused check (more than 50 changes) is drawn back.
        render()
    }

    @objc private func superiorChosen(_ sender: NSButton) {
        guard let r = ref(of: sender) else { return }
        session.setSuperior(r)
        render()
    }

    @objc private func privateToggled(_ sender: NSButton) {
        session.setPrivate(sender.state == .on)
    }

    @objc private func searchChanged(_ sender: NSSearchField) {
        session.search(sender.stringValue)
    }

    func controlTextDidChange(_ obj: Foundation.Notification) {
        session.search(searchField.stringValue)
    }

    @objc private func cancelClicked(_ sender: Any?) {
        session.cancel()
    }

    @objc private func saveClicked(_ sender: Any?) {
        session.save()
    }
}

/// A document view that lays out from the top, for the dialog's list.
@MainActor
private final class FlippedView: NSView {
    override var isFlipped: Bool { true }
}
