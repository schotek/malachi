// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/jira/jira_test.go (English catalogue: the msgids come back
// verbatim). The Czech cases of the Go tests, which use a fake catalogue,
// are in JiraTranslationTests against the generated one.

/// A character as a scalar constant, so the source stays free of invisible
/// characters.
func jiraScalar(_ v: UInt32) -> String {
    String(UnicodeScalar(v) ?? " ")
}

/// Bytes as Swift reads them: invalid UTF-8 becomes the replacement
/// character, which is how Go's invalid bytes reach the port.
func jiraBytes(_ b: [UInt8]) -> String {
    String(decoding: b, as: UTF8.self)
}

let jiraRLO = jiraScalar(0x202E) // RIGHT-TO-LEFT OVERRIDE
let jiraZWSP = jiraScalar(0x200B) // ZERO WIDTH SPACE
let jiraSHY = jiraScalar(0x00AD) // SOFT HYPHEN
let jiraLSEP = jiraScalar(0x2028) // LINE SEPARATOR
let jiraBOM = jiraScalar(0xFEFF) // ZERO WIDTH NO-BREAK SPACE

/// jira_test.go `jiraConfig`.
private func jiraConfig(_ name: String, _ site: String) -> AccountConfig {
    AccountConfig(name: name, email: "jana@acme.example", kind: .jira, jira: JiraConfig(siteUrl: site, deployment: .cloud))
}

/// jira_test.go `fullIssue`.
func fullIssue() -> IssueInfo {
    IssueInfo(
        key: "ITSD-42", url: "https://acme.atlassian.net/browse/ITSD-42", summary: "VPN drops every 10 minutes",
        status: "In Progress", statusCategory: .inProgress, type: "Incident", priority: "High",
        assignee: "Jana Dvořáková", reporter: "Petr Novák", assignedToMe: true,
        commentVisibilities: [.public, .internal])
}

private func issueOnly(_ key: String, summary: String = "", priority: String? = nil) -> IssueInfo {
    IssueInfo(key: key, url: "", summary: summary, status: "", priority: priority)
}

/// A message of a jira account (the Go tests' zero MessageSummary with an
/// issue and flags).
private func issueMessage(_ issue: MessageIssue?, flags: [Flag] = [], subject: String = "") -> MessageSummary {
    MessageSummary(
        id: "m1", accountId: "j1", folderId: "f1", from: [], subject: subject, date: .goZero, snippet: "",
        flags: flags, hasAttachments: false, size: 0, issue: issue)
}

private func issueThread(_ issue: IssueInfo?, latest: MessageIssue?, unread: Int, subject: String = "") -> ThreadSummary {
    ThreadSummary(
        id: "t1", accountId: "j1", subject: subject, participants: [], messageCount: 1, unreadCount: unread,
        latestDate: .goZero, latest: issueMessage(latest), snippet: "", flags: [], hasAttachments: false,
        folderIds: [], issue: issue)
}

struct JiraTests {
    @Test func styleOf() {
        let cases: [(IssueStatusCategory?, String)] = [
            (.todo, "status-todo"),
            (.inProgress, "status-in-progress"),
            (.done, "status-done"),
            ("", ""),
            (nil, ""),
            ("blocked", ""),
            ("Done", ""),
        ]
        for (c, want) in cases {
            #expect(Jira.styleOf(c).rawValue == want, "\(String(describing: c))")
        }
    }

    @Test func clean() {
        let cases: [(String, String, String)] = [
            ("plain", "VPN drops every 10 minutes", "VPN drops every 10 minutes"),
            ("trim and collapse", "  a \t\n  b  ", "a b"),
            ("line breaks become spaces", "first\r\nsecond" + jiraLSEP + "third", "first second third"),
            ("bidi override dropped", "invoice" + jiraRLO + "fdp.exe", "invoicefdp.exe"),
            ("zero width and soft hyphen dropped", "ad" + jiraZWSP + "min" + jiraSHY + "istrator" + jiraBOM, "administrator"),
            ("control characters dropped", jiraBytes([0x61, 0x00, 0x62, 0x07, 0x63, 0x1B, 0x64, 0x7F, 0x65]), "abcde"),
            ("invalid UTF-8 dropped", jiraBytes([0x6F, 0x6B, 0xFF, 0xFE, 0x21]), "ok!"),
            ("only invisible", jiraZWSP + jiraRLO + " \n", ""),
            ("czech kept", "Jana Dvořáková", "Jana Dvořáková"),
            ("empty", "", ""),
        ]
        for (name, input, want) in cases {
            #expect(Jira.clean(input) == want, "\(name)")
        }
        let long = Jira.clean(String(repeating: "č", count: 600))
        #expect(long.utf8.count <= Jira.maxText && long.utf8.count >= Jira.maxText - 1, "Clean of 1200 bytes = \(long.utf8.count) bytes")
        let spaced = Jira.clean(String(repeating: "a", count: Jira.maxText - 1) + " bcd")
        #expect(spaced == String(repeating: "a", count: Jira.maxText - 1), "a cut after a space keeps the space")
    }

