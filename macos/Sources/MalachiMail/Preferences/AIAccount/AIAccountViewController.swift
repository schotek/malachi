// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The page of the AI provider account dialog (ui/data/ui/ai_account.blp,
/// ui/internal/aiaccount/dialog.go): one scrolling page under a header
/// with the title, the banner of a failed call and the plain-http warning
/// under it, Cancel and Add Account / Save below. Its sections are the
/// account (the kind, read only; the name; the server address of an
/// OpenAI-compatible server with the examples under it), the key (the
/// secure field, or "Key stored" with Replace Key… for an existing
/// account), the pinned certificate and Test with its outcome, the models
/// (typed, or picked from what Test listed: an editable combo box), and
/// the consent (a switch row under the consent's heading). The texts, the
/// rules and the calls live in `AIAccountController`; this shows what it
/// holds (`refresh`, on every `onChange`) and feeds back what the user
/// did. Everything the provider answers (the model ids) is plain text.
/// The models, the consent and Save wait for a successful test
/// (`AIAccountController.unlocked`).
@MainActor
final class AIAccountViewController: NSViewController, NSTextFieldDelegate, NSComboBoxDelegate {
    static let size = NSSize(width: 600, height: 720)

    let controller: AIAccountController
    private let confirmTrust: WizardConfirmTrust
    /// Cancel or Escape.
    var onCancel: (() -> Void)?

    private let titleLabel = NSTextField(labelWithString: "")
    private let banner = PrefsBannerView()
    /// Plain http to a server outside this computer (docs/plans/ai-api.md D8).
    private let httpBanner = PrefsBannerView()
    private let pageView = WizardPageView()
    private let progress = JiraWizardProgress()
    private let cancelButton = WizardCancelButton()
    private let saveButton = NSButton(title: "", target: nil, action: nil)
    private let content = prefsColumn(spacing: 24, insets: NSEdgeInsets(top: 24, left: 24, bottom: 24, right: 24))

    // The account.
    private let providerValue = NSTextField(labelWithString: "")
    private let nameEntry = WizardEntryField()
    private let addressEntry = WizardEntryField()
    private let addressHint = PrefsWrappingLabel("", size: 11, color: .secondaryLabelColor)
    private var addressRow: PreferenceRowView!
    private let accountGroup = PreferencesGroupView()

    // The key, the pin and Test.
    private let replaceKeyButton = NSButton(title: "", target: nil, action: nil)
    private let keyEntry = WizardEntryField(secure: true)
    private let keepKeyButton = NSButton(title: "", target: nil, action: nil)
    private let forgetPinButton = NSButton(title: "", target: nil, action: nil)
    private var keyStoredRow: PreferenceRowView!
    private var keyRow: PreferenceRowView!
    private var pinRow: PreferenceRowView!
    private let testRow = AIAccountTestRow()
    private let keyGroup = PreferencesGroupView()

    // The models.
    private let modelCombo = NSComboBox()
    private let boardModelCombo = NSComboBox()
    private var modelRow: PreferenceRowView!
    private var boardModelRow: PreferenceRowView!
    private let modelGroup: PreferencesGroupView
    /// The models the combo boxes list.
    private var shownModels: [AIModel] = []

    // The consent (D7, Q17).
    private let consentSwitch = NSSwitch()
    private var consentRow: PreferenceRowView!
    private let consentGroup: PreferencesGroupView

    /// The field flagged, `.none` for none.
    private var flagged: AIAccount.Field = .none
    /// The widgets are being set from the controller: their actions are
    /// not the user's.
    private var applying = false

