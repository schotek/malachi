// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// Jira accounts in Settings → Accounts (AccountsPage.swift) and in the
// sign-in banner (SyncStatus.swift, SyncController): the Swift-first
// branches, to mirror in accounts_page_test.go and sync_test.go when GTK
// gets Jira accounts.

/// A Jira account of these tests.
private func jiraAccount(_ id: String = "j1", name: String = "Acme", site: String = "https://acme.atlassian.net") -> Account {
    Account(
        id: AccountID(id),
        config: AccountConfig(
            name: name, email: "jana@acme.example", kind: .jira,
            jira: JiraConfig(siteUrl: site, deployment: .cloud, login: "jana@acme.example")
        ),
        enabled: true,
        state: SyncState(accountId: AccountID(id), status: .authRequired),
        capabilities: []
    )
}

@MainActor
@Suite struct JiraAccountsTests {
    @Test func rowTitleAndSubtitle() {
        var a = jiraAccount()
        #expect(accountRowTitle(a) == "Acme")
        #expect(accountRowSubtitle(a) == "acme.atlassian.net")
        // Unnamed: the host is the title, the address under it.
        a.config.name = ""
        #expect(accountRowTitle(a) == "acme.atlassian.net")
        #expect(accountRowSubtitle(a) == "jana@acme.example")
        // A site that is no URL: the address.
        a.config.jira?.siteUrl = "::"
        #expect(accountRowTitle(a) == "jana@acme.example")
        #expect(accountRowSubtitle(a) == "jana@acme.example")

        // Mail accounts as before.
        var m = testAccount("m", name: "Work", email: "me@example.invalid")
        #expect(accountRowTitle(m) == "Work")
        #expect(accountRowSubtitle(m) == "me@example.invalid")
        m.config.name = ""
        #expect(accountRowTitle(m) == "me@example.invalid")
        #expect(accountRowSubtitle(m) == "me@example.invalid")
    }

    @Test func editorByKind() {
        #expect(accountEditor(jiraAccount()) == .jira)
        #expect(accountEditor(testAccount("m")) == .mailWizard)
        var graph = testAccount("g")
        graph.config.kind = .graph
        #expect(accountEditor(graph) == .mailWizard)
        // The row offers no browser sign-in for a Jira account.
        #expect(!accountRowOffersSignIn(jiraAccount()))
    }

    @Test func authBannerNamesTheToken() {
        #expect(authBannerText(.authRequired, "Acme", kind: .jira) == "No API token is stored for Acme")
        #expect(authBannerText(.authFailed, "Acme", kind: .jira) == "The Jira site rejected the token of Acme")
        #expect(authBannerText(.keyringError, "Acme", kind: .jira) == "The system keyring is unavailable; Acme cannot sign in")
        #expect(authBannerText(.networkError, "Acme", kind: .jira) == "Acme needs attention")
        // Mail accounts, and no kind, keep the password's sentences.
        #expect(authBannerText(.authRequired, "Work", kind: .imap) == "No password is stored for Work")
        #expect(authBannerText(.authFailed, "Work") == "The server rejected the password of Work")
    }

    @Test func syncControllerBannerOfAJiraAccount() {
        let sc = SyncController()
        let a = jiraAccount()
        func n(_ reason: ErrorCode) -> AuthRequiredNotification {
            AuthRequiredNotification(accountId: a.id, reason: reason, message: "detail")
        }
        // The token is entered again in the account's own assistant.
        #expect(sc.authBanner(for: n(.authRequired), account: a) == ("No API token is stored for Acme", "Edit Account…"))
        #expect(sc.authBanner(for: n(.authFailed), account: a) == ("The Jira site rejected the token of Acme", "Edit Account…"))
        #expect(sc.authBanner(for: n(.keyringError), account: a) == ("The system keyring is unavailable; Acme cannot sign in", "Open Preferences"))
        #expect(sc.authBannerAction(for: n(.authFailed), account: a) == .editAccount(a.id, reason: .authFailed))
        #expect(sc.authBannerAction(for: n(.authRequired), account: a) == .editAccount(a.id, reason: .authRequired))
    }
}
