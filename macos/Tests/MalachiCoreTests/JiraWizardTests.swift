// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/jira/wizard_test.go (English catalogue). The Czech case of
// TestWizardTexts is in JiraTranslationTests.

private func detect(
    _ d: JiraDeployment, title: String? = nil, version: String? = nil, site: String = "", cloudId: String? = nil
) -> AccountDetectSiteResult {
    AccountDetectSiteResult(kind: .jira, siteUrl: site, deployment: d, cloudId: cloudId, title: title, version: version)
}

struct JiraWizardTests {
    @Test func wizardTexts() {
        let s = Jira.wizardTexts()
        for child in Mirror(reflecting: s).children {
            #expect((child.value as? String)?.isEmpty == false, "WizardStrings.\(child.label ?? "?") is empty")
        }
        #expect(Mirror(reflecting: s).children.count == 16)
        #expect(s.addMenu == "Add _Jira Account…")
        #expect(s.spacesTitle == "Spaces")
        #expect(s.adding == "Adding the account")
    }

    @Test func credentialFields() {
        let cloud = Jira.credentialFields(.cloud)
        let want = Jira.CredentialPage(
            showsLogin: true, loginLabel: "E-mail Address", tokenLabel: "API Token",
            help: "Create an API token for Malachi Mail in your Atlassian account, then paste it here.",
            helpButton: "Create API Token…", helpURL: "https://id.atlassian.com/manage-profile/security/api-tokens",
            tokenPrompt: "Enter the API token for this account", rejected: "The Jira site rejected the token")
        #expect(cloud == want)

        let dc = Jira.credentialFields(.datacenter)
        #expect(!dc.showsLogin)
        #expect(dc.tokenLabel == "Personal Access Token")
        #expect(dc.helpURL == "" && dc.helpButton == "")
        #expect(dc.help == "Create a personal access token in your Jira profile, then paste it here.")
        #expect(dc.tokenPrompt == "Enter the personal access token for this account")
        #expect(Jira.credentialFields("server") == dc, "an unknown deployment gets the Data Center page")
    }

    @Test func needsEmail() {
        let cases: [(JiraDeployment, SiteUser, Bool)] = [
            (.cloud, SiteUser(name: "Jana"), false),
            (.datacenter, SiteUser(name: "Jana"), true),
            (.datacenter, SiteUser(name: "Jana", email: " "), true),
            (.datacenter, SiteUser(name: "Jana", email: "jana@acme.example"), false),
        ]
        for (i, c) in cases.enumerated() {
            #expect(Jira.needsEmail(c.0, c.1) == c.2, "case \(i)")
        }
    }

    @Test func checkSiteInput() {
        let bad = "This is not a web address"
        let cases: [(String, Bool, String)] = [
            ("", false, ""),
            ("   ", false, ""),
            ("acme.atlassian.net", true, ""),
            (" acme.atlassian.net ", true, ""),
            ("https://acme.atlassian.net", true, ""),
            ("HTTPS://acme.atlassian.net/", true, ""),
            ("http://jira.local:8080/jira", true, ""),
            ("jira", true, ""),
            ("jira.acme.example:8443/jira", true, ""),
            ("ftp://acme.atlassian.net", false, bad),
            ("javascript://acme.atlassian.net", false, bad),
            ("acme atlassian.net", false, bad),
            ("acme.atlassian.net\\x", false, bad),
            ("jana@acme.atlassian.net", false, bad),
            ("https://jana:secret@acme.atlassian.net", false, bad),
            ("https://", false, bad),
            ("https:///browse", false, bad),
            ("acme" + jiraZWSP + ".atlassian.net", false, bad),
            ("https://[bad", false, bad),
            ("://acme.atlassian.net", false, bad),
        ]
        for (input, ok, problem) in cases {
            let got = Jira.checkSiteInput(input)
            #expect(got.ok == ok && got.problem == problem, "checkSiteInput(\(input.debugDescription)) = \(got)")
        }
    }

    @Test func detected() {
        let cases: [(AccountDetectSiteResult, String)] = [
            (detect(.cloud, title: "Acme", version: "1001.0.0-SNAPSHOT"), "Found Acme"),
            (detect(.cloud), "Found Jira Cloud"),
            (detect(.datacenter, title: "Acme Jira", version: "9.12.4"), "Found Acme Jira, version 9.12.4"),
            (detect(.datacenter, version: "9.12.4"), "Found Jira Data Center, version 9.12.4"),
            (detect(.datacenter, title: "Acme" + jiraRLO + "\n"), "Found Acme"),
            (detect("", title: jiraZWSP), "Found Jira"),
        ]
        for (res, want) in cases {
            #expect(Jira.detected(res) == want)
        }
    }

