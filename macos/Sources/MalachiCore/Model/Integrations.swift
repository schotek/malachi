// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The view logic of the two account pages of the settings, a 1:1 port of
/// ui/internal/integrations: Accounts, which lists the mail accounts (kind
/// imap, also the empty kind, and graph), and Integrations, which lists
/// every other account — the services beside the mail, today Jira and
/// CalDAV calendars; a kind added later lands there by itself — and the
/// catalogue of services that can be added.
///
/// Both pages show rows of the same shape (`Row`): the switch, the editor
/// and the sentence of the remove confirmation depend on the kind. The
/// order of the accounts is one list owned by the daemon; a page that
/// reorders its own rows sends the whole list (`fullOrder`), mail accounts
/// first, so account.reorder never has to guess where the other page's
/// accounts go. Texts go through `L10n` with the GTK msgids.
public enum Integrations {
    /// integrations.ShowsInAccounts: a mail account (imap, the empty kind,
    /// graph).
    public static func showsInAccounts(_ a: Account) -> Bool {
        let kind = a.config.protocolKind
        return kind == .imap || kind == .graph
    }

    /// integrations.ShowsInIntegrations: every account that is not a mail
    /// account.
    public static func showsInIntegrations(_ a: Account) -> Bool {
        !showsInAccounts(a)
    }

    /// integrations.Split: the mail accounts and the integrations, each in
    /// the order of `accounts`.
    public static func split(_ accounts: [Account]) -> (mail: [Account], integrations: [Account]) {
        var mail: [Account] = []
        var other: [Account] = []
        for a in accounts {
            if showsInAccounts(a) {
                mail.append(a)
            } else {
                other.append(a)
            }
        }
        return (mail, other)
    }

    /// integrations.Page: one of the two account pages.
    public enum Page: Sendable, Equatable {
        case accounts
        case integrations
    }

    /// integrations.FullOrder: the order account.reorder gets when `page`
    /// was reordered to `order`: the mail accounts first, then the
    /// integrations. The reordered page's accounts follow `order`; the
    /// other page's keep their order in `all`. An id of `order` that is not
    /// in `all`, or that belongs to the other page, is ignored, a repeated
    /// one counts once, and an account of the page that `order` omits
    /// follows the listed ones in its order in `all`. The result names
    /// every account of `all` exactly once.
    public static func fullOrder(_ all: [Account], page: Page, order: [AccountID]) -> [AccountID] {
        let (mail, other) = split(all)
        func ordered(_ group: [Account], reordered: Bool) -> [AccountID] {
            guard reordered else { return group.map(\.id) }
            let inGroup = Set(group.map(\.id))
            var seen = Set<AccountID>()
            var ids: [AccountID] = []
            for id in order where inGroup.contains(id) && !seen.contains(id) {
                seen.insert(id)
                ids.append(id)
            }
            for a in group where !seen.contains(a.id) {
                seen.insert(a.id)
                ids.append(a.id)
            }
            return ids
        }
        return ordered(mail, reordered: page == .accounts) + ordered(other, reordered: page == .integrations)
    }

    // Icons of the kinds: the GTK symbolic icon names, which the AppKit
    // side maps to SF Symbols. A mail account's row shows the envelope.
    public static let mailIcon = "mail-unread-symbolic"
    public static let jiraIcon = "checkbox-checked-symbolic"
    public static let calDAVIcon = "x-office-calendar-symbolic"

    /// integrations.Service: one entry of the catalogue.
    public struct Service: Sendable, Equatable {
        public var kind: AccountKind
        /// The service's name; Jira is a brand, not translated.
        public var name: String
        public var description: String
        /// The GTK symbolic icon name.
        public var icon: String
    }

    /// integrations.Catalog: the services that can be added, in the order
    /// the page lists them.
    public static func catalog() -> [Service] {
        [
            Service(
                kind: .jira, name: jiraName,
                description: L10n.T("Issues read as mail, comments and status changes"), icon: jiraIcon),
            Service(
                kind: .caldav, name: L10n.T("CalDAV Calendar"),
                description: L10n.T("Nextcloud, iCloud, Fastmail and other CalDAV servers"), icon: calDAVIcon),
        ]
    }

    /// The brand, never translated.
    static let jiraName = "Jira"

    /// integrations.Editor: what a row's Edit button opens.
    public enum Editor: Sendable, Equatable {
        /// No Edit button (a kind this client does not know).
        case none
        /// The mail account assistant in its edit mode.
        case mailWizard
        /// A Jira account's settings.
        case jira
        /// The Calendar page of the settings, where a CalDAV account's
        /// server, password, certificate and calendars are.
        case calendarPage
    }

