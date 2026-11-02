// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// Settings → Rules, after Integrations (docs/plans/rules.md R14;
/// ui/internal/rules): the page's description, then the rules in their
/// order as rows — the drag handle, the name, the conditions and the
/// actions in one line each, the account and how often the rule acted,
/// the warning when the daemon turned it off, a sending symbol for a rule
/// that sends mail, its switch, Edit Rule… and Remove Rule — or the empty
/// state, and Add Rule… under them. A row moves by dragging its handle or
/// with ⌥⌘↑/⌥⌘↓ (the Accounts page's keys; Alt+Up/Down of the GTK UI).
/// The application's `RulesController` (`AppRules`) keeps the rules; each
/// question is an alert on the settings window, each failure a toast.
@MainActor
final class RulesPaneViewController: PreferencesPaneViewController, NSTableViewDataSource, NSTableViewDelegate {
    private let toast: @MainActor (String) -> Void
    private weak var rules: AppRules?
    private var token: BoardObserverToken?
    private let group = PreferencesGroupView(title: nil, description: Rules.pageDescription)
    private let table = AccountsTableView()
    private let tableScroll = NSScrollView()
    private var tableHeight: NSLayoutConstraint?
    private let emptyRow = PreferenceRowView(title: Rules.emptyTitle, subtitle: Rules.emptyDescription)
    private let addButton = NSButton(title: Rules.addRule, target: nil, action: nil)
    /// The rows as last shown.
    private(set) var rows: [Rules.Row] = []
    private var shownBusy: Set<String> = []
    /// The switches flipped whose call (or question) has not ended: the
    /// row shows the state asked for meanwhile, the rule's own after.
    private var flipped: [String: Bool] = [:]

    /// The window closed: nothing is taken any more.
    var closed = false {
        didSet {
            guard closed, !oldValue else { return }
            token?.cancel()
            token = nil
        }
    }

    init(toast: @escaping @MainActor (String) -> Void) {
        self.toast = toast
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        super.loadView()
        table.dataSource = self
        table.delegate = self
        table.rowHeight = RuleRowCell.baseHeight
        table.onMoveSelected = { [weak self] delta in self?.moveSelected(by: delta) }
        tableScroll.documentView = table
        tableScroll.hasVerticalScroller = false
        tableScroll.hasHorizontalScroller = false
        tableScroll.verticalScrollElasticity = .none
        tableScroll.horizontalScrollElasticity = .none
        tableScroll.drawsBackground = false
        tableScroll.translatesAutoresizingMaskIntoConstraints = false
        let h = tableScroll.heightAnchor.constraint(equalToConstant: 0)
        h.isActive = true
        tableHeight = h
        emptyRow.isEnabled = false
        group.setRows([tableScroll, emptyRow])
        addGroup(group)
        addButton.bezelStyle = .rounded
        addButton.target = self
        addButton.action = #selector(addClicked(_:))
        let addRow = NSStackView(views: [NSView(), addButton])
        addRow.orientation = .horizontal
        prefsAddFilling(addRow, to: contentStack)
        show([])
    }

    /// Binds the page to the application's rules (once).
    func configure(_ rules: AppRules) {
        guard self.rules == nil, !closed else { return }
        _ = view
        self.rules = rules
        token = rules.controller.observe { [weak self] in self?.update() }
        rules.controller.refresh()
        update()
    }

    private func update() {
        guard let c = rules?.controller else { return }
        // Every change redraws: a declined question leaves the rows as they
        // were, yet the switch the user flipped must show the rule again.
        shownBusy = c.busy
        show(c.rows)
        addButton.isEnabled = c.loaded && c.rules.count < API.Limits.maxRules
    }

    private func show(_ rows: [Rules.Row]) {
        self.rows = rows
        var height: CGFloat = 0
        for r in rows {
            height += RuleRowCell.rowHeight(for: r)
        }
        tableHeight?.constant = height
        table.reloadData()
        group.setRow(tableScroll, hidden: rows.isEmpty)
        group.setRow(emptyRow, hidden: !rows.isEmpty)
    }