    @Test func defaultAccountName() {
        let cases: [(AccountDetectSiteResult, String)] = [
            (detect(.cloud, title: " Acme Jira ", site: "https://acme.atlassian.net"), "Acme Jira"),
            (detect(.cloud, site: "https://ACME.atlassian.net"), "acme.atlassian.net"),
            (detect(.datacenter, title: jiraRLO, site: "https://jira.acme.example:8443/jira"), "jira.acme.example"),
            (detect(.cloud, site: "https://[bad"), "Jira"),
            (detect(""), "Jira"),
        ]
        for (res, want) in cases {
            #expect(Jira.defaultAccountName(res) == want, "\(res.siteUrl)")
        }
        let long = Jira.defaultAccountName(detect(.cloud, title: String(repeating: "ř", count: 300)))
        #expect(long.utf8.count <= Jira.maxNameBytes && long.utf8.count >= Jira.maxNameBytes - 1,
                "a long title makes a name of \(long.utf8.count) bytes")
    }

    @Test func spaces() {
        let titles: [(Space, String)] = [
            (Space(id: "", key: "ITSD", name: "IT Service Desk"), "ITSD – IT Service Desk"),
            (Space(id: "", key: "WEB", name: ""), "WEB"),
            (Space(id: "", key: "", name: "Mobile"), "Mobile"),
            (Space(id: "", key: " MOB ", name: "Mobile\napp" + jiraZWSP), "MOB – Mobile app"),
            (Space(id: "", key: "", name: ""), ""),
        ]
        for (s, want) in titles {
            #expect(Jira.spaceTitle(s) == want, "\(s)")
        }
        let counts: [(Int, String)] = [(-1, ""), (0, "about 0 issues"), (1, "about 1 issue"), (5, "about 5 issues")]
        for (n, want) in counts {
            #expect(Jira.approxCount(n) == want, "\(n)")
        }
        let rows = Jira.spaceRows([
            Space(id: "10001", key: "ITSD", name: "IT Service Desk", serviceDesk: true, issues: 42),
            Space(id: "10002", key: "WEB", name: "Website", issues: -1),
        ])
        #expect(rows == [
            Jira.SpaceRow(id: "10001", title: "ITSD – IT Service Desk", count: "about 42 issues", serviceDesk: true),
            Jira.SpaceRow(id: "10002", title: "WEB – Website"),
        ])
        #expect(Jira.spaceRows([]).isEmpty)
        let problems: [(Int, String)] = [
            (-1, "Select at least one space"), (0, "Select at least one space"), (1, ""), (API.Limits.maxJiraSpaces, ""),
            (API.Limits.maxJiraSpaces + 1, "Select at most \(API.Limits.maxJiraSpaces) spaces"),
        ]
        for (n, want) in problems {
            #expect(Jira.spacesProblem(n) == want, "\(n)")
        }
    }

    @Test func offlineChoices() {
        let labels = Jira.offlineChoiceLabels()
        #expect(labels.count == Jira.offlineChoices.count)
        #expect(labels.first == "1 week" && labels.last == "1 year", "\(labels)")
        for d in Jira.offlineChoices {
            #expect(d > 0 && d <= API.Limits.maxJiraOfflineDays, "choice \(d) is outside 1...\(API.Limits.maxJiraOfflineDays)")
        }
        #expect(Jira.offlineChoices[Jira.indexOfOfflineDays(0)] == API.Limits.defaultJiraOfflineDays,
                "0 selects the default window")
        let cases: [(Int, Int)] = [
            (-5, 1), (0, 1), (1, 0), (7, 0), (18, 0), (19, 1), (30, 1), (60, 1), (61, 2), (90, 2), (227, 2), (228, 3),
            (365, 3), (1000, 3),
        ]
        for (days, want) in cases {
            #expect(Jira.indexOfOfflineDays(days) == want, "\(days)")
        }
    }

    @Test func setupConfig() {
        let spaces = [
            Space(id: "10001", key: "ITSD", name: "IT Service Desk", serviceDesk: true, issues: 42),
            Space(id: "10002", key: "WEB", name: "Website", issues: -1),
        ]
        let cloud = Jira.Setup(
            site: AccountDetectSiteResult(
                kind: .jira, siteUrl: "https://acme.atlassian.net", deployment: .cloud,
                cloudId: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", title: "Acme"),
            login: " jana@acme.example ", email: "ignored@acme.example", spaces: spaces, onlyMine: true, offlineDays: 90)
        let want = AccountConfig(
            name: "Acme", email: "jana@acme.example", kind: .jira,
            jira: JiraConfig(
                siteUrl: "https://acme.atlassian.net", deployment: .cloud,
                cloudId: "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", login: "jana@acme.example",
                spaces: [SpaceRef(id: "10001", key: "ITSD", name: "IT Service Desk"), SpaceRef(id: "10002", key: "WEB", name: "Website")],
                offlineDays: 90, onlyMine: true))
        #expect(cloud.config() == want)

        let dc = Jira.Setup(
            site: AccountDetectSiteResult(kind: "", siteUrl: "https://jira.acme.example/jira", deployment: .datacenter, cloudId: "stray"),
            login: "jana", email: " jana@acme.example ", name: " Work Jira ", offlineDays: 9999)
        let got = dc.config()
        #expect(got.name == "Work Jira")
        #expect(got.email == "jana@acme.example")
        #expect(got.kind == .jira)
        #expect(got.jira?.login == nil)
        #expect(got.jira?.cloudId == nil)
        #expect(got.jira?.offlineDays == API.Limits.maxJiraOfflineDays)
        #expect(got.jira?.spaces == [])
        #expect(got.imap == nil && got.smtp == nil && got.graph == nil)

        let negative = Jira.Setup(offlineDays: -3).config()
        #expect(negative.jira?.offlineDays == nil, "a negative window is the default")
        #expect(negative.name == "Jira")
    }

    @Test func classify() {
        let cases: [((any Error)?, Jira.ErrorClass)] = [
            (nil, .other),
            (RPCClient.ClientError.disconnected, .other),
            (RPCError(code: .invalidArgument, message: "bad url"), .invalid),
            (RPCError(code: .invalidParams, message: "bad params"), .invalid),
            // Go wraps it ("detect: %w"); the Swift client throws it as it is.
            (RPCError(code: .serverError, message: "not jira"), .server),
            (RPCError(code: .networkError, message: "refused"), .network),
            (RPCError(code: .offline, message: "offline"), .network),
            (RPCError(code: .serverTimeout, message: "slow"), .network),
            (RPCError(code: .tlsError, message: "untrusted"), .tls),
            (RPCError(code: .authFailed, message: "401"), .authFailed),
            (RPCError(code: .authRequired, message: "no token"), .authRequired),
            (RPCError(code: .conflict, message: "exists"), .conflict),
            (RPCError(code: .keyringError, message: "locked"), .other),
            (RPCError(code: .notImplemented, message: "old daemon"), .other),
        ]
        for (i, c) in cases.enumerated() {
            #expect(Jira.classify(c.0) == c.1, "case \(i)")
        }
        #expect(Jira.classOf(0) == .other)
    }

    @Test func failureOf() {
        let notJira = "This address is not a Jira site"
        let notWeb = "This is not a web address"
        let rejected = "The Jira site rejected the token"
        let exists = "An account for this Jira site already exists"
        let lookUp = "Looking up the Jira site"
        let loading = "Loading the spaces"
        let adding = "Adding the account"
        let saving = "Saving the account"
        let cloudPrompt = "Enter the API token for this account"
        let dcPrompt = "Enter the personal access token for this account"
        typealias F = Jira.Failure
        let cases: [(String, Jira.Step, Jira.ErrorClass, JiraDeployment, Bool, F)] = [
            ("detect: not jira", .detect, .server, "", false, F(page: .site, banner: notJira, what: lookUp)),
            ("detect: bad address", .detect, .invalid, "", false, F(page: .site, banner: notWeb, what: lookUp)),
            ("detect: network", .detect, .network, "", false, F(page: .site, banner: "", what: lookUp)),
            ("detect: tls", .detect, .tls, "", false, F(page: .site, banner: "", what: lookUp)),
            ("detect: other", .detect, .other, "", false, F(page: .site, banner: "", what: lookUp)),
            ("spaces: rejected", .spaces, .authFailed, .cloud, false, F(page: .credentials, banner: rejected, what: loading)),
            ("spaces: no token (cloud)", .spaces, .authRequired, .cloud, true, F(page: .credentials, banner: cloudPrompt, what: loading)),
            ("spaces: no token (dc)", .spaces, .authRequired, .datacenter, true, F(page: .credentials, banner: dcPrompt, what: loading)),
            ("spaces: server error is not 'not jira'", .spaces, .server, .cloud, false, F(page: .credentials, banner: "", what: loading)),
            ("spaces: network", .spaces, .network, .cloud, false, F(page: .credentials, banner: "", what: loading)),
            ("save: conflict", .save, .conflict, .cloud, false, F(page: .spaces, banner: exists, what: adding)),
            ("save: rejected", .save, .authFailed, .cloud, false, F(page: .credentials, banner: rejected, what: adding)),
            ("save: other", .save, .other, .cloud, false, F(page: .spaces, banner: "", what: adding)),
            ("edit: other", .save, .network, .datacenter, true, F(page: .credentials, banner: "", what: saving)),
            ("edit: conflict", .save, .conflict, .datacenter, true, F(page: .credentials, banner: exists, what: saving)),
            ("edit: no token", .save, .authRequired, .datacenter, true, F(page: .credentials, banner: dcPrompt, what: saving)),
        ]
        for (name, step, errorClass, d, editing, want) in cases {
            #expect(Jira.failureOf(step, errorClass, d, editing: editing) == want, "\(name)")
        }
    }
}
