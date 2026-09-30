// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/jira/compose_test.go (English catalogue). Go's nil list of
// visibility options is an empty array here.

/// compose_test.go `vis`.
private func vis(_ v: CommentVisibility...) -> IssueInfo {
    IssueInfo(key: "ITSD-42", url: "", summary: "", status: "", commentVisibilities: v)
}

struct JiraComposeTests {
    @Test func visibilityOptions() {
        let both = [
            Jira.VisibilityOption(visibility: .public, label: "Reply to Customer"),
            Jira.VisibilityOption(visibility: .internal, label: "Internal Note"),
        ]
        let cases: [(String, IssueInfo, [Jira.VisibilityOption])] = [
            ("none", vis(), []),
            ("public only", vis(.public), []),
            ("internal only", vis(.internal), []),
            ("both", vis(.public, .internal), both),
            ("both, other order", vis(.internal, .public), both),
            ("twice public", vis(.public, .public), []),
            ("unknown value", vis(.public, .internal, "partners"), []),
        ]
        for (name, issue, want) in cases {
            #expect(Jira.visibilityOptions(issue) == want, "\(name)")
        }
    }

    @Test func selectedVisibility() {
        let cases: [(DraftComment, CommentVisibility)] = [
            (DraftComment(issue: vis(.public, .internal)), .public),
            (DraftComment(issue: vis(.public, .internal), visibility: .internal), .internal),
            (DraftComment(issue: vis(.public, .internal), visibility: .public), .public),
            (DraftComment(issue: vis(), visibility: .internal), .public),
            (DraftComment(issue: vis(.public, .internal), visibility: "secret"), .public),
        ]
        for (i, c) in cases.enumerated() {
            #expect(Jira.selectedVisibility(c.0) == c.1, "case \(i)")
        }
    }

    @Test func commentCompose() {
        #expect(Jira.commentCompose(Draft(accountId: "a1", subject: "Re: hello")) == nil, "a mail draft has no comment mode")

        let d = Draft(accountId: "j1", comment: DraftComment(
            issue: IssueInfo(key: "ITSD-42" + jiraRLO, url: "", summary: "", status: "", commentVisibilities: [.public, .internal]),
            visibility: .internal))
        let w = Jira.commentCompose(d)
        #expect(w?.title == "Comment on ITSD-42")
        #expect(w?.visibilities.count == 2)
        #expect(w?.visibility == .internal)
        #expect(w?.formats == Jira.commentFormats)

        var changed = w
        changed?.formats[0] = .image
        #expect(Jira.commentFormats[0] == .bold, "changing the window's formats changed commentFormats")

        let plain = Jira.commentCompose(Draft(accountId: "j1", comment: DraftComment(issue: IssueInfo(key: "WEB-7", url: "", summary: "", status: ""))))
        #expect(plain?.visibilities.isEmpty == true && plain?.visibility == .public && plain?.title == "Comment on WEB-7",
                "\(String(describing: plain))")
    }

    @Test func commentAllows() {
        let allowed: [(Jira.Format, Bool)] = [
            (.bold, true), (.italic, true), (.code, true), (.link, true),
            (.bulletList, true), (.numberedList, true), (.quote, true), (.clear, true),
            (.underline, false), (.heading, false), (.alignment, false), (.colour, false), (.image, false),
            ("strike", false),
        ]
        for (f, want) in allowed {
            #expect(Jira.commentAllows(f) == want, "\(f)")
        }
    }

    @Test func sendProblem() {
        let empty = "Write a comment first"
        let cases: [(String, String)] = [
            ("", empty),
            (" \n\t ", empty),
            (jiraZWSP + jiraBOM + " " + jiraSHY, empty),
            (jiraScalar(0x00A0), empty), // NO-BREAK SPACE, as an empty editor paragraph leaves
            ("Restarted the VPN concentrator", ""),
            (" ok ", ""),
        ]
        for (input, want) in cases {
            #expect(Jira.sendProblem(input) == want, "\(input.debugDescription)")
        }
    }

    @Test func composeLabels() {
        #expect(Jira.replyLabel(comment: true) == "Comment")
        #expect(Jira.replyLabel(comment: false) == "Reply")
        #expect(Jira.commentQueued() == "Comment queued")
        #expect(Jira.commentTitle(" WEB-7\n") == "Comment on WEB-7")
    }
}