    init(controller: AIAccountController, confirmTrust: @escaping WizardConfirmTrust) {
        self.controller = controller
        self.confirmTrust = confirmTrust
        modelGroup = PreferencesGroupView(description: controller.texts.modelHint)
        consentGroup = PreferencesGroupView(title: controller.consent.heading)
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The account's name: what the page edits first.
    var initialFirstResponder: NSView? { nameEntry.field }

    // MARK: Building

    override func loadView() {
        let root = NSView(frame: NSRect(origin: .zero, size: Self.size))
        preferredContentSize = Self.size

        let header = NSView()
        header.translatesAutoresizingMaskIntoConstraints = false
        titleLabel.stringValue = controller.title
        titleLabel.font = .systemFont(ofSize: 13, weight: .bold)
        titleLabel.alignment = .center
        titleLabel.lineBreakMode = .byTruncatingTail
        titleLabel.translatesAutoresizingMaskIntoConstraints = false
        header.addSubview(titleLabel)
        let divider = PrefsDivider()
        header.addSubview(divider)

        buildAccount()
        buildKey()
        buildModels()
        buildConsent()
        prefsAddFilling(accountGroup, to: content)
        prefsAddFilling(keyGroup, to: content)
        prefsAddFilling(modelGroup, to: content)
        prefsAddFilling(consentGroup, to: content)
        let scroll = prefsScrollView(clampMaximum: 560, content: content)

        saveButton.title = wizardLabel(controller.saveLabel)
        saveButton.target = self
        saveButton.action = #selector(saveClicked(_:))
        layoutPage(scroll)
        cancelButton.onCancel = { [weak self] in self?.onCancel?() }
        pageView.translatesAutoresizingMaskIntoConstraints = false

        root.addSubview(header)
        root.addSubview(pageView)
        NSLayoutConstraint.activate([
            header.topAnchor.constraint(equalTo: root.topAnchor),
            header.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            header.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            header.heightAnchor.constraint(equalToConstant: WizardRootViewController.headerHeight),
            titleLabel.centerXAnchor.constraint(equalTo: header.centerXAnchor),
            titleLabel.centerYAnchor.constraint(equalTo: header.centerYAnchor),
            titleLabel.leadingAnchor.constraint(greaterThanOrEqualTo: header.leadingAnchor, constant: 44),
            titleLabel.trailingAnchor.constraint(lessThanOrEqualTo: header.trailingAnchor, constant: -44),
            divider.leadingAnchor.constraint(equalTo: header.leadingAnchor),
            divider.trailingAnchor.constraint(equalTo: header.trailingAnchor),
            divider.bottomAnchor.constraint(equalTo: header.bottomAnchor),
            pageView.topAnchor.constraint(equalTo: header.bottomAnchor),
            pageView.leadingAnchor.constraint(equalTo: root.leadingAnchor),
            pageView.trailingAnchor.constraint(equalTo: root.trailingAnchor),
            pageView.bottomAnchor.constraint(equalTo: root.bottomAnchor),
        ])
        view = root
        refresh()
        showBusy(controller.progress)
        banner.reveal(controller.banner)
    }

    override func viewDidAppear() {
        super.viewDidAppear()
        view.window?.recalculateKeyViewLoop()
    }

    /// Lays the page out in `pageView` as the account sheets are: the
    /// banners at the top, the sections filling the middle, the button bar
    /// (Cancel, the progress, Add Account / Save) at the bottom.
    private func layoutPage(_ sections: NSView) {
        saveButton.bezelStyle = .rounded
        saveButton.controlSize = .large
        saveButton.keyEquivalent = "\r"
        let bar = wizardButtonBar([saveButton], cancel: cancelButton)
        bar.insertView(progress, at: 0, in: .trailing)
        sections.setContentHuggingPriority(.defaultLow, for: .vertical)
        let stack = prefsColumn(spacing: 0)
        prefsAddFilling(banner, to: stack)
        prefsAddFilling(httpBanner, to: stack)
        prefsAddFilling(sections, to: stack)
        prefsAddFilling(bar, to: stack)
        pageView.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.topAnchor.constraint(equalTo: pageView.topAnchor),
            stack.bottomAnchor.constraint(equalTo: pageView.bottomAnchor),
            stack.leadingAnchor.constraint(equalTo: pageView.leadingAnchor),
            stack.trailingAnchor.constraint(equalTo: pageView.trailingAnchor),
        ])
    }

    /// A label at the end of a row: a value, plain and selectable, which
    /// holds its width against the row's pull but gives way before the row
    /// would grow.
    private func styleValue(_ label: NSTextField) {
        label.font = .systemFont(ofSize: 13)
        label.textColor = .secondaryLabelColor
        label.lineBreakMode = .byTruncatingMiddle
        label.isSelectable = true
        label.alignment = .right
        label.setContentCompressionResistancePriority(.defaultHigh, for: .horizontal)
        label.widthAnchor.constraint(lessThanOrEqualToConstant: 260).isActive = true
    }

    private func buildAccount() {
        let c = controller
        let t = c.texts
        styleValue(providerValue)
        providerValue.stringValue = c.providerName
        nameEntry.field.delegate = self
        nameEntry.field.stringValue = c.form.name
        addressEntry.field.delegate = self
        addressEntry.field.stringValue = c.form.baseURL
        addressHint.stringValue = t.serverHint
        addressHint.isSelectable = true
        // The hint belongs to the address field: a row of its own under
        // it, as the label under the GTK row.
        let hintRow = NSView()
        hintRow.translatesAutoresizingMaskIntoConstraints = false
        hintRow.addSubview(addressHint)
        addressHint.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            addressHint.leadingAnchor.constraint(equalTo: hintRow.leadingAnchor, constant: PreferenceRowView.inset),
            addressHint.trailingAnchor.constraint(equalTo: hintRow.trailingAnchor, constant: -PreferenceRowView.inset),
            addressHint.topAnchor.constraint(equalTo: hintRow.topAnchor, constant: PreferenceRowView.verticalInset),
            addressHint.bottomAnchor.constraint(equalTo: hintRow.bottomAnchor, constant: -PreferenceRowView.verticalInset),
        ])
        addressRow = PreferenceRowView(title: t.serverLabel, trailing: addressEntry, trailingFills: true)
        var rows: [NSView] = [
            PreferenceRowView(title: t.providerLabel, trailing: providerValue),
            PreferenceRowView(title: t.nameLabel, trailing: nameEntry, trailingFills: true),
        ]
        if c.usesServer {
            rows += [addressRow, hintRow]
        }
        accountGroup.setRows(rows)
    }

    private func buildKey() {
        let c = controller
        let t = c.texts
        replaceKeyButton.title = t.replaceKey
        replaceKeyButton.bezelStyle = .rounded
        replaceKeyButton.target = self
        replaceKeyButton.action = #selector(replaceKeyClicked(_:))
        keyStoredRow = PreferenceRowView(title: t.keyStored, trailing: replaceKeyButton)
        keyEntry.field.delegate = self
        keepKeyButton.title = t.keepKey
        keepKeyButton.bezelStyle = .rounded
        keepKeyButton.target = self
        keepKeyButton.action = #selector(keepKeyClicked(_:))
        keepKeyButton.setContentHuggingPriority(.required, for: .horizontal)
        keepKeyButton.setContentCompressionResistancePriority(.required, for: .horizontal)
        keepKeyButton.isHidden = true
        keyEntry.setContentHuggingPriority(.defaultLow, for: .horizontal)
        let keyControls = NSStackView(views: [keyEntry, keepKeyButton])
        keyControls.orientation = .horizontal
        keyControls.alignment = .centerY
        keyControls.spacing = 8
        keyControls.detachesHiddenViews = true
        keyRow = PreferenceRowView(title: c.keyLabel, trailing: keyControls, trailingFills: true)
        forgetPinButton.title = t.pinForget
        forgetPinButton.bezelStyle = .rounded
        forgetPinButton.target = self
        forgetPinButton.action = #selector(forgetPinClicked(_:))
        pinRow = PreferenceRowView(title: t.pinLabel, subtitle: "", trailing: forgetPinButton)
        pinRow.setSubtitleSelectable()
        testRow.testButton.title = wizardLabel(t.test)
        testRow.testButton.toolTip = t.testing
        testRow.testButton.target = self
        testRow.testButton.action = #selector(testClicked(_:))
        // TRANSLATORS: button of the AI provider dialog after a test refused an https server's certificate the user may trust.
        testRow.trustButton.title = L10n.T("Trust Certificate…")
        testRow.trustButton.target = self
        testRow.trustButton.action = #selector(trustClicked(_:))
        keyGroup.setRows([keyStoredRow, keyRow, pinRow, testRow])
    }

    private func buildModels() {
        let t = controller.texts
        for combo in [modelCombo, boardModelCombo] {
            combo.usesDataSource = false
            combo.completes = false
            combo.numberOfVisibleItems = 12
            combo.font = .systemFont(ofSize: 13)
            combo.delegate = self
            combo.translatesAutoresizingMaskIntoConstraints = false
        }
        modelCombo.stringValue = controller.form.model
        boardModelCombo.stringValue = controller.form.boardModel
        modelRow = PreferenceRowView(title: t.modelLabel, trailing: modelCombo, trailingFills: true)
        boardModelRow = PreferenceRowView(title: t.boardModelLabel, trailing: boardModelCombo, trailingFills: true)
        boardModelRow.toolTip = t.boardModelHint
        modelGroup.setRows([modelRow, boardModelRow])
        fillModels([])
    }

    private func buildConsent() {
        consentSwitch.target = self
        consentSwitch.action = #selector(consentChanged(_:))
        consentRow = PreferenceRowView(title: controller.consent.checkbox, subtitle: controller.consentBody, trailing: consentSwitch)
        consentGroup.setRows([consentRow])
    }

    /// Replaces the models the combo boxes offer: the ids (cleaned as any
    /// text of the provider's); what was typed stays.
    private func fillModels(_ models: [AIModel]) {
        shownModels = models
        let ids = models.map { Jira.clean($0.id) }.filter { !$0.isEmpty }
        for combo in [modelCombo, boardModelCombo] {
            let typed = combo.stringValue
            combo.removeAllItems()
            combo.addItems(withObjectValues: ids)
            if combo.stringValue != typed {
                combo.stringValue = typed
            }
            combo.isButtonBordered = true
        }
    }

    // MARK: Inputs from the controller

    /// Shows what the controller holds now.
    func refresh() {
        guard isViewLoaded else { return }
        let was = applying
        applying = true
        defer { applying = was }
        let c = controller
        let form = c.form
        let editable = !c.saving

        accountGroup.isEnabled = editable
        setText(nameEntry.field, form.name)
        if c.usesServer {
            setText(addressEntry.field, form.baseURL)
        }
        let warning = c.httpWarningText
        httpBanner.reveal(warning.isEmpty ? nil : warning)

        let stored = c.keyStored
        keyGroup.setRow(keyStoredRow, hidden: !stored)
        keyGroup.setRow(keyRow, hidden: stored)
        keyRow.title = c.keyLabel
        setText(keyEntry.field, form.key)
        keepKeyButton.isHidden = !(c.isEditing && !stored)
        keyGroup.setRow(pinRow, hidden: !c.pinned)
        pinRow.subtitle = c.pinText
        keyGroup.isEnabled = editable

        var text = ""
        if let outcome = c.test {
            text = outcome.text
        } else if c.busy {
            text = c.progress ?? ""
        }
        testRow.result = text
        testRow.trustButton.isHidden = c.trustOffer == nil
        testRow.testButton.isEnabled = c.canTest

        if c.models != shownModels {
            fillModels(c.models)
        }
        setText(modelCombo, form.model)
        setText(boardModelCombo, form.boardModel)
        // The models, the consent and Save wait for a successful test
        // (`AIAccountController.connected`).
        modelGroup.isEnabled = c.unlocked

        consentGroup.isHidden = !c.needsConsent
        consentRow.subtitle = c.consentBody
        consentSwitch.state = form.consent ? .on : .off
        consentGroup.isEnabled = c.unlocked

        saveButton.isEnabled = c.canSave
    }

    /// Sets a field from the controller unless the user is typing in it.
    private func setText(_ field: NSTextField, _ value: String) {
        guard field.stringValue != value, field.currentEditor() == nil else { return }
        field.stringValue = value
    }

    /// A call runs (its progress text) or none (nil).
    func showBusy(_ text: String?) {
        guard isViewLoaded else { return }
        progress.show(text)
        refresh()
    }

    /// The page's banner; nil hides it.
    func showBanner(_ text: String?) {
        guard isViewLoaded else { return }
        banner.reveal(text)
    }

    /// Marks the field the controller flagged and gives it the focus;
    /// `.none` clears the mark.
    func flag(_ field: AIAccount.Field) {
        guard isViewLoaded else { return }
        nameEntry.hasError = false
        addressEntry.hasError = false
        keyEntry.hasError = false
        modelCombo.textColor = .labelColor
        boardModelCombo.textColor = .labelColor
        flagged = field
        let focus: NSView?
        switch field {
        case .name:
            nameEntry.hasError = true
            focus = nameEntry.field
        case .server:
            addressEntry.hasError = true
            focus = addressEntry.field
        case .key:
            keyEntry.hasError = true
            focus = keyEntry.field
        case .model:
            modelCombo.textColor = .systemRed
            focus = modelCombo
        case .boardModel:
            boardModelCombo.textColor = .systemRed
            focus = boardModelCombo
        case .consent:
            focus = consentSwitch
        case .none:
            focus = nil
        }
        guard let focus else { return }
        focus.scrollToVisible(focus.bounds)
        view.window?.makeFirstResponder(focus)
    }

    /// Replace Key… of an existing account, from the route that says the
    /// provider refused the key.
    func replaceKey() {
        controller.replaceKey()
        keyEntry.field.scrollToVisible(keyEntry.field.bounds)
        view.window?.makeFirstResponder(keyEntry.field)
    }

    // MARK: Outputs to the controller

    /// Save: the fields as typed, then account.add or account.update
    /// (dialog.go `onSave`).
    @objc private func saveClicked(_ sender: Any?) {
        syncTyped()
        controller.save()
    }

    /// Test: the fields as typed, then account.test.
    @objc private func testClicked(_ sender: Any?) {
        syncTyped()
        controller.runTest()
    }

    /// What is in the fields, in case a change did not report yet (the
    /// combo box's selection, a field still editing).
    private func syncTyped() {
        let c = controller
        c.setName(nameEntry.field.stringValue)
        if c.usesServer {
            c.setBaseURL(addressEntry.field.stringValue)
        }
        if !c.keyStored {
            c.setKey(keyEntry.field.stringValue)
        }
        c.setModel(modelCombo.stringValue)
        c.setBoardModel(boardModelCombo.stringValue)
    }

    @objc private func replaceKeyClicked(_ sender: Any?) {
        replaceKey()
    }

    @objc private func keepKeyClicked(_ sender: Any?) {
        controller.keepKey()
    }

    @objc private func forgetPinClicked(_ sender: Any?) {
        controller.forgetPin()
    }

    @objc private func consentChanged(_ sender: Any?) {
        guard !applying else { return }
        controller.setConsent(consentSwitch.state == .on)
    }

    /// Trust Certificate…: the account assistant's confirmation with the
    /// certificate's details, then the pin and the test again (dialog.go
    /// `onTrust`).
    @objc private func trustClicked(_ sender: Any?) {
        guard let p = controller.trustOffer, let cert = p.cert else { return }
        let changed = p.category == .changed
        let prompt = trustPrompt(cert, servers: [controller.host], changed: changed, previous: changed ? p.expected : "")
        let confirm = confirmTrust
        let window = view.window
        Task { @MainActor [weak self] in
            let ok = await confirm(window, prompt)
            guard ok, let self, !self.controller.closed else { return }
            self.controller.trust()
            self.controller.runTest()
        }
    }

    override func cancelOperation(_ sender: Any?) {
        onCancel?()
    }

    // MARK: NSTextFieldDelegate, NSComboBoxDelegate

    func controlTextDidChange(_ obj: Foundation.Notification) {
        guard !applying, let field = obj.object as? NSTextField else { return }
        if field === nameEntry.field {
            controller.setName(field.stringValue)
        } else if field === addressEntry.field {
            controller.setBaseURL(field.stringValue)
        } else if field === keyEntry.field {
            controller.setKey(field.stringValue)
        } else if field === modelCombo {
            controller.setModel(field.stringValue)
        } else if field === boardModelCombo {
            controller.setBoardModel(field.stringValue)
        }
    }

    /// A model picked from the list goes into the field (the combo box
    /// shows it after this notification).
    func comboBoxSelectionDidChange(_ notification: Foundation.Notification) {
        guard !applying, let combo = notification.object as? NSComboBox,
              let picked = combo.objectValueOfSelectedItem as? String else { return }
        if combo === modelCombo {
            controller.setModel(picked)
        } else if combo === boardModelCombo {
            controller.setBoardModel(picked)
        }
    }
}

