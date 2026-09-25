// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// Recipient completion of one To, Cc or Bcc row (ui/internal/compose/
/// suggest.go): a borderless panel under the field offering what
/// contact.search returns for the address token under the caret. The
/// backend ranks and merges; this finds the token (`tokenAt`), asks after a
/// pause in typing, and inserts the answer (`replaceToken`). The panel
/// never takes the focus, so typing goes on in the row; the arrow keys,
/// Return, Tab and Escape are read off the row's field editor. The
/// controller is the field's delegate; the window hears about edits
/// through `onChanged`.
@MainActor
final class RecipientSuggestionsController: NSObject, NSTextFieldDelegate, NSTableViewDataSource, NSTableViewDelegate {
    static let rowHeight: CGFloat = 36
    private static let padding = NSEdgeInsets(top: 4, left: 10, bottom: 4, right: 10)

    let field: NSTextField

    /// Every edit of the row (the window validates it and marks the draft
    /// dirty), including an accepted suggestion.
    var onChanged: (@MainActor () -> Void)?

    /// What the panel shows, in order.
    private(set) var contacts: [Contact] = []

    private let client: RPCClient
    private let account: @MainActor () -> AccountID
    /// Guards replies of a search the text has outrun.
    private var gen: UInt64 = 0
    private var timer: Task<Void, Never>?
    /// accept's text change must not start a search of its own.
    private var suppress = false
    /// After `cleanup`: a pending search must not touch the row.
    private var closed = false

