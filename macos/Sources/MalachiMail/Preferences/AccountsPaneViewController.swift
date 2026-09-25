// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// The Accounts page of the settings (preferences.blp `accounts_page`,
/// accounts_page.go, accounts_reorder.go): the daemon's accounts with
/// add, edit, remove, pause and reorder. The page reloads after its own
/// actions; the group stays insensitive until the first load succeeds.
/// Positions always come from `accounts`, never from table indexes.
@MainActor
final class AccountsPaneViewController: PreferencesPaneViewController, NSTableViewDataSource, NSTableViewDelegate {
    private let client: RPCClient
    private let confirmRemoval: PrefsConfirmRemoval
    private let toast: @MainActor (String) -> Void
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "preferences")

    private let group: PreferencesGroupView
    private let addButton: NSButton
    private let table = AccountsTableView()
    private let tableScroll = NSScrollView()
    private var tableHeight: NSLayoutConstraint?
    private let emptyRow = PreferenceRowView(title: L10n.T("No accounts yet"), subtitle: L10n.T("Use the + button to add one"))

    private(set) var accounts: [Account] = []
    /// Accounts whose call is in flight: their rows are insensitive.
    private var pending: Set<AccountID> = []
    /// The window closed: late replies are dropped.
    var closed = false

    init(client: RPCClient, confirmRemoval: @escaping PrefsConfirmRemoval, toast: @escaping @MainActor (String) -> Void) {
        self.client = client
        self.confirmRemoval = confirmRemoval
        self.toast = toast
        addButton = NSButton(image: wizardSymbol("plus", pointSize: 14, weight: .medium), target: nil, action: nil)
        addButton.isBordered = false
        addButton.imagePosition = .imageOnly
        addButton.contentTintColor = .labelColor
        addButton.toolTip = L10n.T("Add Account")
        group = PreferencesGroupView(title: L10n.T("Mail Accounts"), headerSuffix: addButton)
        super.init(nibName: nil, bundle: nil)
        addButton.target = self
        addButton.action = #selector(addClicked(_:))
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        super.loadView()
        table.dataSource = self
        table.delegate = self
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
        group.isEnabled = false
        addGroup(group)
        setAccounts([])
        loadAccounts()
    }

    // MARK: Loading

    /// Runs account.list and rebuilds the rows (accounts_page.go `loadAccounts`).
    func loadAccounts() {
        let client = client
        Task { [weak self] in
            let outcome: Result<[Account], any Error>
            do {
                outcome = .success(try await client.call(API.AccountList.self, EmptyParams(), timeout: RPCTimeouts.default).accounts)
            } catch {
                outcome = .failure(error)
            }
            guard let self, !self.closed else { return }
            switch outcome {
            case .failure(let error):
                self.group.descriptionText = rpcErrorText(L10n.T("Loading accounts"), error)
                self.group.isEnabled = false
            case .success(let accounts):
                self.group.descriptionText = ""
                self.setAccounts(accounts)
                self.group.isEnabled = true
            }
        }
    }

    /// Replaces the rows (accounts_page.go `setAccounts`).
    private func setAccounts(_ list: [Account]) {
        accounts = list
        pending = pending.filter { id in list.contains { $0.id == id } }
        tableHeight?.constant = CGFloat(list.count) * AccountsTableView.rowHeight
        table.reloadData()
        group.setRow(tableScroll, hidden: list.isEmpty)
        group.setRow(emptyRow, hidden: !list.isEmpty)
    }

    private func index(of id: AccountID) -> Int? {
        accounts.firstIndex { $0.id == id }
    }

    private func reloadRow(_ id: AccountID) {
        guard let i = index(of: id) else { return }
        table.reloadData(forRowIndexes: IndexSet(integer: i), columnIndexes: IndexSet(integer: 0))
    }

    // MARK: Actions

    @objc private func addClicked(_ sender: Any?) {
        guard let window = view.window else { return }
        AccountWizardController.present(from: window, client: client) { [weak self] _, cfg in
            guard let self else { return }
            self.loadAccounts()
            // TRANSLATORS: %s is the new account's e-mail address.
            self.toast(L10n.T("Added %s", cfg.email))
        }
    }

    /// Pauses or resumes through account.setEnabled; on failure the switch
    /// flips back and a toast explains (accounts_page.go `setAccountEnabled`).
    private func setAccountEnabled(_ id: AccountID, _ want: Bool) {
        pending.insert(id)
        reloadRow(id)
        let what = want ? L10n.T("Resuming the account") : L10n.T("Pausing the account")
        let client = client
        Task { [weak self] in
            var failure: (any Error)?
            do {
                _ = try await client.call(API.AccountSetEnabled.self, AccountSetEnabledParams(accountId: id, enabled: want), timeout: RPCTimeouts.default)
            } catch {
                failure = error
            }
            guard let self, !self.closed else { return }
            self.pending.remove(id)
            // The row may have been torn down by a reload meanwhile.
            guard let i = self.index(of: id) else { return }
            if let failure {
                self.toast(rpcErrorText(what, failure))
                self.reloadRow(id)
                return
            }
            self.accounts[i].enabled = want
            self.accounts[i].state.status = want ? .idle : .disabled
            self.reloadRow(id)
        }
    }

    /// Opens the wizard prefilled with the account (accounts_page.go
    /// `editAccount`); with `signIn` only the browser sign-in again
    /// (accounts_page.go `signInAccount`, NewEditSignIn).
    private func editAccount(_ id: AccountID, signIn: Bool = false) {
        guard let window = view.window, let i = index(of: id) else { return }
        AccountWizardController.present(from: window, client: client, editing: accounts[i], signIn: signIn) { [weak self] _, cfg in
            guard let self else { return }
            self.loadAccounts()
            // TRANSLATORS: %s is the edited account's e-mail address.
            self.toast(L10n.T("Saved %s", cfg.email))
        }
    }

    /// Confirms, then calls account.remove; the check box decides
    /// deleteLocalData (accounts_page.go `removeAccount`).
    private func removeAccount(_ id: AccountID) {
        guard let i = index(of: id) else { return }
        let email = accounts[i].config.email
        let prompt = PrefsConfirmation(
            heading: L10n.T("Remove this account?"),
            // TRANSLATORS: %s is the account's e-mail address.
            body: L10n.T("%s will be removed from Malachi Mail. Mail on the server is not affected.", email),
            confirmLabel: wizardLabel("_Remove"),
            extraLabel: wizardLabel("Also delete _drafts and downloaded data"),
            extraDefault: true
        )
        let window = view.window
        let confirmRemoval = confirmRemoval
        let client = client
        Task { [weak self] in
            let answer = await confirmRemoval(window, prompt)
            guard answer.confirmed, let self, !self.closed, self.index(of: id) != nil else { return }
            self.pending.insert(id)
            self.reloadRow(id)
            var failure: (any Error)?
            do {
                _ = try await client.call(API.AccountRemove.self, AccountRemoveParams(accountId: id, deleteLocalData: answer.deleteLocalData), timeout: RPCTimeouts.default)
            } catch {
                failure = error
            }
            guard !self.closed else { return }
            self.pending.remove(id)
            if let failure {
                self.reloadRow(id)
                self.toast(rpcErrorText(L10n.T("Removing the account"), failure))
                return
            }
            self.loadAccounts()
        }
    }

    // MARK: Reordering (accounts_reorder.go)

    /// Shifts the selected account by `delta` positions (the keyboard path).
    private func moveSelected(by delta: Int) {
        let row = table.selectedRow
        guard row >= 0, row < accounts.count else { return }
        moveAccount(accounts[row].id, by: delta)
    }

    /// accounts_reorder.go `moveAccountBy`.
    func moveAccount(_ id: AccountID, by delta: Int) {
        guard let from = index(of: id) else { return }
        let to = from + delta
        guard to >= 0, to < accounts.count else { return }
        reorderAccounts(from: from, to: to, focusMoved: true)
    }

    /// Moves the row at `from` to `to`: the rows are rebuilt at once so the
    /// gesture feels immediate, then account.reorder saves the order and
    /// the daemon's notify.accountsChanged reorders the main window's
    /// sidebar. A failure reloads the page, because the daemon's order is
    /// the truth (accounts_reorder.go `reorderAccounts`).
    private func reorderAccounts(from: Int, to: Int, focusMoved: Bool) {
        guard from != to, from >= 0, to >= 0, from < accounts.count, to < accounts.count else { return }
        let moved = accounts[from].id
        let reordered = MalachiCore.moveAccount(accounts, from: from, to: to)
        setAccounts(reordered)
        if focusMoved, let i = index(of: moved) {
            table.selectRowIndexes(IndexSet(integer: i), byExtendingSelection: false)
            view.window?.makeFirstResponder(table)
        }
        group.isEnabled = false
        let ids = reordered.map(\.id)
        let client = client
        Task { [weak self] in
            var failure: (any Error)?
            do {
                _ = try await client.call(API.AccountReorder.self, AccountReorderParams(accountIds: ids), timeout: RPCTimeouts.default)
            } catch {
                failure = error
            }
            guard let self, !self.closed else { return }
            self.group.isEnabled = true
            if let failure {
                self.toast(rpcErrorText(L10n.T("Saving the account order"), failure))
                self.loadAccounts()
            }
        }
    }

    // MARK: NSTableViewDataSource

    func numberOfRows(in tableView: NSTableView) -> Int {
        accounts.count
    }

    func tableView(_ tableView: NSTableView, pasteboardWriterForRow row: Int) -> (any NSPasteboardWriting)? {
        guard row < accounts.count, table.mouseDownOnHandle(row: row) else { return nil }
        let item = NSPasteboardItem()
        item.setString(accounts[row].id.rawValue, forType: .string)
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

    /// The payload is one of our account ids; text dragged in from another
    /// application ends up here too and is refused (accounts_reorder.go
    /// `dropTarget`).
    private func draggedIndex(_ info: any NSDraggingInfo) -> Int? {
        guard let id = info.draggingPasteboard.string(forType: .string) else { return nil }
        return index(of: AccountID(rawValue: id))
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
            self.reorderAccounts(from: from, to: to, focusMoved: false)
        }
        return true
    }

    // MARK: NSTableViewDelegate

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        guard row < accounts.count else { return nil }
        let account = accounts[row]
        let cell = (tableView.makeView(withIdentifier: AccountRowCell.reuseIdentifier, owner: nil) as? AccountRowCell)
            ?? AccountRowCell(frame: .zero)
        cell.apply(account)
        cell.setRowEnabled(!pending.contains(account.id))
        cell.setGroupEnabled(group.isEnabled)
        let id = account.id
        cell.onToggle = { [weak self] want in self?.setAccountEnabled(id, want) }
        cell.onEdit = { [weak self] in self?.editAccount(id) }
        cell.onSignIn = { [weak self] in self?.editAccount(id, signIn: true) }
        cell.onRemove = { [weak self] in self?.removeAccount(id) }
        return cell
    }

    func tableView(_ tableView: NSTableView, heightOfRow row: Int) -> CGFloat {
        AccountsTableView.rowHeight
    }
}
