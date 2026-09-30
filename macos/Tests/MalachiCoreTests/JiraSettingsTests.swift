// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/jira/settings_test.go (English catalogue). The Czech cases
// are in JiraSettingsTranslationTests, the patterns in JiraPatternTests.
// Go's empty values are nil in the API types here. Fictional sites and
// people only.

/// Invisible characters of the hostile cases, by their numbers so that no
/// tool rewrites them.
private let bidiOverride = String(Unicode.Scalar(0x202E)!) // RIGHT-TO-LEFT OVERRIDE
private let zeroWidth = String(Unicode.Scalar(0x200B)!) // ZERO WIDTH SPACE

/// settings_test.go `cloudAccount`: a stored Jira Cloud account with every
/// setting at its default.
func jiraCloudAccount() -> AccountConfig {
    AccountConfig(
        name: "Acme", email: "jana@acme.example", kind: .jira,
        jira: JiraConfig(
            siteUrl: "https://acme.atlassian.net", deployment: .cloud,
            cloudId: "0b9e3d2c-1a2b-4c3d-8e9f-001122334455", login: "jana@acme.example",
            spaces: [
                SpaceRef(id: "10001", key: "ITSD", name: "IT Service Desk"), SpaceRef(id: "10002", key: "WEB", name: "Website"),
            ]))
}

/// settings_test.go `dataCenterAccount`: a stored Data Center account.
func jiraDataCenterAccount() -> AccountConfig {
    AccountConfig(
        name: "Acme Jira", email: "jana@acme.example", kind: .jira,
        jira: JiraConfig(
            siteUrl: "https://jira.acme.example/jira", deployment: .datacenter,
            spaces: [SpaceRef(id: "20001", key: "OPS", name: "Operations")]))
}

let jiraSiteSpaces = [
    Space(id: "10001", key: "ITSD", name: "IT Service Desk", serviceDesk: true, issues: -1),
    Space(id: "10003", key: "MOB", name: "Mobile", issues: -1),
    Space(id: "10002", key: "WEB", name: "Website", issues: -1),
]

let jiraSiteStatuses = [
    IssueStatus(id: "1", name: "Open", category: .todo),
    IssueStatus(id: "3", name: "In Progress", category: .inProgress),
    IssueStatus(id: "5", name: "Resolved", category: .done),
    IssueStatus(id: "6", name: "Done", category: .done),
    IssueStatus(id: "10010", name: "Done", category: .done),
    IssueStatus(id: "10020", name: "Waiting for Customer", category: "waiting"),
    IssueStatus(id: "10021", name: "Done", category: .inProgress),
]

/// settings_test.go `groupsText`: the picker on one line:
/// "Title[style]: Name*(ids) Name(ids); …", a star for a selected choice.
private func groupsText(_ groups: [Jira.StatusGroup]) -> String {
    groups.map { g in
        let choices = g.choices.map { c in
            "\(c.name)\(c.selected ? "*" : "")(\(c.ids.joined(separator: ",")))"
        }
        return "\(g.title)[\(g.style.rawValue)]: \(choices.joined(separator: " "))"
    }.joined(separator: "; ")
}

private func ids(_ refs: [SpaceRef]) -> String {
    refs.map(\.id).joined(separator: ",")
}

private func ids(_ refs: [StatusRef]) -> String {
    refs.map(\.id).joined(separator: ",")
}

struct JiraSettingsTests {
    @Test func settingsTexts() {
        let s = Jira.settingsTexts()
        for child in Mirror(reflecting: s).children {
            #expect((child.value as? String)?.isEmpty == false, "SettingsStrings.\(child.label ?? "?") is empty")
        }
        #expect(Mirror(reflecting: s).children.count == 33)
        #expect(s.title == "Jira Account")
        #expect(s.spacesTitle == "Spaces")
        #expect(s.showEvents == "Show Status and Assignee Changes")
        #expect(s.replaceToken == "Replace Token…")
        #expect(s.foldersTitle == "Folders")
        #expect(s.saving == "Saving the account")
    }