    /// integrations.Row: what an account's row on either page shows besides
    /// its status. Plain texts; nothing is markup.
    public struct Row: Sendable, Equatable {
        public var id: AccountID
        public var kind: AccountKind
        public var title: String
        public var subtitle: String
        public var icon: String
        /// The Enabled switch (account.setEnabled pauses the sync; CalDAV
        /// has no pause).
        public var offersSwitch: Bool
        public var editor: Editor
        /// The Edit button's tooltip.
        public var editLabel: String
        public var removeBody: String
    }

    /// integrations.RowFor. Title: the account name as the user typed it
    /// (nothing trimmed), else the Jira site's host, else the address.
    /// Subtitle: the host of the Jira site or of the CalDAV server, else —
    /// and when the title shows the host already — the address.
    public static func rowFor(_ a: Account) -> Row {
        let kind = a.config.protocolKind
        let t = title(a)
        var r = Row(
            id: a.id, kind: kind, title: t, subtitle: subtitle(a), icon: mailIcon, offersSwitch: true,
            editor: .none, editLabel: "", removeBody: "")
        switch kind {
        case .imap, .graph:
            r.editor = .mailWizard
            r.editLabel = L10n.T("Edit Account")
            // TRANSLATORS: %s is the account's e-mail address.
            r.removeBody = L10n.T("%s will be removed from Malachi Mail. Mail on the server is not affected.", a.config.email)
        case .jira:
            r.icon = jiraIcon
            r.editor = .jira
            r.editLabel = L10n.T("Edit Account")
            // TRANSLATORS: %s is the account's name (or its site's host).
            r.removeBody = L10n.T("%s will be removed from Malachi Mail. Issues on the Jira site are not affected.", t)
        case .caldav:
            r.icon = calDAVIcon
            r.editor = .calendarPage
            r.editLabel = calendarSettings
            r.offersSwitch = false
            // TRANSLATORS: %s is the calendar account's name.
            r.removeBody = L10n.T("%s will be removed from Malachi Mail. Calendars on the server are not affected.", t)
        default:
            // TRANSLATORS: %s is the account's name.
            r.removeBody = L10n.T("%s will be removed from Malachi Mail.", t)
        }
        return r
    }

    /// The name of the row: the name, the Jira site's host, the address.
    static func title(_ a: Account) -> String {
        if !a.config.name.isEmpty {
            return a.config.name
        }
        let h = Jira.siteHost(a.config)
        if !h.isEmpty {
            return h
        }
        return a.config.email
    }

    /// The line under the name: the host, unless the title shows it
    /// already; else the address.
    static func subtitle(_ a: Account) -> String {
        let h = host(a)
        return !h.isEmpty && h != title(a) ? h : a.config.email
    }

    /// The host the subtitle shows: a Jira account's site, a CalDAV
    /// account's server; empty for anything else.
    static func host(_ a: Account) -> String {
        switch a.config.protocolKind {
        case .jira:
            return Jira.siteHost(a.config)
        case .caldav:
            guard let c = a.config.caldav, let u = Jira.parseURL(Jira.trimSpace(c.serverUrl)) else { return "" }
            return Jira.clean(u.hostname.lowercased())
        default:
            return ""
        }
    }

    /// integrations.PageTexts: the fixed texts of the Integrations page.
    public struct PageTexts: Sendable, Equatable {
        /// The page's title in the settings' toolbar.
        public var title: String
        /// Under the title.
        public var description: String
        /// The group of the added services.
        public var connectedTitle: String
        /// The group of the catalogue.
        public var availableTitle: String
        /// The connected group's placeholder row.
        public var emptyTitle: String
        public var emptySubtitle: String
        /// A catalogue row's button.
        public var add: String
        /// A CalDAV row's Edit tooltip.
        public var calendarSettings: String
    }

    /// integrations.Texts.
    public static func texts() -> PageTexts {
        PageTexts(
            title: L10n.T("Integrations"),
            description: L10n.T("Services beside your mail: issue trackers and calendars"),
            connectedTitle: L10n.T("Connected Services"),
            availableTitle: L10n.T("Available Services"),
            emptyTitle: L10n.T("No integrations yet"),
            emptySubtitle: L10n.T("Add one from the list below"),
            add: L10n.T("Add…"),
            calendarSettings: calendarSettings)
    }

    /// What a CalDAV row's Edit opens.
    static var calendarSettings: String {
        L10n.T("Calendar Settings")
    }
}
