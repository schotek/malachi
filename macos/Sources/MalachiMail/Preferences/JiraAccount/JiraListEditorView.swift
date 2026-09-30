// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// One list of texts of a Jira account's settings, as a row of a boxed
/// list: the bot accounts, the hidden lines, the name prefixes or the
/// senders of notification e-mails. Under the title and its explanation
/// the entries, each with a button that removes it; a field with Add for
/// a new one (Return adds too); the entries offered with one click
/// (`Jira.suggestions`); and why the entry typed last was not added
/// (`Jira.checkEntry`). The view shows what `JiraAccountController` holds
/// and reports what the user did; it checks nothing itself.
///
/// The entries are the user's own texts, but an account edited elsewhere
/// may carry anything: they are shown as plain text.
@MainActor
final class JiraListEditorView: NSView, PrefsGroupMember, NSTextFieldDelegate {
    let kind: Jira.ListKind

    /// Add or Return with the field's text; true empties the field.
    var onAdd: ((String) -> Bool)?
    /// The remove button of the entry at an index.
    var onRemove: ((Int) -> Void)?
    /// A suggestion's button, with its entry.
    var onSuggestion: ((String) -> Void)?
    /// The field's text changed.
    var onTyped: (() -> Void)?

    private let titleLabel: NSTextField
    private let subtitleLabel: PrefsWrappingLabel
    private let entriesStack = prefsColumn(spacing: 0)
    private let entry = WizardEntryField()
    private let addButton: NSButton
    private let suggestionsFlow = FlowView(spacing: 6, lineSpacing: 6)
    private let problemLabel = PrefsWrappingLabel("", size: 11, color: .systemRed)
    private let removeLabel: String

    private var entries: [String] = []
    private var suggestions: [Jira.Suggestion] = []
    private var rowEnabled = true
    private var groupEnabled = true

    /// The field a new entry is typed into.
    var field: NSTextField { entry.field }

    /// The field's placeholder: what an empty list stands for.
    var placeholder: String {
        get { entry.field.placeholderString ?? "" }
        set { entry.field.placeholderString = newValue.isEmpty ? nil : newValue }
    }

    /// The row's own sensitivity.
    var isEnabled: Bool {
        get { rowEnabled }
        set {
            rowEnabled = newValue
            applyEnabled()
        }
    }