    @Test func settingsSite() {
        let cloud = jiraCloudAccount()
        var got = Jira.settingsSite(cloud, user: nil)
        var want = Jira.SiteInfo(
            address: "https://acme.atlassian.net", deployment: "Jira Cloud", user: "jana@acme.example", tokenLabel: "API Token")
        #expect(got == want, "before the listing")
        got = Jira.settingsSite(cloud, user: SiteUser(name: "Jana Dvořáková", email: "jana@acme.example"))
        want.user = "Jana Dvořáková"
        want.userDetail = "jana@acme.example"
        #expect(got == want, "with the user")

        let dc = jiraDataCenterAccount()
        got = Jira.settingsSite(dc, user: SiteUser(name: "jdvorakova"))
        want = Jira.SiteInfo(
            address: "https://jira.acme.example/jira", deployment: "Jira Data Center", user: "jdvorakova",
            userDetail: "jana@acme.example", tokenLabel: "Personal Access Token")
        #expect(got == want, "data center")
        // A user without a name is the address; a name that is the address
        // has no detail.
        got = Jira.settingsSite(dc, user: SiteUser(name: ""))
        #expect(got.user == "jana@acme.example" && got.userDetail == "", "a nameless user")
        got = Jira.settingsSite(dc, user: SiteUser(name: "jana@acme.example", email: "jana@acme.example"))
        #expect(got.userDetail == "", "a name that is the address")
        // Hostile display text is cleaned.
        let hostile = SiteUser(name: "Jana" + bidiOverride + "\nDvořáková\u{0}", email: "a@b.example\r\nX: y")
        got = Jira.settingsSite(dc, user: hostile)
        #expect(got.user == "Jana Dvořáková" && got.userDetail == "a@b.example X: y", "hostile user")
        // An account of another kind has no site.
        got = Jira.settingsSite(AccountConfig(name: "", email: "jana@acme.example"), user: nil)
        #expect(got.address == "" && got.deployment == "Jira" && got.user == "jana@acme.example", "a mail account")
    }

    @Test func settingsSpaceRows() {
        let stored = jiraCloudAccount().jira!.spaces
        var rows = Jira.settingsSpaceRows(stored: stored, listed: jiraSiteSpaces)
        var want = [
            Jira.SpaceRow(id: "10001", title: "ITSD – IT Service Desk", serviceDesk: true),
            Jira.SpaceRow(id: "10003", title: "MOB – Mobile"),
            Jira.SpaceRow(id: "10002", title: "WEB – Website"),
        ]
        #expect(rows == want)
        // A stored space the listing lacks stays, after the listed ones.
        let gone = [SpaceRef(id: "10009", key: "OLD", name: "Archive")] + stored
        rows = Jira.settingsSpaceRows(stored: gone, listed: jiraSiteSpaces)
        #expect(rows.count == 4 && rows.last == Jira.SpaceRow(id: "10009", title: "OLD – Archive"))
        // Without a listing the stored spaces are the rows.
        rows = Jira.settingsSpaceRows(stored: stored, listed: [])
        want = [Jira.SpaceRow(id: "10001", title: "ITSD – IT Service Desk"), Jira.SpaceRow(id: "10002", title: "WEB – Website")]
        #expect(rows == want)
        #expect(Jira.settingsSpaceRows(stored: [], listed: []).isEmpty)
        // A stored space listed twice is one row.
        let twice = [SpaceRef(id: "7", key: "A"), SpaceRef(id: "7", key: "A")]
        #expect(Jira.settingsSpaceRows(stored: twice, listed: []).count == 1)
    }

    @Test func setSpaceSelected() {
        let stored = [SpaceRef(id: "10009", key: "OLD", name: "Archive")] + jiraCloudAccount().jira!.spaces
        // Ticking one: the order of the rows, the listed ones first.
        var got = Jira.setSpaceSelected(stored, stored: stored, listed: jiraSiteSpaces, id: "10003", on: true)
        #expect(ids(got) == "10001,10003,10002,10009")
        // The key and the name come from the listing.
        let renamed = [Space(id: "10001", key: "HELP", name: "Help Desk")]
        got = Jira.setSpaceSelected(stored, stored: stored, listed: renamed, id: "10002", on: false)
        #expect(ids(got) == "10001,10009")
        #expect(got.first == SpaceRef(id: "10001", key: "HELP", name: "Help Desk"))
        // Unticking the last one leaves nothing; an unknown id changes
        // nothing.
        let one = [SpaceRef(id: "10001", key: "ITSD")]
        #expect(Jira.setSpaceSelected(one, stored: one, listed: [], id: "10001", on: false).isEmpty)
        #expect(ids(Jira.setSpaceSelected(one, stored: one, listed: [], id: "nope", on: true)) == "10001")
    }