    @Test func siteHostAndAccountLabel() {
        let cases: [(String, AccountConfig, String, String)] = [
            ("named", jiraConfig("Acme Jira", "https://acme.atlassian.net"), "acme.atlassian.net", "Acme Jira"),
            ("unnamed", jiraConfig(" ", "https://ACME.atlassian.net"), "acme.atlassian.net", "acme.atlassian.net"),
            ("port and path", jiraConfig("", "https://jira.acme.example:8443/jira"), "jira.acme.example", "jira.acme.example"),
            ("not a URL", jiraConfig("", "https://[bad"), "", "jana@acme.example"),
            ("no jira block", AccountConfig(name: "", email: "jana@acme.example", kind: .jira), "", "jana@acme.example"),
            ("mail account", AccountConfig(name: "", email: " jana@acme.example ", jira: JiraConfig(siteUrl: "https://acme.atlassian.net", deployment: "")),
             "", "jana@acme.example"),
            ("mail account with a name", AccountConfig(name: "Work", email: "jana@acme.example"), "", "Work"),
        ]
        for (name, cfg, host, label) in cases {
            #expect(Jira.siteHost(cfg) == host, "\(name): siteHost")
            #expect(Jira.accountLabel(cfg) == label, "\(name): accountLabel")
        }
    }

    @Test func kindHelpers() {
        #expect(Jira.isJira(jiraConfig("", "")))
        #expect(!Jira.isJira(AccountConfig(name: "", email: "")))
        #expect(!Jira.isJira(AccountConfig(name: "", email: "", kind: .graph)))
        #expect(Jira.alwaysThreaded(jiraConfig("", "")))
        #expect(!Jira.alwaysThreaded(AccountConfig(name: "", email: "")))
        let names: [(JiraDeployment, String)] = [
            (.cloud, "Jira Cloud"), (.datacenter, "Jira Data Center"), ("", "Jira"), ("server", "Jira"),
        ]
        for (d, want) in names {
            #expect(Jira.deploymentName(d) == want, "\(d)")
        }
    }

    @Test func virtualFolders() {
        let cases: [(VirtualFolder?, String, Int, String)] = [
            (.assignedToMe, "Assigned to Me", 0, "folder-saved-search-symbolic"),
            (.watching, "Watching", 1, "folder-saved-search-symbolic"),
            (.open, "Open", 2, "folder-saved-search-symbolic"),
            ("recent", "", 100, "folder-saved-search-symbolic"),
            ("", "", 100, ""),
            (nil, "", 100, ""),
        ]
        for (v, title, rank, icon) in cases {
            let what = String(describing: v)
            #expect(Jira.virtualFolderTitle(v) == title, "\(what): title")
            #expect(Jira.virtualRank(v) == rank, "\(what): rank")
            #expect(Jira.virtualIcon(v) == icon, "\(what): icon")
        }
    }