    // MARK: Actions

    @objc private func addClicked(_ sender: Any?) {
        rules?.addRuleForShownAccount(on: view.window)
    }

    private func edit(_ id: String) {
        rules?.editRule(id, on: view.window)
    }

    private func setEnabled(_ id: String, _ on: Bool) {
        guard let rules else { return }
        let ask = rules.confirm(on: view.window)
        let toast = toast
        flipped[id] = on
        Task { @MainActor [weak self] in
            let text = await rules.controller.setEnabled(id, on, confirm: ask)
            self?.flipped[id] = nil
            self?.update()
            if let text {
                toast(text)
            }
        }
    }

    private func remove(_ id: String) {
        guard let rules else { return }
        let ask = rules.confirm(on: view.window)
        let toast = toast
        Task { @MainActor in
            if let text = await rules.controller.remove(id, confirm: ask) {
                toast(text)
            }
        }
    }

    private func move(from: Int, to: Int) {
        guard let rules else { return }
        let toast = toast
        Task { @MainActor in
            if let text = await rules.controller.move(from: from, to: to) {
                toast(text)
            }
        }
    }

    private func moveSelected(by delta: Int) {
        let row = table.selectedRow
        let to = row + delta
        guard row >= 0, row < rows.count, to >= 0, to < rows.count else { return }
        move(from: row, to: to)
        table.selectRowIndexes(IndexSet(integer: to), byExtendingSelection: false)
    }

    // MARK: NSTableViewDataSource

    func numberOfRows(in tableView: NSTableView) -> Int {
        rows.count
    }

    func tableView(_ tableView: NSTableView, heightOfRow row: Int) -> CGFloat {
        row < rows.count ? RuleRowCell.rowHeight(for: rows[row]) : RuleRowCell.baseHeight
    }

    func tableView(_ tableView: NSTableView, pasteboardWriterForRow row: Int) -> (any NSPasteboardWriting)? {
        guard row < rows.count, let cell = table.view(atColumn: 0, row: row, makeIfNecessary: false) as? RuleRowCell,
              let p = table.mouseDownPoint, cell.handleFrame.contains(cell.convert(p, from: table)) else { return nil }
        let item = NSPasteboardItem()
        item.setString(rows[row].id, forType: .string)
        return item
    }

    func tableView(_ tableView: NSTableView, draggingSession session: NSDraggingSession, willBeginAt screenPoint: NSPoint, forRowIndexes rowIndexes: IndexSet) {
        for row in rowIndexes {
            table.setRowDragging(row, true)
        }
    }

    func tableView(_ tableView: NSTableView, draggingSession session: NSDraggingSession, endedAt screenPoint: NSPoint, operation: NSDragOperation) {
        table.clearDragging()
    }

    /// The payload is one of the rules' ids; text dragged in from another
    /// application is refused.
    private func draggedIndex(_ info: any NSDraggingInfo) -> Int? {
        guard let id = info.draggingPasteboard.string(forType: .string) else { return nil }
        return rows.firstIndex { $0.id == id }
    }

    func tableView(_ tableView: NSTableView, validateDrop info: any NSDraggingInfo, proposedRow row: Int, proposedDropOperation dropOperation: NSTableView.DropOperation) -> NSDragOperation {
        guard let from = draggedIndex(info) else { return [] }
        if dropOperation == .on {
            tableView.setDropRow(row, dropOperation: .above)
        }
        return insertIndex(from: from, target: row, above: true) == from ? [] : .move
    }

    func tableView(_ tableView: NSTableView, acceptDrop info: any NSDraggingInfo, row: Int, dropOperation: NSTableView.DropOperation) -> Bool {
        guard let from = draggedIndex(info) else { return false }
        let to = insertIndex(from: from, target: row, above: true)
        guard to != from else { return false }
        // Never rebuild the rows from inside the drop handler.
        Task { [weak self] in
            guard let self, !self.closed else { return }
            self.move(from: from, to: to)
        }
        return true
    }

