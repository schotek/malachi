// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The rule editor (docs/plans/rules.md R14; ui/internal/rules) as a sheet
/// over the window it came from: the name, Apply to (the mail accounts,
/// then All Mail Accounts), how the conditions combine, the condition rows
/// (field, operator, a text value or a choice, remove) with Add Condition,
/// the action rows (what they do, a folder for Move To, the address of
/// Forward to, the subject and text of a reply, the outgoing hint, remove)
/// with Add Action as a menu of what may still be added, Stop processing
/// more rules with its hint, the Rule is on switch, each problem under its
/// row, Try It over a folder with its result line, and Cancel and Save in
/// the Mac's order (Save off while there is a problem). The model is
/// `RuleEditorController`; every value of a rule (it may come from mail)
/// is plain text (`stringValue`, plain text views).
@MainActor
final class RuleEditorWindowController: NSWindowController, NSTextFieldDelegate, NSTextViewDelegate {
    static let size = NSSize(width: 640, height: 660)

    let editor: RuleEditorController
    private unowned let rules: AppRules
    /// The sheet ended (saved or cancelled).
    var onClosed: (() -> Void)?

    private let titleLabel = NSTextField(labelWithString: "")
    private let nameField = NSTextField()
    private let nameProblem = RuleEditorWindowController.problemLabel()
    private let accountPopup = NSPopUpButton()
    private let matchPopup = NSPopUpButton()
    private let conditionRows = NSStackView()
    private let conditionsProblem = RuleEditorWindowController.problemLabel()
    private let addConditionButton = NSButton(title: "", target: nil, action: nil)
    private let actionRows = NSStackView()
    private let actionsProblem = RuleEditorWindowController.problemLabel()
    private let addActionButton = NSPopUpButton(frame: .zero, pullsDown: true)
    private let stopBox = NSButton(checkboxWithTitle: "", target: nil, action: nil)
    private let enabledSwitch = NSSwitch()
    private let testFolderPopup = NSPopUpButton()
    private let testButton = NSButton(title: "", target: nil, action: nil)
    private let testSpinner = NSProgressIndicator()
    private let testResult = NSTextField(labelWithString: "")
    private let errorLabel = RuleEditorWindowController.problemLabel()
    private let cancelButton = NSButton(title: "", target: nil, action: nil)
    private let saveButton = NSButton(title: "", target: nil, action: nil)
    private let saveSpinner = NSProgressIndicator()

    /// The rows' problem labels, by row.
    private var conditionProblems: [NSTextField] = []
    private var actionProblems: [NSTextField] = []
    /// What the rows were built for: a change of it rebuilds them, any
    /// other change only updates values, problems and buttons (so the
    /// keyboard stays where it is).
    private var builtShape: String?
    private var builtAccounts = false

    init(editor: RuleEditorController, rules: AppRules) {
        self.editor = editor
        self.rules = rules
        let window = WizardSheetWindow(
            contentRect: NSRect(origin: .zero, size: Self.size), styleMask: [.titled, .resizable], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        window.minSize = NSSize(width: 560, height: 480)
        super.init(window: window)
        window.onCancel = { [weak self] in self?.close() }
        window.contentView = build()
        editor.onChange = { [weak self] in self?.render() }
        render()
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Shows the sheet on `parent`.
    func present(on parent: NSWindow) {
        guard let sheet = window else { return }
        parent.beginSheet(sheet) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.onClosed?()
            }
        }
        sheet.makeFirstResponder(nameField)
    }

    override func close() {
        guard let window else { return }
        if let parent = window.sheetParent {
            parent.endSheet(window)
        } else {
            window.close()
            onClosed?()
        }
    }

    // MARK: Building

    private static func problemLabel() -> NSTextField {
        let l = NSTextField(wrappingLabelWithString: "")
        l.font = Typo.caption
        l.textColor = .systemRed
        l.isSelectable = false
        l.isHidden = true
        return l
    }

    private static func caption(_ text: String) -> NSTextField {
        let l = NSTextField(wrappingLabelWithString: text)
        l.font = Typo.caption
        l.textColor = .secondaryLabelColor
        l.isSelectable = false
        return l
    }

