// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Integrations page of the settings (preferences.blp
/// `integrations_page`, ui/internal/window/integrations_page.go): the
/// page's description, the accounts that are not mail accounts as
/// Connected Services (the Accounts page's rows, `AccountsPaneViewController`
/// with page `.integrations`: a Jira row edits its settings, a CalDAV row
/// shows the Calendar page, which keeps the CalDAV account's server,
/// password, certificate and calendars) and the catalogue of services
/// that can be added (Available Services, `Integrations.catalog`), each
/// with Add… opening its assistant.
@MainActor
final class IntegrationsPaneViewController: AccountsPaneViewController {
    /// The calendar source the Add CalDAV sheet works over (the Calendar
    /// page's); nil without one, and Add… then only says so.
    var calendarSource: (() -> (any CalendarDataSource)?)?
    /// The rows of the catalogue by kind (`MALACHI_START` prefs-integrations).
    private(set) var serviceRows: [(kind: AccountKind, view: NSView)] = []

    init(
        client: RPCClient, confirmRemoval: @escaping PrefsConfirmRemoval, confirmTrust: @escaping WizardConfirmTrust,
        toast: @escaping @MainActor (String) -> Void
    ) {
        super.init(page: .integrations, client: client, confirmRemoval: confirmRemoval, confirmTrust: confirmTrust, toast: toast)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The page's description under the toolbar title, muted, as an
    /// `AdwPreferencesPage` shows its description.
    override func addLeadingViews() {
        let label = PrefsWrappingLabel(Integrations.texts().description, size: 13, color: .secondaryLabelColor)
        prefsAddFilling(label, to: contentStack)
    }

    /// Available Services: a row per catalogue entry with its Add… button.
    override func addTrailingGroups() {
        let tx = Integrations.texts()
        let available = PreferencesGroupView(title: tx.availableTitle)
        var rows: [NSView] = []
        for s in Integrations.catalog() {
            let button = NSButton(title: tx.add, target: self, action: #selector(addService(_:)))
            button.bezelStyle = .rounded
            button.identifier = NSUserInterfaceItemIdentifier(s.kind.rawValue)
            let icon = NSImageView(image: wizardSymbol(AccountRowCell.symbol(for: s.icon), pointSize: 15))
            icon.contentTintColor = .labelColor
            let row = PreferenceRowView(title: s.name, subtitle: s.description, prefix: icon, trailing: button)
            rows.append(row)
            serviceRows.append((s.kind, row))
        }
        available.setRows(rows)
        addGroup(available)
    }

    @objc private func addService(_ sender: NSButton) {
        switch AccountKind(rawValue: sender.identifier?.rawValue ?? "") {
        case .jira: addJira()
        case .caldav: addCalDAV()
        default: break
        }
    }

    /// The Jira account assistant (the File menu's Add Jira Account… too).
    private func addJira() {
        guard let window = view.window, window.attachedSheet == nil else { return }
        JiraWizardWindowController.present(from: window, client: client) { [weak self] id, cfg in
            self?.added(id, cfg)
        }
    }

    /// The Add CalDAV Calendar sheet over the settings, as the Calendar
    /// page's Add CalDAV Calendar… opens it; the pages reload once it
    /// added the account.
    private func addCalDAV() {
        guard let window = view.window, window.attachedSheet == nil else { return }
        guard let source = calendarSource?() else {
            toast(Cal.Text.previewOnly)
            return
        }
        CalDAVSheetController.present(from: window, source: source, confirmTrust: confirmTrust) { [weak self] _ in
            guard let self, !self.closed else { return }
            self.loadAccounts()
            self.onAccountsChanged?()
        }
    }
}