    // MARK: NSTableViewDelegate

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        guard row < rows.count else { return nil }
        let r = rows[row]
        let cell = (tableView.makeView(withIdentifier: RuleRowCell.reuseIdentifier, owner: nil) as? RuleRowCell)
            ?? RuleRowCell(frame: .zero)
        cell.apply(r, enabled: flipped[r.id] ?? r.enabled, busy: shownBusy.contains(r.id))
        let id = r.id
        cell.onToggle = { [weak self] on in self?.setEnabled(id, on) }
        cell.onEdit = { [weak self] in self?.edit(id) }
        cell.onRemove = { [weak self] in self?.remove(id) }
        return cell
    }
}

/// One rule of Settings → Rules: the handle, the name with the sending
/// symbol of an outgoing rule, the conditions, the actions, the account
/// and the status (each one line, truncated, whole in the tooltip), the
/// warning of a rule the daemon turned off, then the switch, Edit Rule…
/// and Remove Rule. Every text of a rule is plain text (`stringValue`).
@MainActor
final class RuleRowCell: NSTableCellView {
    static let reuseIdentifier = NSUserInterfaceItemIdentifier("RuleRowCell")
    /// A row without a problem, and one with.
    static let baseHeight: CGFloat = 84
    static let problemHeight: CGFloat = 20

    static func rowHeight(for r: Rules.Row) -> CGFloat {
        r.problem.isEmpty ? baseHeight : baseHeight + problemHeight
    }

    private let handle = RuleRowPassThroughImageView()
    private let titleLabel = NSTextField(labelWithString: "")
    private let outgoingIcon = NSImageView()
    private let conditionsLabel = NSTextField(labelWithString: "")
    private let actionsLabel = NSTextField(labelWithString: "")
    private let detailLabel = NSTextField(labelWithString: "")
    private let problemLabel = NSTextField(labelWithString: "")
    private let toggle = NSSwitch()
    private let editButton: NSButton
    private let removeButton: NSButton
    private var reverting = false

    var onToggle: ((Bool) -> Void)?
    var onEdit: (() -> Void)?
    var onRemove: (() -> Void)?

    /// Where a drag may start, in the cell's coordinates.
    var handleFrame: NSRect { handle.frame.insetBy(dx: -8, dy: -12) }