    init(kind: Jira.ListKind, title: String, subtitle: String, addLabel: String, removeLabel: String) {
        self.kind = kind
        self.removeLabel = removeLabel
        titleLabel = NSTextField(labelWithString: title)
        titleLabel.font = .systemFont(ofSize: 13)
        titleLabel.lineBreakMode = .byTruncatingTail
        titleLabel.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        subtitleLabel = PrefsWrappingLabel(subtitle, size: 11, color: .secondaryLabelColor)
        subtitleLabel.isHidden = subtitle.isEmpty
        addButton = NSButton(title: addLabel, target: nil, action: nil)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false

        if kind == .metadataFilters {
            entry.field.font = .monospacedSystemFont(ofSize: 12, weight: .regular)
        }
        entry.field.delegate = self
        entry.field.setAccessibilityLabel(title)
        addButton.bezelStyle = .rounded
        addButton.target = self
        addButton.action = #selector(addClicked(_:))
        addButton.setContentHuggingPriority(.required, for: .horizontal)
        addButton.setContentCompressionResistancePriority(.required, for: .horizontal)
        addButton.translatesAutoresizingMaskIntoConstraints = false
        // The field takes the width the button leaves (explicit
        // constraints, as in a PreferenceRowView: a stack view hands spare
        // width out by priority ties).
        let addRow = NSView()
        addRow.translatesAutoresizingMaskIntoConstraints = false
        addRow.addSubview(entry)
        addRow.addSubview(addButton)
        let hug = addRow.heightAnchor.constraint(equalToConstant: 0)
        hug.priority = NSLayoutConstraint.Priority(1)
        NSLayoutConstraint.activate([
            entry.leadingAnchor.constraint(equalTo: addRow.leadingAnchor),
            entry.trailingAnchor.constraint(equalTo: addButton.leadingAnchor, constant: -8),
            addButton.trailingAnchor.constraint(equalTo: addRow.trailingAnchor),
            entry.centerYAnchor.constraint(equalTo: addRow.centerYAnchor),
            addButton.centerYAnchor.constraint(equalTo: addRow.centerYAnchor),
            addRow.heightAnchor.constraint(greaterThanOrEqualTo: entry.heightAnchor),
            addRow.heightAnchor.constraint(greaterThanOrEqualTo: addButton.heightAnchor),
            hug,
        ])

        problemLabel.isHidden = true
        entriesStack.isHidden = true
        suggestionsFlow.isHidden = true

        let inset = PreferenceRowView.inset
        let column = prefsColumn(spacing: 6, insets: NSEdgeInsets(top: 10, left: inset, bottom: 12, right: inset))
        prefsAddFilling(titleLabel, to: column)
        prefsAddFilling(subtitleLabel, to: column)
        column.setCustomSpacing(2, after: titleLabel)
        prefsAddFilling(entriesStack, to: column)
        prefsAddFilling(addRow, to: column)
        prefsAddFilling(suggestionsFlow, to: column)
        prefsAddFilling(problemLabel, to: column)
        addSubview(column)
        NSLayoutConstraint.activate([
            column.topAnchor.constraint(equalTo: topAnchor),
            column.bottomAnchor.constraint(equalTo: bottomAnchor),
            column.leadingAnchor.constraint(equalTo: leadingAnchor),
            column.trailingAnchor.constraint(equalTo: trailingAnchor),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    // MARK: Inputs

    /// Shows the list as the controller holds it.
    func apply(entries: [String], suggestions: [Jira.Suggestion], problem: String) {
        if entries != self.entries {
            self.entries = entries
            rebuildEntries()
        }
        if suggestions != self.suggestions {
            self.suggestions = suggestions
            rebuildSuggestions()
        }
        problemLabel.stringValue = problem
        problemLabel.isHidden = problem.isEmpty
        entry.hasError = !problem.isEmpty
    }

    /// Adds what the field holds, as Add does; true when nothing is left
    /// in it that could not be added.
    @discardableResult
    func commit() -> Bool {
        let added = onAdd?(entry.field.stringValue) ?? false
        if added {
            entry.field.stringValue = ""
        }
        return added
    }

    func setGroupEnabled(_ enabled: Bool) {
        groupEnabled = enabled
        applyEnabled()
    }

    // MARK: Rows

    private func rebuildEntries() {
        for v in entriesStack.arrangedSubviews {
            entriesStack.removeArrangedSubview(v)
            v.removeFromSuperview()
        }
        for (i, text) in entries.enumerated() {
            let row = JiraListEntryRow(text: text, index: i, monospaced: kind == .metadataFilters, removeLabel: removeLabel)
            row.onRemove = { [weak self] index in self?.onRemove?(index) }
            row.isEnabled = rowEnabled && groupEnabled
            prefsAddFilling(row, to: entriesStack)
        }
        entriesStack.isHidden = entries.isEmpty
    }

    private func rebuildSuggestions() {
        suggestionsFlow.removeAllViews()
        for (i, s) in suggestions.enumerated() {
            let button = NSButton(title: s.label, target: self, action: #selector(suggestionClicked(_:)))
            button.bezelStyle = .rounded
            button.controlSize = .small
            button.tag = i
            button.lineBreakMode = .byTruncatingMiddle
            button.isEnabled = rowEnabled && groupEnabled
            button.frame.size = button.fittingSize
            suggestionsFlow.addView(button)
        }
        suggestionsFlow.isHidden = suggestions.isEmpty
    }

    private func applyEnabled() {
        let on = rowEnabled && groupEnabled
        titleLabel.textColor = on ? .labelColor : .tertiaryLabelColor
        subtitleLabel.textColor = on ? .secondaryLabelColor : .tertiaryLabelColor
        entry.isEnabled = on
        addButton.isEnabled = on
        for case let row as JiraListEntryRow in entriesStack.arrangedSubviews {
            row.isEnabled = on
        }
        for case let button as NSButton in suggestionsFlow.views {
            button.isEnabled = on
        }
    }

    // MARK: Actions

    @objc private func addClicked(_ sender: Any?) {
        commit()
        window?.makeFirstResponder(entry.field)
    }

    @objc private func suggestionClicked(_ sender: NSButton) {
        guard suggestions.indices.contains(sender.tag) else { return }
        onSuggestion?(suggestions[sender.tag].value)
    }

    // MARK: NSTextFieldDelegate

    func controlTextDidChange(_ obj: Foundation.Notification) {
        onTyped?()
    }

    func control(_ control: NSControl, textView: NSTextView, doCommandBy commandSelector: Selector) -> Bool {
        if commandSelector == #selector(NSResponder.insertNewline(_:)) {
            commit()
            return true
        }
        return false
    }
}

/// An entry of a list: its text and the button that removes it.
@MainActor
private final class JiraListEntryRow: NSView {
    private let label: NSTextField
    private let removeButton: NSButton
    private let index: Int
    var onRemove: ((Int) -> Void)?

    var isEnabled: Bool {
        get { removeButton.isEnabled }
        set {
            removeButton.isEnabled = newValue
            label.textColor = newValue ? .labelColor : .tertiaryLabelColor
        }
    }

    init(text: String, index: Int, monospaced: Bool, removeLabel: String) {
        self.index = index
        label = NSTextField(labelWithString: "")
        removeButton = NSButton(image: wizardSymbol("minus.circle", pointSize: 13), target: nil, action: nil)
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false

        label.stringValue = text
        label.font = monospaced ? .monospacedSystemFont(ofSize: 12, weight: .regular) : .systemFont(ofSize: 13)
        label.lineBreakMode = .byTruncatingMiddle
        label.toolTip = text
        label.setContentHuggingPriority(.defaultLow, for: .horizontal)
        label.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        removeButton.isBordered = false
        removeButton.imagePosition = .imageOnly
        removeButton.contentTintColor = .secondaryLabelColor
        removeButton.toolTip = removeLabel
        removeButton.setAccessibilityLabel(removeLabel)
        removeButton.target = self
        removeButton.action = #selector(removeClicked(_:))
        for v in [label, removeButton] as [NSView] {
            v.translatesAutoresizingMaskIntoConstraints = false
            addSubview(v)
        }
        NSLayoutConstraint.activate([
            heightAnchor.constraint(equalToConstant: 26),
            label.leadingAnchor.constraint(equalTo: leadingAnchor),
            label.centerYAnchor.constraint(equalTo: centerYAnchor),
            label.trailingAnchor.constraint(lessThanOrEqualTo: removeButton.leadingAnchor, constant: -8),
            removeButton.trailingAnchor.constraint(equalTo: trailingAnchor),
            removeButton.centerYAnchor.constraint(equalTo: centerYAnchor),
            removeButton.widthAnchor.constraint(equalToConstant: 24),
            removeButton.heightAnchor.constraint(equalToConstant: 24),
        ])
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    @objc private func removeClicked(_ sender: Any?) {
        onRemove?(index)
    }
}
