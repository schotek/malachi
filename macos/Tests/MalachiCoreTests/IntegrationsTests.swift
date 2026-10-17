// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// The counterpart of ui/internal/integrations/integrations_test.go.
@Suite struct IntegrationsTests {
    /// integrations_test.go `account`: nil kind is the empty kind.
    private func account(_ id: String, _ kind: AccountKind?) -> Account {
        var cfg = AccountConfig(name: "", email: id + "@example.invalid", kind: kind)
        if kind == .jira {
            cfg.jira = JiraConfig(siteUrl: "https://Acme.atlassian.net/", deployment: .cloud, login: id + "@example.invalid")
        } else if kind == .caldav {
            cfg.caldav = CalDAVConfig(serverUrl: "https://Cloud.example.org/remote.php/dav/", username: id)
        }
        return Account(id: AccountID(id), config: cfg, enabled: true, state: SyncState(accountId: AccountID(id), status: .idle))
    }

    private var all: [Account] {
        [
            account("j1", .jira), account("m1", nil), account("c1", .caldav),
            account("m2", .graph), account("j2", .jira), account("m3", .imap),
        ]
    }

    @Test func predicatesByKind() {
        let cases: [(AccountKind?, Bool)] = [
            (nil, true), (.imap, true), (.graph, true),
            (.jira, false), (.caldav, false), (AccountKind(rawValue: "future"), false),
        ]
        for (kind, mail) in cases {
            let a = account("a", kind)
            #expect(Integrations.showsInAccounts(a) == mail, "\(String(describing: kind))")
            #expect(Integrations.showsInIntegrations(a) == !mail, "\(String(describing: kind))")
        }
    }

    @Test func splitKeepsTheOrder() {
        let (mail, other) = Integrations.split(all)
        #expect(mail.map(\.id) == ["m1", "m2", "m3"])
        #expect(other.map(\.id) == ["j1", "c1", "j2"])
        let (m, o) = Integrations.split([])
        #expect(m.isEmpty && o.isEmpty)
    }

    @Test func fullOrder() {
        let cases: [(String, Integrations.Page, [AccountID], [AccountID])] = [
            ("accounts reordered", .accounts, ["m3", "m1", "m2"], ["m3", "m1", "m2", "j1", "c1", "j2"]),
            ("integrations reordered", .integrations, ["j2", "c1", "j1"], ["m1", "m2", "m3", "j2", "c1", "j1"]),
            ("unknown and other page's ids ignored", .accounts, ["x", "j1", "m2", "m2", "m1", "m3"],
             ["m2", "m1", "m3", "j1", "c1", "j2"]),
            ("omitted keep their order", .integrations, ["j2"], ["m1", "m2", "m3", "j2", "j1", "c1"]),
            ("empty order", .accounts, [], ["m1", "m2", "m3", "j1", "c1", "j2"]),
        ]
        for (name, page, order, want) in cases {
            let got = Integrations.fullOrder(all, page: page, order: order)
            #expect(got == want, "\(name)")
            #expect(Set(got).count == all.count && got.count == all.count, "\(name): not every id once")
        }
        #expect(Integrations.fullOrder([], page: .accounts, order: ["a"]).isEmpty)
    }

    @Test func catalog() throws {
        let c = Integrations.catalog()
        try #require(c.count == 2)
        #expect(c[0].kind == .jira && c[1].kind == .caldav)
        #expect(c[0].name == "Jira" && c[0].icon == Integrations.jiraIcon)
        #expect(c[1].name == "CalDAV Calendar" && c[1].icon == Integrations.calDAVIcon)
        for s in c {
            #expect(!s.description.isEmpty, "\(s.kind)")
        }
    }

    @Test func rowForByKind() {
        var mail = account("m", nil)
        mail.config.name = "Work"
        var r = Integrations.rowFor(mail)
        #expect(r.id == "m" && r.kind == .imap && r.title == "Work" && r.subtitle == "m@example.invalid")
        #expect(r.offersSwitch && r.editor == .mailWizard && r.editLabel == "Edit Account" && r.icon == Integrations.mailIcon)
        #expect(r.removeBody.contains("Mail on the server") && r.removeBody.hasPrefix("m@example.invalid "))
        let unnamed = Integrations.rowFor(account("g", .graph))
        #expect(unnamed.title == "g@example.invalid" && unnamed.subtitle == "g@example.invalid" && unnamed.editor == .mailWizard)

        var j = account("j", .jira)
        r = Integrations.rowFor(j)
        #expect(r.title == "acme.atlassian.net" && r.subtitle == "j@example.invalid" && r.offersSwitch)
        #expect(r.editor == .jira && r.icon == Integrations.jiraIcon && r.editLabel == "Edit Account")
        #expect(r.removeBody == "acme.atlassian.net will be removed from Malachi Mail. Issues on the Jira site are not affected.")
        j.config.name = "Acme"
        r = Integrations.rowFor(j)
        #expect(r.title == "Acme" && r.subtitle == "acme.atlassian.net")

        var c = account("c", .caldav)
        c.config.name = "Nextcloud"
        r = Integrations.rowFor(c)
        #expect(r.title == "Nextcloud" && r.subtitle == "cloud.example.org" && !r.offersSwitch)
        #expect(r.editor == .calendarPage && r.editLabel == "Calendar Settings" && r.icon == Integrations.calDAVIcon)
        #expect(r.removeBody == "Nextcloud will be removed from Malachi Mail. Calendars on the server are not affected.")
        c.config.caldav = nil
        c.config.name = ""
        r = Integrations.rowFor(c)
        #expect(r.title == "c@example.invalid" && r.subtitle == "c@example.invalid")

        let f = Integrations.rowFor(account("f", AccountKind(rawValue: "future")))
        #expect(f.editor == .none && f.offersSwitch && f.removeBody == "f@example.invalid will be removed from Malachi Mail.")
    }

    /// The title and subtitle helpers the other screens use are the row's.
    @Test func rowTitleHelpersAreTheRows() {
        for a in all {
            #expect(accountRowTitle(a) == Integrations.rowFor(a).title)
            #expect(accountRowSubtitle(a) == Integrations.rowFor(a).subtitle)
        }
    }

    @Test func texts() {
        let x = Integrations.texts()
        for s in [x.title, x.description, x.connectedTitle, x.availableTitle, x.emptyTitle, x.emptySubtitle, x.add, x.calendarSettings] {
            #expect(!s.isEmpty)
        }
        #expect(x.title == "Integrations" && x.add == "Add…" && x.calendarSettings == "Calendar Settings")
    }
}
