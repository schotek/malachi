// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The case detail's Suggest Reply, in the place of the Suggested Reply
/// block while the case has none (`Board.SuggestReplyView`, the
/// application's `BoardReplyController`): a one-line field for the user's
/// optional instruction and ✦ Suggest Reply (Return in the field does the
/// same); while the request runs for this case a spinner, its progress and
/// Stop; under them one line, why it is disabled or how it failed. Only
/// fixed texts; the field's text is the user's own and goes only to the
/// controller. One instance per detail, kept across the detail's rebuilds
/// so what the user typed stays while the same case is shown.
@MainActor
final class BoardSuggestReplyControl: NSView {
    /// ✦ Suggest Reply, or Return in the field, with the field's text.
    var onSuggest: ((String) -> Void)?
    var onStop: (() -> Void)?

    let field = NSTextField()
    private let suggestButton = NSButton()
    private let stopButton = NSButton()
    private let spinner = Spinner(size: 16)
    private let progress = NSTextField(labelWithString: "")
    private let note = PrefsWrappingLabel.board("", font: Typo.caption, color: Tint.secondary)
    private let inputRow = NSStackView()
    private let runningRow = NSStackView()
    private var shownCase: Board.CaseID?
    private(set) var shownView: Board.SuggestReplyView = .hidden

    init() {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        field.translatesAutoresizingMaskIntoConstraints = false
        field.lineBreakMode = .byTruncatingTail
        field.usesSingleLineMode = true
        field.cell?.isScrollable = true
        field.cell?.wraps = false
        field.cell?.sendsActionOnEndEditing = false
        field.target = self
        field.action = #selector(suggestClicked(_:))
        field.setContentHuggingPriority(.defaultLow, for: .horizontal)
        field.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(250), for: .horizontal)
        configure(suggestButton, action: #selector(suggestClicked(_:)))
        configure(stopButton, action: #selector(stopClicked(_:)))
        progress.font = Typo.body
        progress.textColor = Tint.secondary
        progress.lineBreakMode = .byTruncatingTail
        progress.isSelectable = false
        progress.setContentHuggingPriority(.defaultLow, for: .horizontal)
        progress.setContentCompressionResistancePriority(NSLayoutConstraint.Priority(250), for: .horizontal)

        inputRow.setViews([field, suggestButton], in: .leading)
        inputRow.orientation = .horizontal
        inputRow.alignment = .centerY
        inputRow.spacing = 8
        runningRow.setViews([spinner, progress, stopButton], in: .leading)
        runningRow.orientation = .horizontal
        runningRow.alignment = .centerY
        runningRow.spacing = 8
        let column = FillStackView(fillingViews: [inputRow, runningRow, note])
        column.spacing = 6
        addSubview(column)
        NSLayoutConstraint.activate([
            column.topAnchor.constraint(equalTo: topAnchor),
            column.bottomAnchor.constraint(equalTo: bottomAnchor),
            column.leadingAnchor.constraint(equalTo: leadingAnchor),
            column.trailingAnchor.constraint(equalTo: trailingAnchor),
        ])
        runningRow.isHidden = true
        note.isHidden = true
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    private func configure(_ button: NSButton, action: Selector) {
        button.bezelStyle = .rounded
        button.controlSize = .regular
        button.target = self
        button.action = action
        button.setContentHuggingPriority(.required, for: .horizontal)
        button.setContentCompressionResistancePriority(.defaultHigh, for: .horizontal)
    }

    /// Shows `v` for case `id`; another case empties the field.
    func apply(_ v: Board.SuggestReplyView, case id: Board.CaseID) {
        if id != shownCase {
            shownCase = id
            field.stringValue = ""
        }
        shownView = v
        field.placeholderString = v.placeholder
        field.setAccessibilityLabel(v.placeholder)
        suggestButton.title = v.title
        suggestButton.setAccessibilityLabel(v.title)
        stopButton.title = v.stop
        stopButton.setAccessibilityLabel(v.stop)
        progress.stringValue = v.progress
        let editing = field.currentEditor() != nil
        inputRow.isHidden = v.running
        runningRow.isHidden = !v.running
        if !v.enabled, editing, let window {
            // The field gives up the keyboard before it goes insensitive:
            // to Stop while the request runs, else to the window.
            if !(v.running && window.makeFirstResponder(stopButton)) {
                window.makeFirstResponder(nil)
            }
        }
        field.isEnabled = v.enabled
        suggestButton.isEnabled = v.enabled
        if v.running {
            spinner.start()
        } else {
            spinner.stop()
        }
        note.stringValue = v.note
        note.textColor = v.noteIsFailure ? .systemRed : Tint.secondary
        note.isHidden = v.note.isEmpty
    }

    @objc private func suggestClicked(_ sender: Any?) {
        guard shownView.enabled, !shownView.running else { return }
        onSuggest?(field.stringValue)
    }

    @objc private func stopClicked(_ sender: Any?) {
        onStop?()
    }
}
