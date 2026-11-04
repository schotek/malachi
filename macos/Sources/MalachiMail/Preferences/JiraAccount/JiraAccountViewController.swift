// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The settings of a Jira account: one scrolling page under a header with
/// the title, and Cancel and Save below it. Its sections are the site
/// (read only, with Replace Token…), the spaces, the synchronisation, the
/// folders with the closed statuses, the notification e-mails and the
/// comments posted by bots. The texts, the rules and the calls live in
/// `JiraAccountController`; this shows what it holds (`refresh`, on every
/// `onChange`) and feeds back what the user did. Everything from the site
/// (its address, the user, the names of spaces and statuses) and every
/// entry of the lists is shown as plain text.
@MainActor
final class JiraAccountViewController: NSViewController, NSTableViewDataSource, NSTableViewDelegate, NSTextFieldDelegate {
    static let size = NSSize(width: 600, height: 680)
    /// A row of the spaces, and how many of them show without scrolling.
    private static let spaceRowHeight: CGFloat = 28
    private static let spaceRowsShown = 7

    let controller: JiraAccountController
    /// Cancel or Escape.
    var onCancel: (() -> Void)?

    private let titleLabel = NSTextField(labelWithString: "")
    private let banner = JiraAccountBannerView()
    private let pageView = WizardPageView()
    private let progress = JiraWizardProgress()
    private let cancelButton = WizardCancelButton()
    private let saveButton = NSButton(title: "", target: nil, action: nil)

    // The site.
    private let deploymentLabel = NSTextField(labelWithString: "")
    private let userLabel = NSTextField(labelWithString: "")
    private let nameEntry = WizardEntryField()
    private let tokenButton = NSButton(title: "", target: nil, action: nil)
    private let addressRow: PreferenceRowView
    private let userRow: PreferenceRowView
    private let tokenRow: PreferenceRowView
    private let siteGroup: PreferencesGroupView

    // The spaces.
    private let spacesTable = NSTableView()
    private let spacesScroll = NSScrollView()
    private var spacesHeight: NSLayoutConstraint?
    private let noSpacesRow: PreferenceRowView
    private let spacesGroup: PreferencesGroupView
    private let spacesProblem = PrefsWrappingLabel("", size: 11, color: .systemRed)
    /// The page's column, for the space between the spaces and what
    /// follows them.
    private let content = prefsColumn(spacing: 24, insets: NSEdgeInsets(top: 24, left: 24, bottom: 24, right: 24))
    private var spaceRows: [Jira.SpaceRow] = []
    private var selectedSpaces: Set<String> = []

    // The synchronisation and the folders.
    private let offlinePopup = NSPopUpButton(frame: .zero, pullsDown: false)
    private let onlyMineSwitch = NSSwitch()
    private let showEventsSwitch = NSSwitch()
    private let syncGroup: PreferencesGroupView
    private var folderSwitches: [(view: VirtualFolder, toggle: NSSwitch)] = []
    private let statusPicker: JiraStatusPickerView
    private let foldersGroup: PreferencesGroupView

    // The notification e-mails and the bots.
    private let modePopup = NSPopUpButton(frame: .zero, pullsDown: false)
    private let modeRow: PreferenceRowView
    private let notificationGroup: PreferencesGroupView
    private let botsGroup: PreferencesGroupView
    private let editors: [JiraListEditorView]