    @Test func folders() {
        #expect(Jira.virtualFolders == [.assignedToMe, .watching, .open])
        for v in Jira.virtualFolders {
            #expect(Jira.folderShown([], v), "\(v.rawValue) is hidden by default")
        }
        var disabled = Jira.setFolderShown([], .open, shown: false)
        disabled = Jira.setFolderShown(disabled, .assignedToMe, shown: false)
        #expect(disabled == [.assignedToMe, .open])
        #expect(!Jira.folderShown(disabled, .open) && Jira.folderShown(disabled, .watching))
        // Hiding twice, and an unknown or repeated stored view, leave one
        // entry per known view.
        disabled = Jira.setFolderShown(["open", "open", "archive"], .open, shown: false)
        #expect(disabled == [.open])
        #expect(Jira.setFolderShown(disabled, .open, shown: true).isEmpty)
    }

    @Test func notificationModes() {
        let labels = Jira.notificationModeLabels()
        #expect(labels == ["Check the Issue at Once", "Check the Issue and Hide the E-mail", "Do Nothing"])
        #expect(labels.count == Jira.notificationModes.count)
        let cases: [(NotificationMailMode?, Int)] = [(nil, 0), ("", 0), ("sync", 0), ("hide", 1), ("ignore", 2), ("later", 0)]
        for (mode, index) in cases {
            #expect(Jira.indexOfNotificationMode(mode) == index, "indexOfNotificationMode(\(mode?.rawValue ?? "nil"))")
        }
        #expect(Jira.notificationHint(.hide) == "Hidden e-mails stay in your mailbox and come back when you turn this off")
        for mode in [nil, "", NotificationMailMode.sync, .ignore] as [NotificationMailMode?] {
            #expect(Jira.notificationHint(mode) == "", "hint of \(mode?.rawValue ?? "nil")")
        }
        #expect(Jira.sendersEditable(nil) && Jira.sendersEditable("") && Jira.sendersEditable(.hide))
        #expect(!Jira.sendersEditable(.ignore))
    }

    @Test func defaultSenders() {
        #expect(Jira.defaultSenders(jiraCloudAccount()) == "@acme.atlassian.net")
        #expect(Jira.defaultSenders(jiraDataCenterAccount()) == "")
        var upper = jiraCloudAccount()
        upper.jira?.siteUrl = "https://ACME.Atlassian.NET:8443/"
        #expect(Jira.defaultSenders(upper) == "@acme.atlassian.net", "upper case with a port")
        var broken = jiraCloudAccount()
        broken.jira?.siteUrl = "::"
        #expect(Jira.defaultSenders(broken) == "", "no host")
        #expect(Jira.defaultSenders(AccountConfig(name: "", email: "a@b.example")) == "", "a mail account")
    }

    @Test func statusGroups() {
        // The default: the statuses of the category done; a name is one
        // choice per category, with every id of it.
        var got = groupsText(Jira.statusGroups(jiraSiteStatuses, closed: []))
        var want = "To Do[status-todo]: Open(1); "
            + "In Progress[status-in-progress]: In Progress(3) Done(10021); "
            + "Done[status-done]: Resolved*(5) Done*(6,10010); "
            + "Other[]: Waiting for Customer(10020)"
        #expect(got == want, "default")
        // Stored statuses: a choice is selected when one of its ids is
        // stored; a stored status the site lacks is listed among the
        // others.
        let closed = [
            StatusRef(id: "10010", name: "Done"), StatusRef(id: "10020"), StatusRef(id: "777", name: "Cancelled"),
            StatusRef(id: "778"),
        ]
        got = groupsText(Jira.statusGroups(jiraSiteStatuses, closed: closed))
        want = "To Do[status-todo]: Open(1); "
            + "In Progress[status-in-progress]: In Progress(3) Done(10021); "
            + "Done[status-done]: Resolved(5) Done*(6,10010); "
            + "Other[]: Waiting for Customer*(10020) Cancelled*(777) 778*(778)"
        #expect(got == want, "stored")
        // Without a listing only the stored ones are known.
        #expect(groupsText(Jira.statusGroups([], closed: Array(closed.prefix(1)))) == "Other[]: Done*(10010)")
        #expect(Jira.statusGroups([], closed: []).isEmpty, "nothing known")
        // Hostile names are cleaned; a status without an id or listed
        // twice is left out; a nameless one shows its id.
        let hostile = [
            IssueStatus(id: "1", name: "Do" + zeroWidth + "ne\n" + bidiOverride + "now", category: .done),
            IssueStatus(id: "", name: "Nameless", category: .done),
            IssueStatus(id: "1", name: "Again", category: .todo),
            IssueStatus(id: "2", name: " \t ", category: .todo),
        ]
        got = groupsText(Jira.statusGroups(hostile, closed: []))
        #expect(got == "To Do[status-todo]: 2(2); Done[status-done]: Done now*(1)", "hostile")
    }