    override init(frame: NSRect) {
        editButton = NSButton(image: wizardSymbol("pencil", pointSize: 14), target: nil, action: nil)
        removeButton = NSButton(image: wizardSymbol("trash", pointSize: 14), target: nil, action: nil)
        super.init(frame: frame)
        identifier = RuleRowCell.reuseIdentifier

        handle.image = wizardSymbol("line.3.horizontal", pointSize: 14)
        handle.contentTintColor = NSColor.labelColor.withAlphaComponent(0.55)
        handle.imageScaling = .scaleNone
        handle.toolTip = "Drag to reorder, or press \u{2325}\u{2318}\u{2191} and \u{2325}\u{2318}\u{2193}" // macOS-only string

        titleLabel.font = .systemFont(ofSize: 13, weight: .medium)
        outgoingIcon.image = wizardSymbol("paperplane", pointSize: 12)
        outgoingIcon.contentTintColor = .secondaryLabelColor
        for l in [titleLabel, conditionsLabel, actionsLabel, detailLabel, problemLabel] {
            l.lineBreakMode = .byTruncatingTail
            l.setContentHuggingPriority(.defaultLow, for: .horizontal)
            l.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        }
        for l in [conditionsLabel, actionsLabel] {
            l.font = .systemFont(ofSize: 11)
        }
        detailLabel.font = .preferredFont(forTextStyle: .caption1)
        detailLabel.textColor = .secondaryLabelColor
        problemLabel.font = .systemFont(ofSize: 11)
        problemLabel.textColor = .systemOrange

        toggle.toolTip = Rules.enabledTooltip
        toggle.setAccessibilityLabel(Rules.enabledTooltip)
        toggle.target = self
        toggle.action = #selector(toggled(_:))
        editButton.toolTip = Rules.editRule
        editButton.setAccessibilityLabel(Rules.editRule)
        editButton.target = self
        editButton.action = #selector(editClicked(_:))
        removeButton.toolTip = Rules.removeRule
        removeButton.setAccessibilityLabel(Rules.removeRule)
        removeButton.target = self
        removeButton.action = #selector(removeClicked(_:))
        for button in [editButton, removeButton] {
            button.isBordered = false
            button.imagePosition = .imageOnly
            button.contentTintColor = .labelColor
            button.widthAnchor.constraint(equalToConstant: 28).isActive = true
            button.heightAnchor.constraint(equalToConstant: 28).isActive = true
        }

        let titleRow = NSStackView(views: [titleLabel, outgoingIcon])
        titleRow.orientation = .horizontal
        titleRow.alignment = .centerY
        titleRow.spacing = 6
        titleRow.detachesHiddenViews = true
        outgoingIcon.setContentHuggingPriority(.required, for: .horizontal)
        let text = prefsColumn(spacing: 2)
        text.detachesHiddenViews = true
        for v in [titleRow, conditionsLabel, actionsLabel, detailLabel, problemLabel] as [NSView] {
            prefsAddFilling(v, to: text)
        }
        let cluster = NSStackView(views: [toggle, editButton, removeButton])
        cluster.orientation = .horizontal
        cluster.alignment = .centerY
        cluster.spacing = 6
        cluster.setCustomSpacing(2, after: editButton)
        cluster.setHuggingPriority(.required, for: .horizontal)
        cluster.translatesAutoresizingMaskIntoConstraints = false
        for v in [handle, toggle, editButton, removeButton] as [NSView] {
            v.translatesAutoresizingMaskIntoConstraints = false
            v.setContentHuggingPriority(.required, for: .horizontal)
            v.setContentCompressionResistancePriority(.required, for: .horizontal)
        }
        addSubview(handle)
        addSubview(text)
        addSubview(cluster)
        NSLayoutConstraint.activate([
            handle.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 12),
            handle.centerYAnchor.constraint(equalTo: centerYAnchor),
            text.leadingAnchor.constraint(equalTo: handle.trailingAnchor, constant: 12),
            text.centerYAnchor.constraint(equalTo: centerYAnchor),
            text.trailingAnchor.constraint(equalTo: cluster.leadingAnchor, constant: -12),
            cluster.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -12),
            cluster.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// Shows the rule's row with its switch at `enabled`; `busy` while its
    /// switch or removal runs.
    func apply(_ r: Rules.Row, enabled: Bool, busy: Bool) {
        titleLabel.stringValue = r.title
        titleLabel.toolTip = r.title
        outgoingIcon.isHidden = !r.outgoing
        conditionsLabel.stringValue = r.conditions
        conditionsLabel.toolTip = r.conditions
        actionsLabel.stringValue = r.actions
        actionsLabel.toolTip = r.actions
        // The account, then how often the rule acted: two values of one
        // line, apart as the GTK row's subtitle shows them.
        let detail = [r.scope, r.status].filter { !$0.isEmpty }.joined(separator: " · ") // macOS-only string
        detailLabel.stringValue = detail
        detailLabel.toolTip = detail
        problemLabel.stringValue = r.problem.isEmpty ? "" : "\u{26A0}\u{FE0E} " + r.problem
        problemLabel.toolTip = r.problem.isEmpty ? nil : r.problem
        problemLabel.isHidden = r.problem.isEmpty
        reverting = true
        toggle.state = enabled ? .on : .off
        reverting = false
        for c in [toggle, editButton, removeButton] as [NSControl] {
            c.isEnabled = !busy
        }
    }

    @objc private func toggled(_ sender: NSSwitch) {
        guard !reverting else { return }
        onToggle?(sender.state == .on)
    }

    @objc private func editClicked(_ sender: Any?) {
        onEdit?()
    }

    @objc private func removeClicked(_ sender: Any?) {
        onRemove?()
    }
}

/// The handle: an image that lets the click through to the table.
@MainActor
private final class RuleRowPassThroughImageView: NSImageView {
    override func hitTest(_ point: NSPoint) -> NSView? {
        nil
    }
}
