// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The shell's part of eWay-CRM (docs/eway.md §2.7): the application's
/// `CRMState` over the mailbox's accounts, the folders' roles and the
/// message cache; the main window's `CRMController` over the selection
/// (each message window makes its own over its message); the action flags
/// of the selection (`ActionsController.crm`); and the redraw of the
/// buttons and bars when a save or removal begins or ends and when the
/// accounts change. The list's pill, the bars and the buttons follow the
/// daemon's notify.messagesChanged like every other part of a message.
extension Integration {
    func wireCRM() {
        let crm = state.crm
        crm.accounts = { [weak self] in self?.mailbox.model.accounts ?? [] }
        crm.folderRole = { [weak self] s in self?.messageActions.folderRole(of: s) ?? .none }
        crm.loaded = { [weak self] id in self?.cache.loaded(id)?.msg }
        actions.crm = crm
        if let mainWindow {
            let c = CRMController(client: state.client, state: crm)
            // One message per selected row: the row's (a conversation's
            // newest member in the folder), as Reply takes it.
            c.subjects = { [weak self] in self?.list.selectedRows.map(\.message) ?? [] }
            c.conversations = { [weak self] in Set((self?.list.selectedRows ?? []).filter(\.thread).map(\.message.id)) }
            CRMWindowSupport.configure(c, state: state, window: { [weak mainWindow] in mainWindow?.window }, toast: mainToast)
            mainWindow.crm = c
        }
        _ = crm.observe { [weak self] in self?.refreshCRM() }
        _ = state.notifications.addAccountsChanged { [weak self] in self?.refreshCRM() }
        _ = state.notifications.addMessagesChanged { [weak self] _ in self?.refreshCRM() }
        // The toolbar's button follows the selection and the loaded
        // message explicitly: on macOS 15 a hidden toolbar item is left
        // out of the window's validation (`validateVisibleItems`), so a
        // button hidden at start (no selection yet) would never come back
        // when a message is selected (seen at Gate B, 2026-10-17). The
        // handlers installed before keep running first.
        let selectedRow = listView.onSelectedRowChanged
        listView.onSelectedRowChanged = { [weak self] row in
            selectedRow?(row)
            self?.refreshCRM()
        }
        let several = list.onSeveralSelected
        list.onSeveralSelected = { [weak self] n in
            several?(n)
            self?.refreshCRM()
        }
        let loaded = cache.onLoaded
        cache.onLoaded = { [weak self] id, m in
            loaded?(id, m)
            self?.refreshCRM()
        }
        // The accounts arrive after the wiring (account.list), and the
        // eWay-CRM account among them decides whether anything is shown.
        let accountsLoaded = mailbox.onAccountsLoaded
        mailbox.onAccountsLoaded = { [weak self] accounts in
            accountsLoaded?(accounts)
            self?.refreshCRM()
        }
        refreshCRM()
    }

    /// Every button and bar of eWay-CRM again.
    func refreshCRM() {
        mainWindow?.refreshCRM()
        mainWindow?.mailToolbar.validateVisibleItems()
        for wc in windows.messageWindows {
            wc.refreshCRM()
        }
        for v in windows.views {
            v.refreshCRM()
        }
    }
}