    @Test func setStatusSelected() throws {
        func choice(_ closed: [StatusRef], _ name: String) throws -> Jira.StatusChoice {
            let all = Jira.statusGroups(jiraSiteStatuses, closed: closed).flatMap(\.choices)
            return try #require(all.first { $0.name == name }, "no choice \(name)")
        }

        let def = Jira.defaultClosedStatuses(jiraSiteStatuses)
        #expect(ids(def) == "5,6,10010" && def[1].name == "Done")
        // From the default: ticking adds to the statuses of the category
        // done.
        var closed = Jira.setStatusSelected(jiraSiteStatuses, closed: [], choice: try choice([], "Waiting for Customer"), on: true)
        #expect(ids(closed) == "5,6,10010,10020" && closed[3].name == "Waiting for Customer", "after ticking")
        // Unticking a name takes every status of it.
        closed = Jira.setStatusSelected(jiraSiteStatuses, closed: closed, choice: try choice(closed, "Resolved"), on: false)
        #expect(ids(closed) == "6,10010,10020", "after unticking Resolved")
        // Back at the statuses of the category done: the default, empty.
        closed = Jira.setStatusSelected(jiraSiteStatuses, closed: closed, choice: try choice(closed, "Resolved"), on: true)
        closed = Jira.setStatusSelected(
            jiraSiteStatuses, closed: closed, choice: try choice(closed, "Waiting for Customer"), on: false)
        #expect(closed.isEmpty, "the default again")
        // One stored id of a name: ticking stores the others too.
        closed = Jira.setStatusSelected(
            jiraSiteStatuses, closed: [StatusRef(id: "10010"), StatusRef(id: "1")],
            choice: Jira.StatusChoice(name: "Done", ids: ["6", "10010"]), on: true)
        #expect(ids(closed) == "10010,1,6", "after ticking a name stored in part")
        // Nothing left is the default.
        closed = Jira.setStatusSelected(
            jiraSiteStatuses, closed: [StatusRef(id: "1")], choice: Jira.StatusChoice(name: "Open", ids: ["1"]), on: false)
        #expect(closed.isEmpty, "nothing left")
        // Without a listing the stored ones can still be unticked.
        let stored = [StatusRef(id: "777", name: "Cancelled"), StatusRef(id: "5", name: "Resolved")]
        closed = Jira.setStatusSelected([], closed: stored, choice: Jira.StatusChoice(name: "Cancelled", ids: ["777"]), on: false)
        #expect(ids(closed) == "5", "without a listing")
        // A choice the site does not list keeps its name.
        closed = Jira.setStatusSelected(
            [], closed: Array(stored.suffix(1)), choice: Jira.StatusChoice(name: "Cancelled", ids: ["777"]), on: true)
        #expect(ids(closed) == "5,777" && closed[1].name == "Cancelled", "a choice that is not listed")
    }

    @Test func statusesProblem() {
        var many = (0..<API.Limits.maxJiraStatuses).map { StatusRef(id: String($0)) }
        #expect(Jira.statusesProblem(many) == "", "at the limit")
        many.append(StatusRef(id: "x"))
        #expect(Jira.statusesProblem(many) == "Select at most 64 statuses", "over the limit")
        #expect(Jira.statusesProblem([]) == "", "none")
    }