    init(controller: JiraAccountController) {
        self.controller = controller
        let t = controller.texts
        addressRow = PreferenceRowView(title: t.siteAddress, trailing: deploymentLabel)
        userRow = PreferenceRowView(title: t.signedInAs, trailing: userLabel)
        tokenRow = PreferenceRowView(title: controller.site.tokenLabel, trailing: tokenButton)
        siteGroup = PreferencesGroupView(title: t.siteTitle)
        noSpacesRow = PreferenceRowView(title: t.noSpaces)
        spacesGroup = PreferencesGroupView(title: t.spacesTitle, description: t.spacesDescription)
        syncGroup = PreferencesGroupView(title: t.syncTitle)
        statusPicker = JiraStatusPickerView(title: t.closedStatuses, subtitle: t.closedStatusesSubtitle)
        foldersGroup = PreferencesGroupView(title: t.foldersTitle)
        modeRow = PreferenceRowView(title: t.notificationMode, trailing: modePopup)
        notificationGroup = PreferencesGroupView(title: t.notificationTitle)
        botsGroup = PreferencesGroupView(title: t.botsTitle)
        func editor(_ kind: Jira.ListKind, _ title: String, _ subtitle: String) -> JiraListEditorView {
            JiraListEditorView(kind: kind, title: title, subtitle: subtitle, addLabel: t.add, removeLabel: t.remove)
        }
        editors = [
            editor(.senders, t.senders, t.sendersSubtitle),
            editor(.botNames, t.botNames, t.botNamesSubtitle),
            editor(.metadataFilters, t.hiddenLines, t.hiddenLinesSubtitle),
            editor(.authorPrefixes, t.namePrefixes, t.namePrefixesSubtitle),
        ]
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The account's name: what the page edits first.
    var initialFirstResponder: NSView? { nameEntry.field }

    private func editor(_ kind: Jira.ListKind) -> JiraListEditorView? {
        editors.first { $0.kind == kind }
    }

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

        buildSite()
        buildSpaces()
        buildSync()
        buildFolders()
        buildNotifications()
        buildBots()
        prefsAddFilling(siteGroup, to: content)
        prefsAddFilling(spacesGroup, to: content)
        prefsAddFilling(spacesProblem, to: content)
        prefsAddFilling(syncGroup, to: content)
        prefsAddFilling(foldersGroup, to: content)
        prefsAddFilling(notificationGroup, to: content)
        prefsAddFilling(botsGroup, to: content)
        let scroll = prefsScrollView(clampMaximum: 560, content: content)

        saveButton.title = controller.saveLabel
        saveButton.target = self
        saveButton.action = #selector(saveClicked(_:))
        layoutPage(scroll)
        cancelButton.onCancel = { [weak self] in self?.onCancel?() }
        // Return in the field of a list adds the entry; elsewhere it is
        // the default button's.
        pageView.returnHandler = { [weak self] field in
            guard let self, let editor = self.editors.first(where: { $0.field === field }) else { return false }
            editor.commit()
            return true
        }
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

    /// Lays the page out in `pageView`, as the pages of the account
    /// assistant are (`jiraWizardLayout`): the banner at the top, the
    /// sections filling the middle, the button bar (Cancel, the progress,
    /// Save) at the bottom.
    private func layoutPage(_ sections: NSView) {
        saveButton.bezelStyle = .rounded
        saveButton.controlSize = .large
        saveButton.keyEquivalent = "\r"
        let bar = wizardButtonBar([saveButton], cancel: cancelButton)
        bar.insertView(progress, at: 0, in: .trailing)
        sections.setContentHuggingPriority(.defaultLow, for: .vertical)
        let stack = prefsColumn(spacing: 0)
        prefsAddFilling(banner, to: stack)
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

    /// A label at the end of a row: a value from the site, plain and
    /// selectable, at most half the row wide (a long one is cut in its
    /// middle). It holds its width against the row's pull to shrink what
    /// is at its end, but gives way before the row would grow.
    private func styleValue(_ label: NSTextField) {
        label.font = .systemFont(ofSize: 13)
        label.textColor = .secondaryLabelColor
        label.lineBreakMode = .byTruncatingMiddle
        label.isSelectable = true
        label.alignment = .right
        label.setContentCompressionResistancePriority(.defaultHigh, for: .horizontal)
        label.widthAnchor.constraint(lessThanOrEqualToConstant: 260).isActive = true
    }

    private func buildSite() {
        let t = controller.texts
        styleValue(deploymentLabel)
        styleValue(userLabel)
        addressRow.setSubtitleSelectable()
        userRow.setSubtitleSelectable()
        nameEntry.field.delegate = self
        nameEntry.field.stringValue = controller.form.name
        tokenButton.title = t.replaceToken
        tokenButton.bezelStyle = .rounded
        tokenButton.target = self
        tokenButton.action = #selector(tokenClicked(_:))
        siteGroup.setRows([
            addressRow,
            PreferenceRowView(title: t.accountName, trailing: nameEntry, trailingFills: true),
            userRow,
            tokenRow,
        ])
    }

    private func buildSpaces() {
        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("space"))
        column.resizingMask = .autoresizingMask
        spacesTable.addTableColumn(column)
        spacesTable.headerView = nil
        spacesTable.style = .plain
        spacesTable.rowHeight = Self.spaceRowHeight
        spacesTable.intercellSpacing = .zero
        spacesTable.selectionHighlightStyle = .none
        spacesTable.focusRingType = .none
        spacesTable.backgroundColor = .clear
        spacesTable.columnAutoresizingStyle = .uniformColumnAutoresizingStyle
        spacesTable.dataSource = self
        spacesTable.delegate = self
        spacesScroll.documentView = spacesTable
        spacesScroll.hasVerticalScroller = true
        spacesScroll.hasHorizontalScroller = false
        spacesScroll.autohidesScrollers = true
        spacesScroll.drawsBackground = false
        spacesScroll.borderType = .noBorder
        spacesScroll.translatesAutoresizingMaskIntoConstraints = false
        let h = spacesScroll.heightAnchor.constraint(equalToConstant: 0)
        h.isActive = true
        spacesHeight = h
        noSpacesRow.isEnabled = false
        spacesGroup.setRows([spacesScroll, noSpacesRow])
        spacesProblem.isHidden = true
    }

    private func buildSync() {
        let t = controller.texts
        offlinePopup.addItems(withTitles: controller.offlineLabels)
        offlinePopup.target = self
        offlinePopup.action = #selector(offlineChanged(_:))
        onlyMineSwitch.target = self
        onlyMineSwitch.action = #selector(onlyMineChanged(_:))
        showEventsSwitch.target = self
        showEventsSwitch.action = #selector(showEventsChanged(_:))
        syncGroup.setRows([
            PreferenceRowView(title: t.keepOffline, subtitle: t.keepOfflineSubtitle, trailing: offlinePopup),
            PreferenceRowView(title: t.onlyMine, subtitle: t.onlyMineSubtitle, trailing: onlyMineSwitch),
            PreferenceRowView(title: t.showEvents, trailing: showEventsSwitch),
        ])
    }

    private func buildFolders() {
        var rows: [NSView] = []
        for (i, v) in Jira.virtualFolders.enumerated() {
            let toggle = NSSwitch()
            toggle.tag = i
            toggle.target = self
            toggle.action = #selector(folderChanged(_:))
            folderSwitches.append((v, toggle))
            rows.append(PreferenceRowView(title: Jira.virtualFolderTitle(v), trailing: toggle))
        }
        statusPicker.onToggle = { [weak self] choice, on in
            self?.controller.setStatus(choice, selected: on)
        }
        rows.append(statusPicker)
        foldersGroup.setRows(rows)
    }

    private func buildNotifications() {
        modePopup.addItems(withTitles: controller.notificationLabels)
        modePopup.target = self
        modePopup.action = #selector(modeChanged(_:))
        var rows: [NSView] = [modeRow]
        if let senders = editor(.senders) {
            senders.placeholder = controller.sendersPlaceholder
            rows.append(senders)
        }
        notificationGroup.setRows(rows)
        wireEditors()
    }

    private func buildBots() {
        botsGroup.setRows(editors.filter { $0.kind != .senders })
    }

    private func wireEditors() {
        for e in editors {
            let kind = e.kind
            e.onAdd = { [weak self] text in self?.controller.addEntry(kind, text) ?? false }
            e.onRemove = { [weak self] index in self?.controller.removeEntry(kind, at: index) }
            e.onSuggestion = { [weak self] value in self?.controller.addSuggestion(kind, value) }
            e.onTyped = { [weak self] in self?.controller.entryTyped(kind) }
        }
    }

    // MARK: Inputs from the controller

    /// Shows what the controller holds now.
    func refresh() {
        guard isViewLoaded else { return }
        let editable = !controller.saving

        let site = controller.site
        addressRow.subtitle = site.address
        deploymentLabel.stringValue = site.deployment
        userLabel.stringValue = site.user
        userLabel.toolTip = site.user
        userRow.subtitle = site.userDetail
        tokenRow.title = site.tokenLabel
        if nameEntry.field.stringValue != controller.form.name, nameEntry.field.currentEditor() == nil {
            nameEntry.field.stringValue = controller.form.name
        }
        siteGroup.isEnabled = editable

        let rows = controller.spaceRows
        let selected = controller.selectedSpaces
        if rows != spaceRows || selected != selectedSpaces || spacesGroup.isEnabled != editable {
            spaceRows = rows
            selectedSpaces = selected
            spacesHeight?.constant = CGFloat(min(rows.count, Self.spaceRowsShown)) * Self.spaceRowHeight
            spacesGroup.isEnabled = editable
            spacesTable.reloadData()
        }
        spacesGroup.setRow(spacesScroll, hidden: rows.isEmpty)
        spacesGroup.setRow(noSpacesRow, hidden: !rows.isEmpty)
        let problem = rows.isEmpty ? "" : controller.spacesProblem
        spacesProblem.stringValue = problem
        spacesProblem.isHidden = problem.isEmpty
        // The problem belongs to the list above it.
        content.setCustomSpacing(problem.isEmpty ? content.spacing : 8, after: spacesGroup)

        offlinePopup.selectItem(at: controller.offlineIndex)
        onlyMineSwitch.state = controller.form.onlyMine ? .on : .off
        showEventsSwitch.state = controller.form.showEvents ? .on : .off
        syncGroup.isEnabled = editable

        for (v, toggle) in folderSwitches {
            toggle.state = controller.folderShown(v) ? .on : .off
        }
        statusPicker.apply(groups: controller.statusGroups, problem: controller.statusesProblem)
        foldersGroup.isEnabled = editable

        modePopup.selectItem(at: controller.notificationIndex)
        modeRow.subtitle = controller.notificationHint
        for e in editors {
            e.apply(entries: controller.entries(e.kind), suggestions: controller.suggestions(e.kind), problem: controller.problem(e.kind))
        }
        editor(.senders)?.isEnabled = controller.sendersEditable
        notificationGroup.isEnabled = editable
        botsGroup.isEnabled = editable

        saveButton.isEnabled = controller.canSave
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

    // MARK: Outputs to the controller

    @objc private func saveClicked(_ sender: Any?) {
        controller.setName(nameEntry.field.stringValue)
        // What is still in the field of a list is meant to be in the list.
        // A list that does not matter in the mode chosen (the senders while
        // notification e-mails are left alone) is not looked at: its field
        // is disabled and could not take the focus (dialog.go `onSave`).
        for e in editors where e.isEnabled && !e.field.stringValue.isEmpty {
            if !e.commit() {
                view.window?.makeFirstResponder(e.field)
                e.field.scrollToVisible(e.field.bounds)
                return
            }
        }
        controller.save()
    }

    @objc private func tokenClicked(_ sender: Any?) {
        controller.replaceToken()
    }

    @objc private func offlineChanged(_ sender: NSPopUpButton) {
        controller.setOfflineIndex(sender.indexOfSelectedItem)
    }

    @objc private func onlyMineChanged(_ sender: NSSwitch) {
        controller.setOnlyMine(sender.state == .on)
    }

    @objc private func showEventsChanged(_ sender: NSSwitch) {
        controller.setShowEvents(sender.state == .on)
    }

    @objc private func folderChanged(_ sender: NSSwitch) {
        guard folderSwitches.indices.contains(sender.tag) else { return }
        controller.setFolder(folderSwitches[sender.tag].view, shown: sender.state == .on)
    }

    @objc private func modeChanged(_ sender: NSPopUpButton) {
        controller.setNotificationIndex(sender.indexOfSelectedItem)
    }

    override func cancelOperation(_ sender: Any?) {
        onCancel?()
    }

    // MARK: NSTextFieldDelegate

    func controlTextDidChange(_ obj: Foundation.Notification) {
        guard (obj.object as? NSTextField) === nameEntry.field else { return }
        controller.setName(nameEntry.field.stringValue)
    }

    // MARK: NSTableViewDataSource, NSTableViewDelegate

    func numberOfRows(in tableView: NSTableView) -> Int {
        spaceRows.count
    }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        guard row < spaceRows.count else { return nil }
        let r = spaceRows[row]
        let cell = tableView.makeView(withIdentifier: JiraAccountSpaceCell.reuseIdentifier, owner: nil) as? JiraAccountSpaceCell
            ?? JiraAccountSpaceCell()
        cell.apply(r, selected: selectedSpaces.contains(r.id), enabled: !controller.saving)
        let id = r.id
        cell.onToggle = { [weak self] on in self?.controller.setSpace(id, selected: on) }
        return cell
    }

    func tableView(_ tableView: NSTableView, shouldSelectRow row: Int) -> Bool {
        false
    }
}

/// The page's banner: a callout card with a warning symbol and the text
/// (why the spaces could not be listed, why Save did nothing), as
/// `WizardBannerView`, but with the text pinned between the symbol and the
/// card's edge, so that it wraps at the width of the page whatever width
/// the label had while the banner was hidden.
@MainActor
private final class JiraAccountBannerView: NSView {
    private let label = PrefsWrappingLabel("")
    private let symbolView = CalloutCard.symbolView()

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        setContentHuggingPriority(.required, for: .vertical)
        CalloutCard.show("exclamationmark.triangle.fill", .warning, in: symbolView)
        let content = NSView()
        symbolView.translatesAutoresizingMaskIntoConstraints = false
        label.translatesAutoresizingMaskIntoConstraints = false
        content.addSubview(symbolView)
        content.addSubview(label)
        NSLayoutConstraint.activate([
            symbolView.leadingAnchor.constraint(equalTo: content.leadingAnchor),
            symbolView.centerYAnchor.constraint(equalTo: content.centerYAnchor),
            symbolView.topAnchor.constraint(greaterThanOrEqualTo: content.topAnchor),
            symbolView.bottomAnchor.constraint(lessThanOrEqualTo: content.bottomAnchor),
            label.leadingAnchor.constraint(equalTo: symbolView.trailingAnchor, constant: CalloutCard.spacing),
            label.trailingAnchor.constraint(equalTo: content.trailingAnchor),
            label.topAnchor.constraint(equalTo: content.topAnchor),
            label.bottomAnchor.constraint(equalTo: content.bottomAnchor),
        ])
        CalloutCard.install(content, in: self)
        isHidden = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Shows the banner with `text`, or hides it for nil.
    func reveal(_ text: String?) {
        if let text {
            label.stringValue = text
            isHidden = false
        } else {
            isHidden = true
        }
    }
}

/// One space of the list: its check box titled "KEY – Name". The title is
/// the site's text (cleaned by `Jira.spaceTitle`), plain.
@MainActor
private final class JiraAccountSpaceCell: NSTableCellView {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("JiraAccountSpaceCell")

    private let check = NSButton(checkboxWithTitle: "", target: nil, action: nil)
    var onToggle: ((Bool) -> Void)?

    init() {
        super.init(frame: .zero)
        identifier = JiraAccountSpaceCell.reuseIdentifier
        check.lineBreakMode = .byTruncatingTail
        check.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        check.target = self
        check.action = #selector(toggled(_:))
        check.translatesAutoresizingMaskIntoConstraints = false
        addSubview(check)
        NSLayoutConstraint.activate([
            check.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 12),
            check.trailingAnchor.constraint(lessThanOrEqualTo: trailingAnchor, constant: -12),
            check.centerYAnchor.constraint(equalTo: centerYAnchor),
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
