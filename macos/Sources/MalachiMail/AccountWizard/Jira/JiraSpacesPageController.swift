// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The assistant's last page: the spaces the token sees as check boxes
/// ("KEY – Name" and the estimate of their issues in the offline window,
/// `Jira.spaceRows`), why the account cannot be added yet
/// (`Jira.spacesProblem`), the offline window ("Keep Issues Offline For",
/// `Jira.offlineChoices`), "Only Issues Involving Me", and the account's
/// address when a Data Center site does not reveal it. Add Account stores
/// the account (account.add). The spaces' names come from the site and
/// are shown as plain text.
@MainActor
final class JiraSpacesPageController: NSViewController, NSTableViewDataSource, NSTableViewDelegate, NSTextFieldDelegate,
    JiraWizardPage {
    /// The list's fixed height: about eight spaces, the page scrolls.
    private static let listHeight: CGFloat = 220

    private let wizard: JiraWizardController
    private let banner = WizardBannerView()
    private let table = NSTableView()
    private let tableScroll = NSScrollView()
    private let emptyRow: PreferenceRowView
    private let spacesGroup = PreferencesGroupView()
    private let problemLabel = PrefsWrappingLabel("", size: 11, color: .secondaryLabelColor)
    private let offlinePopup = NSPopUpButton(frame: .zero, pullsDown: false)
    private let onlyMineSwitch = NSSwitch()
    private let emailEntry = WizardEntryField()
    private let optionsGroup = PreferencesGroupView()
    private let emailRow: PreferenceRowView
    private let addButton = NSButton(title: "", target: nil, action: nil)
    private let progress = JiraWizardProgress()
    let cancelButton = WizardCancelButton()
    private let pageView = WizardPageView()

    private var rows: [Jira.SpaceRow] = []
    private var selected: Set<String> = []
    private var problem = ""
    private var busy = false
    /// The address row is shown (`showEmail`), kept for `loadView`: the
    /// list arrives before the page is first shown.
    private var emailShown = false

    init(wizard: JiraWizardController) {
        self.wizard = wizard
        emptyRow = PreferenceRowView(title: wizard.texts.noSpaces)
        emailRow = PreferenceRowView(title: L10n.T("E-mail Address"), trailing: emailEntry, trailingFills: true)
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    var initialFirstResponder: NSView? { emailShown ? emailEntry.field : nil }

    override func loadView() {
        let texts = wizard.texts
        let description = PrefsWrappingLabel(texts.spacesDescription, color: .secondaryLabelColor)

        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("space"))
        column.resizingMask = .autoresizingMask
        table.addTableColumn(column)
        table.headerView = nil
        table.style = .plain
        table.rowHeight = 28
        table.intercellSpacing = NSSize(width: 0, height: 0)
        table.selectionHighlightStyle = .none
        table.focusRingType = .none
        table.backgroundColor = .clear
        table.columnAutoresizingStyle = .uniformColumnAutoresizingStyle
        table.dataSource = self
        table.delegate = self
        tableScroll.documentView = table
        tableScroll.hasVerticalScroller = true
        tableScroll.hasHorizontalScroller = false
        tableScroll.autohidesScrollers = true
        tableScroll.drawsBackground = false
        tableScroll.borderType = .noBorder
        tableScroll.translatesAutoresizingMaskIntoConstraints = false
        tableScroll.heightAnchor.constraint(equalToConstant: Self.listHeight).isActive = true
        emptyRow.isEnabled = false
        spacesGroup.setRows([tableScroll, emptyRow])

        offlinePopup.addItems(withTitles: wizard.offlineLabels)
        offlinePopup.selectItem(at: wizard.offlineIndex)
        offlinePopup.target = self
        offlinePopup.action = #selector(offlineChanged(_:))
        onlyMineSwitch.state = wizard.onlyMine ? .on : .off
        onlyMineSwitch.target = self
        onlyMineSwitch.action = #selector(onlyMineChanged(_:))
        emailEntry.field.delegate = self
        optionsGroup.setRows([
            PreferenceRowView(title: texts.keepOffline, subtitle: texts.keepOfflineSubtitle, trailing: offlinePopup),
            PreferenceRowView(title: texts.onlyMine, subtitle: texts.onlyMineSubtitle, trailing: onlyMineSwitch),
            emailRow,
        ])
        optionsGroup.setRow(emailRow, hidden: !emailShown)

        let content = prefsColumn(spacing: 12, insets: NSEdgeInsets(top: 24, left: 24, bottom: 24, right: 24))
        prefsAddFilling(description, to: content)
        prefsAddFilling(spacesGroup, to: content)
        prefsAddFilling(problemLabel, to: content)
        content.setCustomSpacing(24, after: problemLabel)
        prefsAddFilling(optionsGroup, to: content)
        let scroll = prefsScrollView(clampMaximum: 480, content: content)

        addButton.title = wizard.nextLabel(.spaces)
        addButton.target = self
        addButton.action = #selector(addClicked(_:))
        jiraWizardLayout(pageView, banner: banner, content: scroll, button: addButton, progress: progress, cancel: cancelButton)
        pageView.returnHandler = { [weak self] field in
            guard let self, field === self.emailEntry.field else { return false }
            self.sync()
            self.wizard.next()
            return true
        }
        view = pageView
        applyRows()
        applyEnabled()
    }

    // MARK: Inputs from the controller

    func show(rows: [Jira.SpaceRow], selected: Set<String>) {
        self.rows = rows
        self.selected = selected
        applyRows()
    }

    func showProblem(_ text: String) {
        problem = text
        guard isViewLoaded else { return }
        problemLabel.stringValue = text
        problemLabel.isHidden = text.isEmpty || rows.isEmpty
        applyEnabled()
    }

    func showEmail(shown: Bool, email: String) {
        emailShown = shown
        if emailEntry.field.stringValue != email {
            emailEntry.field.stringValue = email
        }
        optionsGroup.setRow(emailRow, hidden: !shown)
    }

    func setBusy(_ progressText: String?) {
        busy = progressText != nil
        progress.show(progressText)
        applyEnabled()
        // The check boxes follow.
        table.reloadData()
    }

    func showBanner(_ text: String?) {
        banner.reveal(text)
    }

    func showProblems(_ fields: Set<JiraWizardController.Field>) {
        emailEntry.hasError = fields.contains(.email)
    }

    func view(for field: JiraWizardController.Field) -> NSView? {
        field == .email ? emailEntry.field : nil
    }

    private func applyRows() {
        guard isViewLoaded else { return }
        table.reloadData()
        spacesGroup.setRow(tableScroll, hidden: rows.isEmpty)
        spacesGroup.setRow(emptyRow, hidden: !rows.isEmpty)
        showProblem(problem)
    }

    private func applyEnabled() {
        guard isViewLoaded else { return }
        offlinePopup.isEnabled = !busy
        onlyMineSwitch.isEnabled = !busy
        emailEntry.isEnabled = !busy
        addButton.isEnabled = !busy && problem.isEmpty && !rows.isEmpty
    }

    // MARK: Outputs to the controller

    private func sync() {
        wizard.setEmail(emailEntry.field.stringValue)
    }

    @objc private func addClicked(_ sender: Any?) {
        sync()
        wizard.next()
    }

    @objc private func offlineChanged(_ sender: NSPopUpButton) {
        wizard.setOfflineIndex(sender.indexOfSelectedItem)
    }

    @objc private func onlyMineChanged(_ sender: NSSwitch) {
        wizard.setOnlyMine(sender.state == .on)
    }

    private func toggle(_ id: String, _ on: Bool) {
        if on {
            selected.insert(id)
        } else {
            selected.remove(id)
        }
        wizard.setSpace(id, selected: on)
    }

    // MARK: NSTableViewDataSource, NSTableViewDelegate

    func numberOfRows(in tableView: NSTableView) -> Int {
        rows.count
    }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        guard row < rows.count else { return nil }
        let r = rows[row]
        let cell = tableView.makeView(withIdentifier: JiraSpaceCellView.reuseIdentifier, owner: nil) as? JiraSpaceCellView
            ?? JiraSpaceCellView()
        cell.apply(r, selected: selected.contains(r.id), enabled: !busy)
        let id = r.id
        cell.onToggle = { [weak self] on in self?.toggle(id, on) }
        return cell
    }

    func tableView(_ tableView: NSTableView, shouldSelectRow row: Int) -> Bool {
        false
    }

    // MARK: NSTextFieldDelegate

    func controlTextDidChange(_ obj: Foundation.Notification) {
        sync()
    }

    func control(_ control: NSControl, textView: NSTextView, doCommandBy commandSelector: Selector) -> Bool {
        if commandSelector == #selector(NSResponder.insertNewline(_:)), control === emailEntry.field {
            sync()
            wizard.next()
            return true
        }
        return false
    }
}