    @Test func normaliseList() {
        let cases: [(Jira.ListKind, [String], [String])] = [
            (.botNames, [], []),
            (.botNames, ["", "  "], []),
            (.botNames, [" Issue Sync – Synchronization for Jira ", "issue sync - synchronization  for jira", "Deploy Bot"],
             ["Issue Sync – Synchronization for Jira", "Deploy Bot"]),
            (.metadataFilters, [#"^a$"#, #" ^a$ "#, #"^A$"#, ""], [#"^a$"#, #"^A$"#]),
            (.authorPrefixes, ["ACME", "acme", " Acme s.r.o. "], ["ACME", "Acme s.r.o."]),
            (.senders, [" Jira@Acme.Example ", "jira@acme.example", "@ACME.example"], ["jira@acme.example", "@acme.example"]),
        ]
        for (kind, input, want) in cases {
            #expect(Jira.normaliseList(kind, input) == want, "normaliseList(\(kind), \(input))")
        }
    }

    @Test func checkEntry() {
        let tooLong = "This entry is too long"
        let control = "This entry contains control characters"
        let duplicate = "This entry is already in the list"
        let full = "The list holds at most 32 entries"
        let sender = "Enter an address, or a domain such as @example.org"
        let short = "A bot name needs at least 3 characters"
        let thirtyTwo = (0..<API.Limits.maxJiraListEntries).map { "entry \($0)" }
        let exact = String(repeating: "a", count: API.Limits.maxJiraPatternBytes)
        let cases: [(kind: Jira.ListKind, input: String, have: [String], entry: String, problem: String)] = [
            // Nothing typed is nothing to add, and no problem.
            (.botNames, "", [], "", ""),
            (.metadataFilters, " \t ", [], "", ""),

            (.botNames, "  Issue Sync – Synchronization for Jira ", [], "Issue Sync – Synchronization for Jira", ""),
            (.botNames, "Bot", [], "Bot", ""),
            (.botNames, "Žů", [], "", short),
            (.botNames, "a" + zeroWidth + zeroWidth + "b", [], "", short),
            (.botNames, "issue sync - synchronization for jira", [Jira.suggestedBotName], "", duplicate),
            (.botNames, "Deploy\tBot", [], "", control),
            (.botNames, "Deploy Bot\u{0}", [], "", control),
            (.botNames, exact, [], exact, ""),
            (.botNames, exact + "a", [], "", tooLong),
            (.botNames, String(repeating: "ž", count: API.Limits.maxJiraPatternBytes / 2 + 1), [], "", tooLong),
            (.botNames, "One More", thirtyTwo, "", full),
            (.botNames, "Entry 3", thirtyTwo, "", duplicate),

            (.metadataFilters, #" ^Remote comment create date:.*$ "#, [], #"^Remote comment create date:.*$"#, ""),
            (.metadataFilters, #"^a$"#, [#"^A$"#], #"^a$"#, ""),
            (.metadataFilters, #"^a$"#, [#" ^a$"#], "", duplicate),
            (.metadataFilters, #"(a"#, [], "", "This pattern is not valid: missing closing )"),
            (.metadataFilters, #"(?=a)"#, [], "", "This pattern is not valid: invalid or unsupported Perl syntax"),
            (.metadataFilters, #"a{1001}"#, [], "", "This pattern is not valid: invalid repeat count"),
            (.metadataFilters, "a\nb", [], "", control),
            (.metadataFilters, "x", thirtyTwo, "", full),

            (.authorPrefixes, " ACME ", [], "ACME", ""),
            (.authorPrefixes, "a", [], "a", ""),
            (.authorPrefixes, "acme", ["ACME"], "", duplicate),
            (.authorPrefixes, zeroWidth, [], "", control),

            (.senders, " Jira@Acme.Example ", [], "jira@acme.example", ""),
            (.senders, "@Acme.Example", [], "@acme.example", ""),
            (.senders, "@localhost", [], "@localhost", ""),
            (.senders, "JIRA@acme.example", ["jira@acme.example"], "", duplicate),
            (.senders, "acme.example", [], "", sender),
            (.senders, "@", [], "", sender),
            (.senders, "@acme..example", [], "", sender),
            (.senders, "@-acme.example", [], "", sender),
            (.senders, "@acme_.example", [], "", sender),
            (.senders, "@acme.example/path", [], "", sender),
            (.senders, "Jira <jira@acme.example>", [], "", sender),
            (.senders, "jira@acme.example, x@acme.example", [], "", sender),
            (.senders, "jira@", [], "", sender),
            (.senders, "@" + String(repeating: "a", count: 64) + ".example", [], "", sender),
        ]
        for c in cases {
            let got = Jira.checkEntry(c.kind, c.input, have: c.have)
            #expect(got.entry == c.entry && got.problem == c.problem,
                    "checkEntry(\(c.kind), \(c.input.debugDescription)) = \(got)")
        }
    }

    @Test func suggestions() {
        var got = Jira.suggestions(.botNames, have: [])
        #expect(got == [Jira.Suggestion(
            value: "Issue Sync – Synchronization for Jira", label: "Add Issue Sync – Synchronization for Jira")])
        got = Jira.suggestions(.metadataFilters, have: [#"^x$"#])
        #expect(got == [Jira.Suggestion(
            value: #"^Remote comment create date:.*$"#, label: #"Add ^Remote comment create date:.*$"#)])
        // Not once the list has it (a bot name in any spelling of its
        // dash).
        #expect(Jira.suggestions(.botNames, have: ["Deploy Bot", " issue sync - Synchronization for JIRA"]).isEmpty)
        #expect(Jira.suggestions(.metadataFilters, have: [#"^Remote comment create date:.*$"#]).isEmpty)
        // Not for the other lists, and not for a full list.
        #expect(Jira.suggestions(.authorPrefixes, have: []).isEmpty && Jira.suggestions(.senders, have: []).isEmpty)
        let full = (0..<API.Limits.maxJiraListEntries).map { "Bot \($0)" }
        #expect(Jira.suggestions(.botNames, have: full).isEmpty, "a full list")
        // Both suggestions pass the page's own check.
        var checked = Jira.checkEntry(.botNames, Jira.suggestedBotName, have: [])
        #expect(checked.entry == Jira.suggestedBotName && checked.problem == "")
        checked = Jira.checkEntry(.metadataFilters, Jira.suggestedMetadataFilter, have: [])
        #expect(checked.entry == Jira.suggestedMetadataFilter && checked.problem == "")
    }

    @Test func newSettingsForm() {
        var got = Jira.SettingsForm(jiraCloudAccount())
        var want = Jira.SettingsForm()
        want.name = "Acme"
        want.spaces = [SpaceRef(id: "10001", key: "ITSD", name: "IT Service Desk"), SpaceRef(id: "10002", key: "WEB", name: "Website")]
        #expect(got == want, "defaults")
        #expect(want.showEvents && want.notificationMail == .sync)

        var cfg = jiraCloudAccount()
        cfg.jira?.offlineDays = 45
        cfg.jira?.onlyMine = true
        cfg.jira?.hideEvents = true
        cfg.jira?.disabledFolders = [.watching]
        cfg.jira?.closedStatuses = [StatusRef(id: "5", name: "Resolved")]
        cfg.jira?.notificationMail = .hide
        cfg.jira?.notificationSenders = ["jira@acme.example"]
        cfg.jira?.botNames = [Jira.suggestedBotName]
        cfg.jira?.metadataFilters = [Jira.suggestedMetadataFilter]
        cfg.jira?.authorPrefixes = ["ACME"]
        got = Jira.SettingsForm(cfg)
        want.offlineDays = 45
        want.onlyMine = true
        want.showEvents = false
        want.disabledFolders = [.watching]
        want.closedStatuses = [StatusRef(id: "5", name: "Resolved")]
        want.notificationMail = .hide
        want.notificationSenders = ["jira@acme.example"]
        want.botNames = [Jira.suggestedBotName]
        want.metadataFilters = [Jira.suggestedMetadataFilter]
        want.authorPrefixes = ["ACME"]
        #expect(got == want, "stored")
        // A mode this client does not know is shown as the default; an
        // account of another kind has an empty form.
        cfg.jira?.notificationMail = "later"
        #expect(Jira.SettingsForm(cfg).notificationMail == .sync, "an unknown mode")
        var mail = Jira.SettingsForm()
        mail.name = "Mail"
        #expect(Jira.SettingsForm(AccountConfig(name: "Mail", email: "jana@acme.example")) == mail, "a mail account")
    }

    @Test func settingsFormApply() {
        // Nothing edited: the account as it is stored.
        let stored = jiraCloudAccount()
        var got = Jira.SettingsForm(stored).apply(stored)
        #expect(got == stored, "unedited")
        #expect(!Jira.changed(stored, got), "an unedited form counts as changed")

        var f = Jira.SettingsForm(stored)
        f.name = "  Acme Jira  "
        f.spaces = [
            SpaceRef(id: "10003", key: "MOB", name: "Mobile"), SpaceRef(id: "10003", key: "MOB"), SpaceRef(id: "", key: "X"),
            SpaceRef(id: "10001", key: "ITSD"),
        ]
        f.offlineDays = 90
        f.onlyMine = true
        f.showEvents = false
        f.disabledFolders = [.open, .assignedToMe, .open]
        f.closedStatuses = [
            StatusRef(id: " 5 ", name: " Resolved "), StatusRef(id: "5"), StatusRef(id: ""), StatusRef(id: "6", name: "Done"),
        ]
        f.notificationMail = .hide
        f.notificationSenders = [" Jira@Acme.Example", "jira@acme.example", ""]
        f.botNames = [" Issue Sync – Synchronization for Jira ", "issue sync - synchronization for jira"]
        f.metadataFilters = [Jira.suggestedMetadataFilter, " " + Jira.suggestedMetadataFilter]
        f.authorPrefixes = ["ACME", "  ", "Acme"]
        got = f.apply(stored)
        var want = jiraCloudAccount()
        want.name = "Acme Jira"
        want.jira?.spaces = [SpaceRef(id: "10003", key: "MOB", name: "Mobile"), SpaceRef(id: "10001", key: "ITSD")]
        want.jira?.offlineDays = 90
        want.jira?.onlyMine = true
        want.jira?.hideEvents = true
        want.jira?.disabledFolders = [.assignedToMe, .open]
        want.jira?.closedStatuses = [StatusRef(id: "5", name: "Resolved"), StatusRef(id: "6", name: "Done")]
        want.jira?.notificationMail = .hide
        want.jira?.notificationSenders = ["jira@acme.example"]
        want.jira?.botNames = ["Issue Sync – Synchronization for Jira"]
        want.jira?.metadataFilters = [Jira.suggestedMetadataFilter]
        want.jira?.authorPrefixes = ["ACME"]
        #expect(got == want, "edited")
        #expect(Jira.changed(stored, got), "an edited form does not count as changed")
        // The connection stays.
        #expect(got.jira?.siteUrl == stored.jira?.siteUrl && got.jira?.login == stored.jira?.login)
        #expect(got.jira?.cloudId == stored.jira?.cloudId && got.email == stored.email)

        // Back to the defaults: everything optional is left out.
        var back = Jira.SettingsForm(got)
        back.offlineDays = 0
        back.onlyMine = false
        back.showEvents = true
        back.disabledFolders = []
        back.closedStatuses = []
        back.notificationMail = .sync
        back.notificationSenders = []
        back.botNames = [" "]
        back.metadataFilters = []
        back.authorPrefixes = []
        back.spaces = jiraCloudAccount().jira!.spaces
        back.name = "Acme"
        #expect(back.apply(got) == jiraCloudAccount(), "defaults again")

        // An empty name is the site's host; the window is kept within the
        // limit.
        f = Jira.SettingsForm(stored)
        f.name = " \t"
        f.offlineDays = 5000
        got = f.apply(stored)
        #expect(got.name == "acme.atlassian.net" && got.jira?.offlineDays == API.Limits.maxJiraOfflineDays)
        f.offlineDays = -3
        f.name = String(repeating: "ž", count: 200)
        got = f.apply(stored)
        #expect(got.jira?.offlineDays == nil && got.name.utf8.count == 256, "a negative window and a long name")
        // An unknown mode is the default; an account of another kind is
        // left alone.
        f.notificationMail = "later"
        #expect(f.apply(stored).jira?.notificationMail == nil, "an unknown mode")
        let mail = AccountConfig(name: "Mail", email: "jana@acme.example")
        #expect(f.apply(mail) == mail, "a mail account")
    }

    /// The wire form of what `apply` makes: what Go leaves out is left out.
    @Test func appliedDefaultsAreLeftOut() throws {
        let cfg = Jira.SettingsForm(jiraCloudAccount()).apply(jiraCloudAccount())
        let data = try JSONCoding.encoder().encode(cfg)
        let object = try #require(try JSONSerialization.jsonObject(with: data) as? [String: Any])
        let jira = try #require(object["jira"] as? [String: Any])
        #expect(Set(jira.keys) == ["siteUrl", "deployment", "cloudId", "login", "spaces"])
    }

    @Test func settingsProblem() {
        var f = Jira.SettingsForm(jiraCloudAccount())
        #expect(f.settingsProblem() == "", "a stored account")
        f.spaces = []
        #expect(f.settingsProblem() == "Select at least one space")
        f.spaces = jiraCloudAccount().jira!.spaces
        f.closedStatuses = (0...API.Limits.maxJiraStatuses).map { StatusRef(id: String($0)) }
        #expect(f.settingsProblem() == "Select at most 64 statuses")
    }

    @Test func changed() {
        func edit(_ f: (inout AccountConfig) -> Void) -> AccountConfig {
            var cfg = jiraCloudAccount()
            cfg.jira?.closedStatuses = [StatusRef(id: "5", name: "Resolved"), StatusRef(id: "6", name: "Done")]
            cfg.jira?.disabledFolders = [.watching, .open]
            cfg.jira?.botNames = ["Deploy Bot", Jira.suggestedBotName]
            cfg.jira?.authorPrefixes = ["ACME", "Globex"]
            f(&cfg)
            return cfg
        }
        let old = edit { _ in }
        let cases: [(name: String, f: (inout AccountConfig) -> Void, want: Bool)] = [
            ("nothing", { _ in }, false),
            ("the default window written out", { $0.jira?.offlineDays = 30 }, false),
            ("the default window as zero", { $0.jira?.offlineDays = 0 }, false),
            ("the default mode written out", { $0.jira?.notificationMail = .sync }, false),
            ("spaces around the name", { $0.name = " Acme " }, false),
            ("the order of the spaces", { $0.jira?.spaces.swapAt(0, 1) }, false),
            ("the name of a space", { $0.jira?.spaces[0].name = "Help Desk" }, false),
            ("the name and order of the statuses", {
                $0.jira?.closedStatuses = [StatusRef(id: "6"), StatusRef(id: "5", name: "Closed"), StatusRef(id: "6")]
            }, false),
            ("the order of the views", { $0.jira?.disabledFolders = [.open, .watching, .open] }, false),
            ("the order and spelling of the bots", {
                $0.jira?.botNames = [" issue sync - synchronization for jira", "Deploy Bot", "deploy bot"]
            }, false),
            ("a switch written out as off", {
                $0.jira?.onlyMine = false
                $0.jira?.hideEvents = false
            }, false),
            ("the kind written out", { $0.kind = .jira }, false),

            ("the name", { $0.name = "Acme Jira" }, true),
            ("a space more", { $0.jira?.spaces.append(SpaceRef(id: "10003", key: "MOB")) }, true),
            ("the key of a space", { $0.jira?.spaces[0].key = "HELP" }, true),
            ("the window", { $0.jira?.offlineDays = 90 }, true),
            ("only mine", { $0.jira?.onlyMine = true }, true),
            ("the events", { $0.jira?.hideEvents = true }, true),
            ("a view", { $0.jira?.disabledFolders = [.watching] }, true),
            ("a status", { $0.jira?.closedStatuses.removeLast() }, true),
            ("no statuses", { $0.jira?.closedStatuses = [] }, true),
            ("the mode", { $0.jira?.notificationMail = .ignore }, true),
            ("a sender", { $0.jira?.notificationSenders = ["@acme.example"] }, true),
            ("a bot", { $0.jira?.botNames.removeLast() }, true),
            ("a pattern", { $0.jira?.metadataFilters = [#"^x$"#] }, true),
            ("the order of the prefixes", { $0.jira?.authorPrefixes = ["Globex", "ACME"] }, true),
            ("the site", { $0.jira?.siteUrl = "https://globex.atlassian.net" }, true),
            ("the login", { $0.jira?.login = "petr@acme.example" }, true),
            ("the interval", { $0.syncIntervalSeconds = 600 }, true),
        ]
        for c in cases {
            #expect(Jira.changed(old, edit(c.f)) == c.want, "changed after \(c.name)")
        }
        // Accounts of another kind compare as they are.
        let mail = AccountConfig(name: "Mail", email: "jana@acme.example")
        var other = mail
        other.email = "petr@acme.example"
        #expect(!Jira.changed(mail, mail) && Jira.changed(mail, other) && Jira.changed(mail, old))
    }
}