    private static func heading(_ text: String) -> NSTextField {
        let l = NSTextField(labelWithString: text)
        l.font = Typo.heading
        return l
    }

    private static func symbolButton(_ symbol: String, tooltip: String) -> NSButton {
        let b = NSButton(image: wizardSymbol(symbol, pointSize: 14), target: nil, action: nil)
        b.isBordered = false
        b.imagePosition = .imageOnly
        b.contentTintColor = .secondaryLabelColor
        b.toolTip = tooltip
        b.setAccessibilityLabel(tooltip)
        b.setContentHuggingPriority(.required, for: .horizontal)
        return b
    }

    private func build() -> NSView {
        titleLabel.font = .systemFont(ofSize: 15, weight: .semibold)

        nameField.placeholderString = Rules.nameLabel
        nameField.delegate = self
        nameField.lineBreakMode = .byTruncatingTail
        nameField.setAccessibilityLabel(Rules.nameLabel)
        accountPopup.target = self
        accountPopup.action = #selector(accountChosen(_:))
        accountPopup.setAccessibilityLabel(Rules.accountLabel)
        for m in [RuleMatch.all, .any] {
            matchPopup.addItem(withTitle: Rules.matchText(m))
            matchPopup.lastItem?.representedObject = m.rawValue
        }
        matchPopup.target = self
        matchPopup.action = #selector(matchChosen(_:))
        matchPopup.setAccessibilityLabel(Rules.matchLabel)

        let grid = NSGridView(views: [
            [NSTextField(labelWithString: Rules.nameLabel), nameField],
            [NSGridCell.emptyContentView, nameProblem],
            [NSTextField(labelWithString: Rules.accountLabel), accountPopup],
        ])
        grid.rowSpacing = 6
        grid.columnSpacing = 10
        grid.column(at: 0).xPlacement = .trailing
        grid.rowAlignment = .firstBaseline
        grid.translatesAutoresizingMaskIntoConstraints = false

        let matchRow = NSStackView(views: [NSTextField(labelWithString: Rules.matchLabel), matchPopup])
        matchRow.orientation = .horizontal
        matchRow.spacing = 8

        for stack in [conditionRows, actionRows] {
            stack.orientation = .vertical
            stack.alignment = .leading
            stack.spacing = 6
            stack.translatesAutoresizingMaskIntoConstraints = false
        }

        addConditionButton.title = Rules.addCondition
        addConditionButton.image = wizardSymbol("plus", pointSize: 12)
        addConditionButton.imagePosition = .imageLeading
        addConditionButton.bezelStyle = .rounded
        addConditionButton.target = self
        addConditionButton.action = #selector(addCondition(_:))

        addActionButton.bezelStyle = .rounded
        addActionButton.autoenablesItems = false

        stopBox.title = Rules.stopLabel
        stopBox.target = self
        stopBox.action = #selector(stopToggled(_:))
        let stopHint = Self.caption(Rules.stopHint)
        enabledSwitch.target = self
        enabledSwitch.action = #selector(enabledToggled(_:))
        enabledSwitch.setAccessibilityLabel(Rules.enabledLabel)
        let enabledRow = NSStackView(views: [enabledSwitch, NSTextField(labelWithString: Rules.enabledLabel)])
        enabledRow.orientation = .horizontal
        enabledRow.spacing = 8

        testFolderPopup.target = self
        testFolderPopup.action = #selector(testFolderChosen(_:))
        testFolderPopup.autoenablesItems = false
        testFolderPopup.setAccessibilityLabel(Rules.testFolderLabel)
        testButton.title = Rules.testButton
        testButton.bezelStyle = .rounded
        testButton.target = self
        testButton.action = #selector(testClicked(_:))
        testSpinner.style = .spinning
        testSpinner.controlSize = .small
        testSpinner.isDisplayedWhenStopped = false
        testResult.textColor = .secondaryLabelColor
        testResult.lineBreakMode = .byTruncatingTail
        testResult.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        let testRow = NSStackView(views: [NSTextField(labelWithString: Rules.testFolderLabel), testFolderPopup, testButton, testSpinner, testResult])
        testRow.orientation = .horizontal
        testRow.spacing = 8

        let column = prefsColumn(spacing: 10, insets: NSEdgeInsets(top: 16, left: 20, bottom: 16, right: 20))
        for v in [titleLabel, grid, matchRow, conditionRows, conditionsProblem, addConditionButton,
                  Self.heading(Rules.actionsHeading), actionRows, actionsProblem, addActionButton,
                  stopBox, stopHint, enabledRow, testRow] as [NSView] {
            column.addArrangedSubview(v)
        }
        for v in [conditionRows, actionRows, conditionsProblem, actionsProblem, stopHint, testRow] as [NSView] {
            prefsFillWidth(v, in: column)
        }
        column.setCustomSpacing(16, after: titleLabel)
        column.setCustomSpacing(16, after: addConditionButton)
        column.setCustomSpacing(16, after: addActionButton)
        column.setCustomSpacing(2, after: stopBox)
        column.setCustomSpacing(16, after: stopHint)
        column.setCustomSpacing(16, after: enabledRow)

        let document = RuleEditorDocumentView()
        document.translatesAutoresizingMaskIntoConstraints = false
        document.addSubview(column)
        let scroll = NSScrollView()
        scroll.documentView = document
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.drawsBackground = false
        scroll.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            column.topAnchor.constraint(equalTo: document.topAnchor),
            column.leadingAnchor.constraint(equalTo: document.leadingAnchor),
            column.trailingAnchor.constraint(equalTo: document.trailingAnchor),
            column.bottomAnchor.constraint(equalTo: document.bottomAnchor),
            document.widthAnchor.constraint(equalTo: scroll.contentView.widthAnchor),
            nameField.widthAnchor.constraint(greaterThanOrEqualToConstant: 320),
        ])

        errorLabel.translatesAutoresizingMaskIntoConstraints = false
        cancelButton.title = mn(L10n.T("_Cancel"))
        cancelButton.bezelStyle = .rounded
        cancelButton.keyEquivalent = "\u{1b}"
        cancelButton.target = self
        cancelButton.action = #selector(cancelClicked(_:))
        saveButton.title = mn(L10n.T("_Save"))
        saveButton.bezelStyle = .rounded
        saveButton.keyEquivalent = "\r"
        saveButton.target = self
        saveButton.action = #selector(saveClicked(_:))
        saveSpinner.style = .spinning
        saveSpinner.controlSize = .small
        saveSpinner.isDisplayedWhenStopped = false
        let buttons = NSStackView(views: [saveSpinner, cancelButton, saveButton])
        buttons.orientation = .horizontal
        buttons.spacing = 12
        buttons.translatesAutoresizingMaskIntoConstraints = false

        let root = NSView(frame: NSRect(origin: .zero, size: Self.size))
        root.addSubview(scroll)
        root.addSubview(errorLabel)
        root.addSubview(buttons)
        NSLayoutConstraint.activate([
            scroll.topAnchor.constraint(equalTo: root.topAnchor),
            scroll.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            errorLabel.topAnchor.constraint(equalTo: scroll.bottomAnchor, constant: 8),
            errorLabel.leadingAnchor.constraint(equalTo: root.leadingAnchor, constant: 20),
            errorLabel.trailingAnchor.constraint(equalTo: root.trailingAnchor, constant: -20),
            buttons.topAnchor.constraint(equalTo: errorLabel.bottomAnchor, constant: 8),
            buttons.trailingAnchor.constraint(equalTo: root.trailingAnchor, constant: -20),
            buttons.bottomAnchor.constraint(equalTo: root.bottomAnchor, constant: -16),
        ])
        return root
    }

    // MARK: Rendering

    /// What decides the rows' controls: a change of a field's value kind,
    /// of an operator list, of the actions' kinds, of the account (the
    /// folders) or of what Add Action offers.
    private func shape() -> String {
        let f = editor.form
        var s = "\(f.accountId?.rawValue ?? "")|"
        s += f.conditions.map { "\($0.field.rawValue):\($0.op.rawValue)" }.joined(separator: ",")
        s += "|" + f.actions.map(\.kind.rawValue).joined(separator: ",")
        s += "|" + editor.availableActions.map(\.rawValue).joined(separator: ",")
        return s
    }

    private func render() {
        let f = editor.form
        let title = Rules.editorTitle(f)
        titleLabel.stringValue = title
        window?.title = title
        if !builtAccounts {
            builtAccounts = true
            fillAccounts()
        }
        if nameField.stringValue != f.name {
            nameField.stringValue = f.name
        }
        selectAccount(f.accountId)
        matchPopup.selectItem(at: f.match == .any ? 1 : 0)
        let s = shape()
        if s != builtShape {
            builtShape = s
            buildConditions()
            buildActions()
            fillAddAction()
            fillTestFolders()
        }
        stopBox.state = f.stop ? .on : .off
        enabledSwitch.state = f.enabled ? .on : .off
        // Validated once per drawing.
        let problems = editor.problems
        func texts(_ field: String, _ i: Int) -> [String] {
            problems.filter { $0.field == field && $0.index == i }.map(Rules.problemText)
        }
        show(nameProblem, texts("name", 0))
        for (i, l) in conditionProblems.enumerated() {
            show(l, texts("conditions", i))
        }
        for (i, l) in actionProblems.enumerated() {
            show(l, texts("actions", i))
        }
        // The list's own problems (no row to show them under).
        show(conditionsProblem, f.conditions.isEmpty ? texts("conditions", 0) : [])
        show(actionsProblem, f.actions.isEmpty ? texts("actions", 0) : [])
        if let k = editor.testFolder {
            selectTestFolder(k)
        }
        testButton.isEnabled = editor.testFolder != nil && problems.isEmpty && !editor.testing
        if editor.testing {
            testSpinner.startAnimation(nil)
        } else {
            testSpinner.stopAnimation(nil)
        }
        testResult.stringValue = editor.testText
        testResult.toolTip = editor.testText.isEmpty ? nil : editor.testText
        errorLabel.stringValue = editor.error
        errorLabel.isHidden = editor.error.isEmpty
        saveButton.isEnabled = problems.isEmpty && !editor.saving
        if editor.saving {
            saveSpinner.startAnimation(nil)
        } else {
            saveSpinner.stopAnimation(nil)
        }
    }

    private func show(_ label: NSTextField, _ problems: [String]) {
        let text = problems.joined(separator: "\n")
        if label.stringValue != text {
            label.stringValue = text
        }
        label.isHidden = text.isEmpty
    }

    private func fillAccounts() {
        accountPopup.removeAllItems()
        for a in editor.accounts {
            accountPopup.addItem(withTitle: accountLabel(a))
            accountPopup.lastItem?.representedObject = a.id.rawValue
        }
        accountPopup.addItem(withTitle: Rules.allAccounts)
        accountPopup.lastItem?.representedObject = ""
    }

    private func selectAccount(_ id: AccountID?) {
        let raw = id?.rawValue ?? ""
        if let i = accountPopup.itemArray.firstIndex(where: { ($0.representedObject as? String) == raw }) {
            accountPopup.selectItem(at: i)
        } else if let id {
            // A rule for an account no longer listed (removed, or not a
            // mail account): named as such, kept until the user picks one.
            accountPopup.insertItem(withTitle: Rules.scopeText(id, names: Rules.Names()), at: 0)
            accountPopup.item(at: 0)?.representedObject = raw
            accountPopup.selectItem(at: 0)
        }
    }

    // MARK: Condition rows

    private func clear(_ stack: NSStackView) {
        for v in stack.arrangedSubviews {
            stack.removeArrangedSubview(v)
            v.removeFromSuperview()
        }
    }

    private func buildConditions() {
        clear(conditionRows)
        conditionProblems = []
        for (i, c) in editor.form.conditions.enumerated() {
            let field = NSPopUpButton()
            for f in Rules.fields {
                field.addItem(withTitle: Rules.fieldText(f))
                field.lastItem?.representedObject = f.rawValue
            }
            if let j = Rules.fields.firstIndex(of: c.field) {
                field.selectItem(at: j)
            }
            field.tag = i
            field.target = self
            field.action = #selector(fieldChosen(_:))
            field.setAccessibilityLabel(Rules.fieldText(c.field))

            let ops = Rules.operators(for: c.field)
            let op = NSPopUpButton()
            for o in ops {
                op.addItem(withTitle: Rules.operatorText(c.field, o))
                op.lastItem?.representedObject = o.rawValue
            }
            if let j = ops.firstIndex(of: c.op) {
                op.selectItem(at: j)
            }
            op.isEnabled = ops.count > 1
            op.tag = i
            op.target = self
            op.action = #selector(operatorChosen(_:))

            let value: NSView
            switch Rules.valueKind(of: c.field) {
            case .textField:
                let t = NSTextField()
                t.stringValue = c.value
                t.placeholderString = Rules.valuePlaceholder(c.field, c.op)
                t.lineBreakMode = .byTruncatingTail
                t.tag = i
                t.delegate = self
                t.identifier = NSUserInterfaceItemIdentifier("condition")
                t.setContentHuggingPriority(.defaultLow, for: .horizontal)
                t.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
                value = t
            case .bulk, .bool:
                let p = NSPopUpButton()
                let values = Rules.valueKind(of: c.field) == .bulk ? Rules.bulkValues : [Rules.valueTrue, Rules.valueFalse]
                for v in values {
                    p.addItem(withTitle: Rules.valueText(c.field, v))
                    p.lastItem?.representedObject = v
                }
                if let j = values.firstIndex(of: c.value) {
                    p.selectItem(at: j)
                }
                p.tag = i
                p.target = self
                p.action = #selector(valueChosen(_:))
                value = p
            }

            let remove = Self.symbolButton("minus.circle", tooltip: Rules.removeRow)
            remove.tag = i
            remove.target = self
            remove.action = #selector(removeCondition(_:))

            let row = NSStackView(views: [field, op, value, remove])
            row.orientation = .horizontal
            row.spacing = 6
            row.distribution = .fill
            for v in [field, op, remove] as [NSView] {
                v.setContentHuggingPriority(.required, for: .horizontal)
            }
            let problem = Self.problemLabel()
            conditionProblems.append(problem)
            conditionRows.addArrangedSubview(row)
            prefsFillWidth(row, in: conditionRows)
            conditionRows.addArrangedSubview(problem)
            prefsFillWidth(problem, in: conditionRows)
        }
    }

    // MARK: Action rows

    private func buildActions() {
        clear(actionRows)
        actionProblems = []
        replyViews = [:]
        let f = editor.form
        for (i, a) in f.actions.enumerated() {
            let label = NSTextField(labelWithString: Rules.actionText(a.kind))
            label.setContentHuggingPriority(.required, for: .horizontal)
            var line: [NSView] = [label]
            var below: [NSView] = []
            switch a.kind {
            case .move:
                let p = NSPopUpButton()
                p.autoenablesItems = false
                p.addItem(withTitle: Rules.folderPlaceholder)
                p.lastItem?.representedObject = ""
                for t in editor.moveTargets {
                    p.addItem(withTitle: folderTitle(t.folder))
                    p.lastItem?.representedObject = t.folder.id.rawValue
                    p.lastItem?.indentationLevel = min(max(t.depth, 0), 15)
                }
                if let id = a.folderId, let j = p.itemArray.firstIndex(where: { ($0.representedObject as? String) == id.rawValue }) {
                    p.selectItem(at: j)
                } else {
                    p.selectItem(at: 0)
                }
                p.item(at: 0)?.isEnabled = false
                p.tag = i
                p.target = self
                p.action = #selector(folderChosen(_:))
                line.append(p)
            case .forward:
                let t = NSTextField()
                t.stringValue = a.to
                t.placeholderString = Rules.forwardPlaceholder
                t.tag = i
                t.delegate = self
                t.identifier = NSUserInterfaceItemIdentifier("forward")
                t.setContentHuggingPriority(.defaultLow, for: .horizontal)
                line.append(t)
                below.append(Self.caption(Rules.outgoingHint(a.kind)))
            case .autoReply:
                let subject = NSTextField()
                subject.stringValue = a.subject
                subject.placeholderString = Rules.replySubjectPlaceholder
                subject.tag = i
                subject.delegate = self
                subject.identifier = NSUserInterfaceItemIdentifier("subject")
                subject.setContentHuggingPriority(.defaultLow, for: .horizontal)
                line.append(subject)
                below.append(replyTextView(a.text, row: i))
                below.append(Self.caption(Rules.outgoingHint(a.kind)))
            default:
                let spacer = NSView()
                spacer.setContentHuggingPriority(.defaultLow, for: .horizontal)
                line.append(spacer)
            }
            let remove = Self.symbolButton("minus.circle", tooltip: Rules.removeRow)
            remove.tag = i
            remove.target = self
            remove.action = #selector(removeAction(_:))
            line.append(remove)
            let row = NSStackView(views: line)
            row.orientation = .horizontal
            row.spacing = 6
            actionRows.addArrangedSubview(row)
            prefsFillWidth(row, in: actionRows)
            for v in below {
                actionRows.addArrangedSubview(v)
                prefsFillWidth(v, in: actionRows)
            }
            let problem = Self.problemLabel()
            actionProblems.append(problem)
            actionRows.addArrangedSubview(problem)
            prefsFillWidth(problem, in: actionRows)
        }
    }

    /// The reply's text: a plain text view (never rich), a few lines high,
    /// scrolling beyond them.
    private func replyTextView(_ text: String, row: Int) -> NSView {
        let scroll = NSTextView.scrollableTextView()
        scroll.borderType = .bezelBorder
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.heightAnchor.constraint(equalToConstant: 96).isActive = true
        if let view = scroll.documentView as? NSTextView {
            view.isRichText = false
            view.importsGraphics = false
            view.allowsUndo = true
            view.isAutomaticQuoteSubstitutionEnabled = false
            view.isAutomaticDashSubstitutionEnabled = false
            view.isAutomaticLinkDetectionEnabled = false
            view.isAutomaticDataDetectionEnabled = false
            view.font = Typo.body
            view.string = text
            view.delegate = self
            view.setAccessibilityLabel(Rules.replyTextPlaceholder)
            replyViews[ObjectIdentifier(view)] = row
        }
        return scroll
    }

    /// The reply text views and their action rows.
    private var replyViews: [ObjectIdentifier: Int] = [:]

    private func fillAddAction() {
        addActionButton.removeAllItems()
        addActionButton.addItem(withTitle: Rules.addAction)
        for k in editor.availableActions {
            addActionButton.addItem(withTitle: Rules.actionText(k))
            addActionButton.lastItem?.representedObject = k.rawValue
            addActionButton.lastItem?.target = self
            addActionButton.lastItem?.action = #selector(addAction(_:))
        }
        addActionButton.isEnabled = !editor.availableActions.isEmpty
    }

    // MARK: Try It's folders

    private func fillTestFolders() {
        testFolderPopup.removeAllItems()
        let byAccount = Dictionary(grouping: editor.testFolders, by: \.accountId)
        let several = byAccount.count > 1
        for a in editor.accounts {
            guard let list = byAccount[a.id], !list.isEmpty else { continue }
            if several {
                // The account's name as a heading of its folders.
                testFolderPopup.addItem(withTitle: accountLabel(a))
                testFolderPopup.lastItem?.isEnabled = false
            }
            for folder in list {
                testFolderPopup.addItem(withTitle: folderTitle(folder))
                testFolderPopup.lastItem?.representedObject = FolderKey(account: folder.accountId, folder: folder.id).encoded
                testFolderPopup.lastItem?.indentationLevel = several ? 1 : 0
            }
        }
        testFolderPopup.isEnabled = !editor.testFolders.isEmpty
    }

    private func selectTestFolder(_ k: FolderKey) {
        if let i = testFolderPopup.itemArray.firstIndex(where: { ($0.representedObject as? String) == k.encoded }) {
            testFolderPopup.selectItem(at: i)
        }
    }

    // MARK: Actions

    func controlTextDidChange(_ obj: Foundation.Notification) {
        guard let t = obj.object as? NSTextField else { return }
        if t === nameField {
            editor.setName(t.stringValue)
            return
        }
        switch t.identifier?.rawValue ?? "" {
        case "condition": editor.setValue(t.tag, t.stringValue)
        case "forward": editor.setForwardTo(t.tag, t.stringValue)
        case "subject": editor.setReplySubject(t.tag, t.stringValue)
        default: break
        }
    }

    func textDidChange(_ notification: Foundation.Notification) {
        guard let view = notification.object as? NSTextView, let row = replyViews[ObjectIdentifier(view)] else { return }
        editor.setReplyText(row, view.string)
    }

    @objc private func accountChosen(_ sender: NSPopUpButton) {
        let raw = sender.selectedItem?.representedObject as? String ?? ""
        editor.setAccount(raw.isEmpty ? nil : AccountID(raw))
    }

    @objc private func matchChosen(_ sender: NSPopUpButton) {
        editor.setMatch(RuleMatch(rawValue: sender.selectedItem?.representedObject as? String ?? RuleMatch.all.rawValue))
    }

    @objc private func fieldChosen(_ sender: NSPopUpButton) {
        guard let raw = sender.selectedItem?.representedObject as? String else { return }
        editor.setField(sender.tag, RuleField(rawValue: raw))
    }

    @objc private func operatorChosen(_ sender: NSPopUpButton) {
        guard let raw = sender.selectedItem?.representedObject as? String else { return }
        editor.setOperator(sender.tag, RuleOperator(rawValue: raw))
    }

    @objc private func valueChosen(_ sender: NSPopUpButton) {
        guard let raw = sender.selectedItem?.representedObject as? String else { return }
        editor.setValue(sender.tag, raw)
    }

    @objc private func removeCondition(_ sender: NSButton) {
        builtShape = nil
        editor.removeCondition(sender.tag)
    }

    @objc private func addCondition(_ sender: Any?) {
        editor.addCondition()
    }

    @objc private func addAction(_ sender: NSMenuItem) {
        guard let raw = sender.representedObject as? String else { return }
        editor.addAction(RuleActionKind(rawValue: raw))
    }

    @objc private func removeAction(_ sender: NSButton) {
        builtShape = nil
        editor.removeAction(sender.tag)
    }

    @objc private func folderChosen(_ sender: NSPopUpButton) {
        let raw = sender.selectedItem?.representedObject as? String ?? ""
        editor.setFolder(sender.tag, raw.isEmpty ? nil : FolderID(raw))
    }

    @objc private func stopToggled(_ sender: NSButton) {
        editor.setStop(sender.state == .on)
    }

    @objc private func enabledToggled(_ sender: NSSwitch) {
        editor.setEnabled(sender.state == .on)
    }

    @objc private func testFolderChosen(_ sender: NSPopUpButton) {
        guard let raw = sender.selectedItem?.representedObject as? String, let k = FolderKey(encoded: raw) else { return }
        editor.setTestFolder(k)
    }

    @objc private func testClicked(_ sender: Any?) {
        let editor = editor
        Task { await editor.test() }
    }

    @objc private func cancelClicked(_ sender: Any?) {
        close()
    }

    @objc private func saveClicked(_ sender: Any?) {
        // A text field being edited hands its last characters over first.
        window?.makeFirstResponder(nil)
        guard editor.canSave else { return }
        let editor = editor
        let ask = rules.confirm(on: window)
        Task { @MainActor [weak self] in
            guard let rule = await editor.save(confirm: ask) else { return }
            self?.rules.saved(rule)
            self?.close()
        }
    }
}

/// The editor's scrolling document: flipped, so the form starts at the
/// top.
@MainActor
private final class RuleEditorDocumentView: NSView {
    override var isFlipped: Bool { true }
}