    @Test func issueCard() {
        let c = Jira.issueCard(fullIssue())
        let want = Jira.Card(
            key: "ITSD-42", url: "https://acme.atlassian.net/browse/ITSD-42", openTooltip: "Open ITSD-42 in the Browser",
            summary: "VPN drops every 10 minutes", status: "In Progress", statusLabel: "Status", statusStyle: .inProgress,
            rows: [
                Jira.CardRow(label: "Assignee", value: "Jana Dvořáková"),
                Jira.CardRow(label: "Priority", value: "High"),
                Jira.CardRow(label: "Type", value: "Incident"),
                Jira.CardRow(label: "Reporter", value: "Petr Novák"),
            ])
        #expect(c == want)

        let empty = Jira.issueCard(issueOnly("WEB-7", summary: " " + jiraRLO + "Login\nbroken "))
        #expect(empty.summary == "Login broken")
        #expect(empty.status == "")
        #expect(empty.statusStyle == .plain)
        #expect(empty.rows == [
            Jira.CardRow(label: "Assignee", value: "Unassigned", missing: true),
            Jira.CardRow(label: "Priority", value: "None", missing: true),
            Jira.CardRow(label: "Type", value: "None", missing: true),
            Jira.CardRow(label: "Reporter", value: "None", missing: true),
        ])
        let hidden = Jira.issueCard(issueOnly("WEB-7", priority: jiraZWSP + " ")).rows[1]
        #expect(hidden.missing, "an invisible priority is missing: \(hidden)")
    }

    @Test func issueCardItem() {
        let issue = fullIssue()
        let internalItem = MessageIssue(info: issue, item: .comment, visibility: .internal, via: "Issue Sync", edited: true)
        let c = Jira.issueCard(issue, item: internalItem)
        #expect(c.internal && c.internalLabel == "Internal" && c.via == "via Issue Sync" && c.edited == "Edited", "\(c)")

        let publicItem = MessageIssue(info: issue, item: .comment, visibility: .public)
        let p = Jira.issueCard(issue, item: publicItem)
        #expect(!p.internal && p.internalLabel == "" && p.via == "" && p.edited == "", "\(p)")

        // Only a comment is internal, whatever the visibility field says.
        let desc = MessageIssue(info: issue, item: .description, visibility: .internal)
        #expect(!Jira.issueCard(issue, item: desc).internal, "a description is never internal")

        let invisibleVia = MessageIssue(info: issueOnly(""), item: .comment, via: jiraRLO + jiraZWSP)
        #expect(Jira.issueCard(issue, item: invisibleVia).via == "")
    }

    @Test func eventLines() {
        let cases: [(String, [IssueChange], [String])] = [
            ("none", [], []),
            ("status", [IssueChange(field: .status, from: "To Do", to: "In Progress")], ["Status: To Do → In Progress"]),
            ("status without the old value", [IssueChange(field: .status, to: "Done")], ["Status: — → Done"]),
            ("status without the new value", [IssueChange(field: .status, from: "Done")], ["Status: Done → —"]),
            ("status both empty", [IssueChange(field: .status)], ["Status: — → —"]),
            ("assigned", [IssueChange(field: .assignee, to: "Jana Dvořáková")], ["Assignee: Unassigned → Jana Dvořáková"]),
            ("unassigned", [IssueChange(field: .assignee, from: "Jana Dvořáková")], ["Assignee: Jana Dvořáková → Unassigned"]),
            ("unknown field skipped", [IssueChange(field: "priority", from: "Low", to: "High"), IssueChange(field: .assignee, from: "A", to: "B")],
             ["Assignee: A → B"]),
            ("hostile values cleaned", [IssueChange(field: .status, from: "To\nDo", to: jiraRLO + jiraZWSP)], ["Status: To Do → —"]),
            ("two changes", [IssueChange(field: .status, from: "A", to: "B"), IssueChange(field: .assignee, to: "C")],
             ["Status: A → B", "Assignee: Unassigned → C"]),
        ]
        for (name, changes, want) in cases {
            #expect(Jira.eventLines(changes) == want, "\(name)")
        }
        let two = [IssueChange(field: .status, from: "A", to: "B"), IssueChange(field: .assignee, to: "C")]
        #expect(Jira.eventText(two) == "Status: A → B; Assignee: Unassigned → C")
        #expect(Jira.eventText([IssueChange(field: "labels")]) == "")
    }

    @Test func rowIssue() {
        let issue = fullIssue()
        #expect(Jira.rowIssue(issueMessage(nil, subject: "Hello")) == nil, "a mail message has no issue row")

        var comment = issueMessage(MessageIssue(info: issue, item: .comment, visibility: .internal))
        let r = Jira.rowIssue(comment)
        let want = Jira.IssueRow(
            key: "ITSD-42", summary: "VPN drops every 10 minutes", status: "In Progress", statusStyle: .inProgress,
            internal: true, internalLabel: "Internal", unread: true)
        #expect(r == want)

        comment.flags = [.flagged, .seen]
        #expect(Jira.rowIssue(comment)?.unread == false, "a seen comment is read")

        let event = issueMessage(MessageIssue(
            info: issue, item: .event, changes: [IssueChange(field: .status, from: "To Do", to: "In Progress")]))
        let e = Jira.rowIssue(event)
        #expect(e?.event == true && e?.eventText == "Status: To Do → In Progress" && e?.unread == false && e?.internal == false,
                "\(String(describing: e))")
    }

    @Test func threadRowIssue() {
        let issue = fullIssue()
        #expect(Jira.threadRowIssue(issueThread(nil, latest: nil, unread: 0, subject: "Hello")) == nil,
                "a mail conversation has no issue row")

        var thread = issueThread(
            issue,
            latest: MessageIssue(
                info: IssueInfo(key: "ITSD-42", url: "", summary: "old summary", status: ""), item: .event,
                changes: [IssueChange(field: .assignee, to: "Jana Dvořáková")]),
            unread: 2)
        let r = Jira.threadRowIssue(thread)
        #expect(r?.summary == "VPN drops every 10 minutes" && r?.event == true
                    && r?.eventText == "Assignee: Unassigned → Jana Dvořáková" && r?.unread == true,
                "latest event, 2 unread: \(String(describing: r))")

        thread.unreadCount = 0
        thread.latest.issue = MessageIssue(info: issue, item: .comment)
        let read = Jira.threadRowIssue(thread)
        #expect(read?.event == false && read?.unread == false && read?.eventText == "", "latest comment, read: \(String(describing: read))")

        thread.issue = nil
        #expect(Jira.threadRowIssue(thread)?.key == "ITSD-42", "a thread without an issue falls back to its latest member")
    }

    @Test func authBannerText() {
        let cases: [(AccountKind, ErrorCode, String)] = [
            (.jira, .authRequired, "No API token is stored for Acme"),
            (.jira, .authFailed, "The Jira site rejected the token of Acme"),
            (.jira, .keyringError, ""),
            (.imap, .authFailed, ""),
            ("", .authRequired, ""),
        ]
        for (kind, reason, want) in cases {
            #expect(Jira.authBannerText(kind: kind, reason: reason, account: "Acme") == want, "\(kind) \(reason)")
        }
    }

    @Test func isIssueURL() {
        let cloud = "https://acme.atlassian.net"
        let cases: [(String, String, String, Bool)] = [
            ("issue", "https://acme.atlassian.net/browse/ITSD-42", cloud, true),
            ("site root", "https://acme.atlassian.net", cloud, true),
            ("query and fragment", "https://acme.atlassian.net/browse/ITSD-42?focusedCommentId=7#comment-7", cloud, true),
            ("host case", "https://ACME.Atlassian.NET/browse/ITSD-42", cloud, true),
            ("scheme case", "HTTPS://acme.atlassian.net/browse/ITSD-42", cloud, true),
            ("site with a trailing slash", "https://acme.atlassian.net/browse/X-1", cloud + "/", true),
            ("site with upper case", "https://acme.atlassian.net/browse/X-1", "https://ACME.atlassian.net", true),
            ("http on an https site", "http://acme.atlassian.net/browse/ITSD-42", cloud, false),
            ("foreign host", "https://evil.example/browse/ITSD-42", cloud, false),
            ("site as a subdomain", "https://acme.atlassian.net.evil.example/browse/ITSD-42", cloud, false),
            ("subdomain of the site", "https://www.acme.atlassian.net/browse/ITSD-42", cloud, false),
            ("parent domain", "https://atlassian.net/browse/ITSD-42", cloud, false),
            ("similar host", "https://acme-atlassian.net/browse/ITSD-42", cloud, false),
            ("javascript", "javascript:alert(1)", cloud, false),
            ("javascript with the host", "javascript://acme.atlassian.net/%0aalert(1)", cloud, false),
            ("data", "data:text/html,<script>alert(1)</script>", cloud, false),
            ("file", "file:///etc/passwd", cloud, false),
            ("mailto", "mailto:jana@acme.atlassian.net", cloud, false),
            ("scheme-relative", "//acme.atlassian.net/browse/ITSD-42", cloud, false),
            ("relative", "/browse/ITSD-42", cloud, false),
            ("opaque", "https:acme.atlassian.net/browse/ITSD-42", cloud, false),
            ("userinfo", "https://jana@acme.atlassian.net/browse/ITSD-42", cloud, false),
            ("userinfo hiding the host", "https://acme.atlassian.net@evil.example/browse/ITSD-42", cloud, false),
            ("userinfo with password", "https://jana:secret@acme.atlassian.net/", cloud, false),
            ("backslash", "https://acme.atlassian.net\\@evil.example/", cloud, false),
            ("backslash in the path", "https://acme.atlassian.net/browse\\ITSD-42", cloud, false),
            ("percent-encoded host", "https://%61cme.atlassian.net/browse/ITSD-42", cloud, false),
            ("percent in the path", "https://acme.atlassian.net/browse/ITSD-42%20x", cloud, true),
            ("space", "https://acme.atlassian.net/browse/ITSD 42", cloud, false),
            ("leading space", " https://acme.atlassian.net/browse/ITSD-42", cloud, false),
            ("newline", "https://acme.atlassian.net/browse/ITSD-42\n", cloud, false),
            ("invalid UTF-8", "https://acme.atlassian.net/browse/" + jiraBytes([0xFF]), cloud, false),
            ("tab in the host", "https://acme.atlas\tsian.net/", cloud, false),
            ("bidi override", "https://acme.atlassian.net/browse/" + jiraRLO + "24-DSTI", cloud, false),
            ("trailing dot", "https://acme.atlassian.net./browse/ITSD-42", cloud, true),
            ("trailing dot on the site", "https://acme.atlassian.net/browse/ITSD-42", "https://acme.atlassian.net.", true),
            ("two trailing dots", "https://acme.atlassian.net../browse/ITSD-42", cloud, false),
            ("empty label", "https://acme..atlassian.net/browse/ITSD-42", cloud, false),
            ("only a dot", "https://./browse/ITSD-42", cloud, false),
            ("default port", "https://acme.atlassian.net:443/browse/ITSD-42", cloud, true),
            ("other port", "https://acme.atlassian.net:8443/browse/ITSD-42", cloud, false),
            ("empty port", "https://acme.atlassian.net:/browse/ITSD-42", cloud, true),
            ("bad port", "https://acme.atlassian.net:99999/browse/ITSD-42", cloud, false),
            ("zero-padded port", "https://acme.atlassian.net:0443/browse/ITSD-42", cloud, true),
            ("site with a port", "https://jira.acme.example:8443/jira/browse/WEB-1", "https://jira.acme.example:8443/jira", true),
            ("site port missing", "https://jira.acme.example/jira/browse/WEB-1", "https://jira.acme.example:8443/jira", false),
            ("http site", "http://jira.local:8080/browse/WEB-1", "http://jira.local:8080", true),
            ("https on an http site, same port", "https://jira.local:8080/browse/WEB-1", "http://jira.local:8080", true),
            ("https on an http site, default ports", "https://jira.local/browse/WEB-1", "http://jira.local", false),
            ("http site, http default port", "http://jira.local:80/browse/WEB-1", "http://jira.local", true),
            ("IPv6 site", "http://[::1]:8080/browse/WEB-1", "http://[::1]:8080", true),
            ("IPv6 case", "https://[FE80::1]/browse/WEB-1", "https://[fe80::1]", true),
            ("IPv6 zone", "https://[fe80::1%25en0]/browse/WEB-1", "https://[fe80::1]", false),
            ("IDN to punycode site", "https://bücher.example/browse/WEB-1", "https://xn--bcher-kva.example", true),
            ("punycode to IDN site", "https://xn--bcher-kva.example/browse/WEB-1", "https://bücher.example", true),
            ("IDN upper case", "https://BÜCHER.example/browse/WEB-1", "https://bücher.example", true),
            ("IDN homograph", "https://bucher.example/browse/WEB-1", "https://bücher.example", false),
            ("cyrillic a", "https://" + jiraScalar(0x0430) + "cme.atlassian.net/browse/ITSD-42", cloud, false),
            ("fullwidth dot", "https://acme" + jiraScalar(0x3002) + "atlassian.net/browse/ITSD-42", cloud, false),
            ("empty", "", cloud, false),
            ("empty site", "https://acme.atlassian.net/browse/ITSD-42", "", false),
            ("site not http", "https://acme.atlassian.net/browse/ITSD-42", "ftp://acme.atlassian.net", false),
            ("site without a scheme", "https://acme.atlassian.net/browse/ITSD-42", "acme.atlassian.net", false),
        ]
        for (name, raw, site, want) in cases {
            #expect(Jira.isIssueURL(raw, siteURL: site) == want, "\(name)")
        }
    }

    @Test func punycode() {
        // RFC 3492 §7.1 samples (B and L) and two common names.
        let cases: [(String, String)] = [
            ("bücher", "bcher-kva"),
            ("münchen", "mnchen-3ya"),
            ("他们为什么不说中文", "ihqwcrb4cv8a8dqg056pqjye"),
            ("Pročprostěnemluvíčesky", "Proprostnemluvesky-uyb24dma41a"),
            ("ü", "tda"),
            ("abc", "abc-"),
        ]
        for (input, want) in cases {
            #expect(Jira.punycode(input) == want, "\(input)")
        }
        #expect(Jira.punycode(jiraBytes([0x61, 0xFF, 0x62])) == nil, "invalid UTF-8 encodes")
    }
}