/// The Test row of the key group: the outcome of the last test (or the
/// progress of the one under way) at the start, Trust Certificate… when
/// the test refused an https certificate the user may trust, and Test at
/// the end.
@MainActor
final class AIAccountTestRow: NSView, PrefsGroupMember {
    private let label = PrefsWrappingLabel("", size: 11)
    let trustButton = NSButton(title: "", target: nil, action: nil)
    let testButton = NSButton(title: "", target: nil, action: nil)
    private var rowEnabled = true
    private var groupEnabled = true

    var result: String {
        get { label.stringValue }
        set { label.stringValue = newValue }
    }

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        trustButton.bezelStyle = .rounded
        trustButton.isHidden = true
        testButton.bezelStyle = .rounded
        for b in [trustButton, testButton] {
            b.setContentHuggingPriority(.required, for: .horizontal)
            b.setContentCompressionResistancePriority(.required, for: .horizontal)
        }
        let buttons = NSStackView(views: [trustButton, testButton])
        buttons.orientation = .horizontal
        buttons.alignment = .centerY
        buttons.spacing = 8
        buttons.detachesHiddenViews = true
        buttons.setHuggingPriority(.required, for: .horizontal)
        buttons.translatesAutoresizingMaskIntoConstraints = false
        label.translatesAutoresizingMaskIntoConstraints = false
        addSubview(label)
        addSubview(buttons)
        let inset = PreferenceRowView.inset
        let vInset = PreferenceRowView.verticalInset
        NSLayoutConstraint.activate([
            label.leadingAnchor.constraint(equalTo: leadingAnchor, constant: inset),
            label.centerYAnchor.constraint(equalTo: centerYAnchor),
            label.topAnchor.constraint(greaterThanOrEqualTo: topAnchor, constant: vInset),
            label.bottomAnchor.constraint(lessThanOrEqualTo: bottomAnchor, constant: -vInset),
            label.trailingAnchor.constraint(equalTo: buttons.leadingAnchor, constant: -inset),
            buttons.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -inset),
            buttons.centerYAnchor.constraint(equalTo: centerYAnchor),
            buttons.topAnchor.constraint(greaterThanOrEqualTo: topAnchor, constant: vInset),
            buttons.bottomAnchor.constraint(lessThanOrEqualTo: bottomAnchor, constant: -vInset),
            heightAnchor.constraint(greaterThanOrEqualToConstant: PreferenceRowView.minHeight),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func setGroupEnabled(_ enabled: Bool) {
        groupEnabled = enabled
        applyEnabled()
    }

    /// The group's sensitivity dims the row; Test's own state is the
    /// controller's (`canTest`, set by the page's `refresh`, which follows
    /// every change of the group).
    private func applyEnabled() {
        let on = rowEnabled && groupEnabled
        trustButton.isEnabled = on
        if !on {
            testButton.isEnabled = false
        }
        label.textColor = on ? .labelColor : .tertiaryLabelColor
    }
}