    private let panel: NSPanel
    private let table = NSTableView()
    private let scroll = NSScrollView()
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "compose")

    /// - Parameters:
    ///   - field: the recipient row; the controller becomes its delegate.
    ///   - client: the transport for contact.search.
    ///   - account: the sender identity's id (its address books are asked).
    init(field: NSTextField, client: RPCClient, account: @escaping @MainActor () -> AccountID) {
        self.field = field
        self.client = client
        self.account = account
        panel = NSPanel(
            contentRect: NSRect(x: 0, y: 0, width: 240, height: Self.rowHeight),
            styleMask: [.borderless, .nonactivatingPanel], backing: .buffered, defer: true)
        super.init()

        panel.isReleasedWhenClosed = false
        panel.hasShadow = true
        panel.isOpaque = false
        panel.backgroundColor = .clear
        panel.hidesOnDeactivate = false
        panel.becomesKeyOnlyIfNeeded = true
        panel.isMovableByWindowBackground = false
        panel.animationBehavior = .none

        let backdrop = NSVisualEffectView()
        backdrop.material = .popover
        backdrop.blendingMode = .behindWindow
        backdrop.state = .active
        backdrop.wantsLayer = true
        backdrop.layer?.cornerRadius = 8
        backdrop.layer?.masksToBounds = true
        backdrop.translatesAutoresizingMaskIntoConstraints = false

        let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("suggestion"))
        column.resizingMask = .autoresizingMask
        table.addTableColumn(column)
        table.headerView = nil
        table.rowHeight = Self.rowHeight
        table.intercellSpacing = .zero
        table.style = .plain
        table.backgroundColor = .clear
        table.selectionHighlightStyle = .regular
        table.allowsEmptySelection = true
        table.allowsMultipleSelection = false
        table.refusesFirstResponder = true
        table.columnAutoresizingStyle = .uniformColumnAutoresizingStyle
        table.dataSource = self
        table.delegate = self
        table.target = self
        table.action = #selector(rowClicked(_:))

        scroll.documentView = table
        scroll.drawsBackground = false
        scroll.hasVerticalScroller = true
        scroll.hasHorizontalScroller = false
        scroll.autohidesScrollers = true
        scroll.translatesAutoresizingMaskIntoConstraints = false

        backdrop.addSubview(scroll)
        NSLayoutConstraint.activate([
            scroll.topAnchor.constraint(equalTo: backdrop.topAnchor),
            scroll.bottomAnchor.constraint(equalTo: backdrop.bottomAnchor),
            scroll.leadingAnchor.constraint(equalTo: backdrop.leadingAnchor),
            scroll.trailingAnchor.constraint(equalTo: backdrop.trailingAnchor),
        ])
        panel.contentView = backdrop
        field.delegate = self
    }

    /// Whether the panel is showing (the window's Escape must not close
    /// the window then).
    var isVisible: Bool { panel.isVisible }

    /// The window is closing: the panel goes and nothing arrives late.
    func cleanup() {
        hide()
        closed = true
        if field.delegate === self {
            field.delegate = nil
        }
    }

    // MARK: NSTextFieldDelegate

    func controlTextDidChange(_ obj: Foundation.Notification) {
        guard !suppress else { return }
        onChanged?()
        textChanged()
    }

    func controlTextDidEndEditing(_ obj: Foundation.Notification) {
        hide()
    }

    /// onKey: drives the panel from the row while it is shown; everything
    /// else, and every key while it is hidden, goes on to the row.
    func control(_ control: NSControl, textView: NSTextView, doCommandBy commandSelector: Selector) -> Bool {
        guard panel.isVisible else { return false }
        switch commandSelector {
        case #selector(NSResponder.moveDown(_:)):
            move(1)
            return true
        case #selector(NSResponder.moveUp(_:)):
            move(-1)
            return true
        case #selector(NSResponder.insertNewline(_:)), #selector(NSResponder.insertTab(_:)):
            if table.selectedRow >= 0 {
                accept(table.selectedRow)
                return true
            }
            return false
        case #selector(NSResponder.cancelOperation(_:)):
            hide()
            return true
        default:
            return false
        }
    }

    // MARK: Searching

    /// The caret as a scalar offset into the field's text (GTK's character
    /// position); the end of the text when the row is not being edited.
    private var caret: Int {
        let text = field.stringValue
        guard let editor = field.currentEditor() else { return text.unicodeScalars.count }
        let loc = min(max(editor.selectedRange.location, 0), text.utf16.count)
        return scalarOffset(of: String.Index(utf16Offset: loc, in: text), in: text)
    }

    /// onChanged: finds the token under the caret and, after a pause in
    /// typing, asks for suggestions.
    private func textChanged() {
        cancelTimer()
        let (token, _) = tokenAt(field.stringValue, caret: caret)
        if token.unicodeScalars.count < suggestMinChars {
            hide()
            return
        }
        timer = Task { [weak self] in
            do {
                try await Task.sleep(for: suggestDebounce)
            } catch {
                return
            }
            guard let self, !Task.isCancelled else { return }
            self.timer = nil
            self.search(token)
        }
    }

    /// search asks the backend for `token` and shows the answer, unless the
    /// row has moved on meanwhile. A failure is logged, never shown:
    /// completion is a convenience, and the row still takes what is typed.
    private func search(_ token: String) {
        gen += 1
        let gen = gen
        let params = ContactSearchParams(accountId: account(), query: token, limit: suggestLimit)
        let client = client
        Task { [weak self] in
            let outcome: Result<ContactSearchResult, any Error>
            do {
                outcome = .success(try await client.call(API.ContactSearch.self, params))
            } catch {
                outcome = .failure(error)
            }
            guard let self, !self.closed, gen == self.gen else { return }
            switch outcome {
            case .failure(let err):
                self.log.debug("contact.search: \(String(describing: err), privacy: .public)")
                self.hide()
            case .success(let res):
                let (now, _) = tokenAt(self.field.stringValue, caret: self.caret)
                guard now == token else { return }
                self.show(res.contacts)
            }
        }
    }

    /// show fills the panel and pops it up the width of the row, with the
    /// first suggestion selected so Return takes it at once.
    private func show(_ list: [Contact]) {
        contacts = list
        if list.isEmpty {
            hide()
            return
        }
        table.reloadData()
        table.selectRowIndexes(IndexSet(integer: 0), byExtendingSelection: false)
        guard let window = field.window else { return }
        let fieldRect = window.convertToScreen(field.convert(field.bounds, to: nil))
        let height = CGFloat(min(list.count, suggestLimit)) * Self.rowHeight
        let frame = NSRect(x: fieldRect.minX, y: fieldRect.minY - height - 2, width: fieldRect.width, height: height)
        panel.setFrame(frame, display: true)
        if panel.parent !== window {
            panel.parent?.removeChildWindow(panel)
            window.addChildWindow(panel, ordered: .above)
        }
        if !panel.isVisible {
            panel.orderFront(nil)
        }
    }

    /// hide closes the panel and forgets any search in flight.
    func hide() {
        cancelTimer()
        gen += 1
        if panel.isVisible {
            panel.parent?.removeChildWindow(panel)
            panel.orderOut(nil)
        }
    }

    private func cancelTimer() {
        timer?.cancel()
        timer = nil
    }

    /// move steps the selection, wrapping around.
    private func move(_ delta: Int) {
        let n = contacts.count
        guard n > 0 else { return }
        let i = max(table.selectedRow, 0)
        let next = ((i + delta) % n + n) % n
        table.selectRowIndexes(IndexSet(integer: next), byExtendingSelection: false)
        table.scrollRowToVisible(next)
    }

    /// accept replaces the token under the caret with the chosen contact
    /// and leaves the caret after the separator, ready for the next
    /// recipient.
    private func accept(_ i: Int) {
        guard i >= 0, i < contacts.count else { return }
        let c = contacts[i]
        let text = field.stringValue
        let (_, range) = tokenAt(text, caret: caret)
        let (newText, caretOffset) = replaceToken(in: text, range: range, with: Address(name: c.name, address: c.address))
        hide()
        suppress = true
        field.stringValue = newText
        if let editor = field.currentEditor() {
            let idx = index(atScalarOffset: caretOffset, in: newText)
            editor.selectedRange = NSRange(location: idx.utf16Offset(in: newText), length: 0)
        }
        suppress = false
        // GTK's SetText fires the row's changed handler (validation, dirty).
        onChanged?()
    }

    @objc private func rowClicked(_ sender: Any?) {
        let row = table.clickedRow
        guard row >= 0 else { return }
        accept(row)
    }

    // MARK: NSTableViewDataSource / NSTableViewDelegate

    func numberOfRows(in tableView: NSTableView) -> Int {
        contacts.count
    }

    /// suggestionRow: the source icon, the name over the address (or the
    /// address alone). Labels never interpret markup.
    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        guard row >= 0, row < contacts.count else { return nil }
        let c = contacts[row]
        let icon = NSImageView(image: Icon.image(suggestionIcon(c.source), size: .regular))
        icon.contentTintColor = Tint.secondary
        icon.setContentHuggingPriority(.required, for: .horizontal)

        let name = (c.name ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        let primary = NSTextField(labelWithString: name.isEmpty ? c.address : name)
        primary.font = Typo.body
        primary.lineBreakMode = .byTruncatingTail
        primary.alignment = .left
        let text = NSStackView(views: [primary])
        text.orientation = .vertical
        text.alignment = .leading
        text.spacing = 0
        if !name.isEmpty {
            let secondary = NSTextField(labelWithString: c.address)
            secondary.font = Typo.caption
            secondary.textColor = Tint.secondary
            secondary.lineBreakMode = .byTruncatingTail
            secondary.alignment = .left
            text.addArrangedSubview(secondary)
        }

        let cell = NSTableCellView()
        let row = NSStackView(views: [icon, text])
        row.orientation = .horizontal
        row.alignment = .centerY
        row.spacing = 8
        row.edgeInsets = Self.padding
        row.translatesAutoresizingMaskIntoConstraints = false
        cell.addSubview(row)
        NSLayoutConstraint.activate([
            row.topAnchor.constraint(equalTo: cell.topAnchor),
            row.bottomAnchor.constraint(equalTo: cell.bottomAnchor),
            row.leadingAnchor.constraint(equalTo: cell.leadingAnchor),
            row.trailingAnchor.constraint(equalTo: cell.trailingAnchor),
        ])
        cell.toolTip = suggestionTooltip(c)
        return cell
    }
}