/// One space of the list: its check box titled "KEY – Name" and the
/// estimate of its issues at the end. The title is the site's text
/// (cleaned by `Jira.spaceTitle`), plain.
@MainActor
private final class JiraSpaceCellView: NSTableCellView {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("JiraSpaceCell")

    private let check = NSButton(checkboxWithTitle: "", target: nil, action: nil)
    private let count = NSTextField(labelWithString: "")
    var onToggle: ((Bool) -> Void)?

    init() {
        super.init(frame: .zero)
        identifier = JiraSpaceCellView.reuseIdentifier
        check.lineBreakMode = .byTruncatingTail
        check.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        check.target = self
        check.action = #selector(toggled(_:))
        count.font = .monospacedDigitSystemFont(ofSize: 11, weight: .regular)
        count.textColor = .secondaryLabelColor
        count.alignment = .right
        count.setContentHuggingPriority(.required, for: .horizontal)
        count.setContentCompressionResistancePriority(.required, for: .horizontal)
        for v in [check, count] as [NSView] {
            v.translatesAutoresizingMaskIntoConstraints = false
            addSubview(v)
        }
        NSLayoutConstraint.activate([
            check.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 12),
            check.centerYAnchor.constraint(equalTo: centerYAnchor),
            count.leadingAnchor.constraint(greaterThanOrEqualTo: check.trailingAnchor, constant: 8),
            count.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -12),
            count.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    func apply(_ row: Jira.SpaceRow, selected: Bool, enabled: Bool) {
        check.title = row.title
        check.state = selected ? .on : .off
        check.isEnabled = enabled
        count.stringValue = row.count
        count.isHidden = row.count.isEmpty
        toolTip = row.title
    }

    override func prepareForReuse() {
        super.prepareForReuse()
        onToggle = nil
    }

    @objc private func toggled(_ sender: Any?) {
        onToggle?(check.state == .on)
    }
}
