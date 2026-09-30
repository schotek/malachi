// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

// ui/internal/jira/transitions_test.go (English catalogue): the items of
// the Change Status menu and its texts.

private let rlo = jiraScalar(0x202E) // RIGHT-TO-LEFT OVERRIDE
private let zwsp = jiraScalar(0x200B) // ZERO WIDTH SPACE
private let shy = jiraScalar(0x00AD) // SOFT HYPHEN

private func account(_ caps: [Capability]?) -> Account {
    Account(
        id: "j", config: AccountConfig(name: "Acme Jira", email: "jana@acme.example", kind: .jira), enabled: true,
        state: SyncState(accountId: "j", status: .idle), capabilities: caps
    )
}

private func info(_ status: String) -> IssueInfo {
    IssueInfo(key: "ITSD-42", url: "https://acme.atlassian.net/browse/ITSD-42", summary: "Printer", status: status)
}

struct JiraTransitionsTests {
    @Test func canTransition() {
        #expect(Jira.canTransition(account([.comment, .forward, .transition])), "an account with the transition capability offers the menu")
        #expect(!Jira.canTransition(account([.comment, .forward])), "an account without it does not")
        #expect(!Jira.canTransition(account([])), "an empty list offers nothing")
        #expect(!Jira.canTransition(account(nil)), "a mail account (nil capabilities, the mail default) never changes statuses")
    }

    @Test func transitions() {
        let res = IssueTransitionsResult(
            issue: IssueInfo(key: "ITSD-42", url: "", summary: "", status: "To Do", statusCategory: .todo),
            transitions: [
                IssueTransition(id: "11", name: "Start Progress", to: "In Progress", toCategory: .inProgress),
                IssueTransition(id: "21", name: "Done", to: "Done", toCategory: .done),
                IssueTransition(id: "31", name: "Resolve", to: "Resolved", needsInput: true),
                IssueTransition(id: "41", name: " " + rlo + "Escalate\n", to: "escalated" + zwsp, needsInput: true),
                IssueTransition(id: "  ", name: "No id", to: "Nowhere"),
                IssueTransition(id: "51", name: "", to: "Closed"),
                IssueTransition(id: "61", name: zwsp + shy, to: rlo),
                IssueTransition(id: "71", name: "DONE", to: "Done", needsInput: false),
                IssueTransition(id: "81", name: "Back to the backlog", to: "to do", needsInput: false),
            ]
        )
        let want = [
            Jira.TransitionItem(id: "11", title: "Start Progress", target: "In Progress", subtitle: "In Progress", enabled: true),
            Jira.TransitionItem(id: "21", title: "Done", target: "Done", enabled: true),
            Jira.TransitionItem(id: "31", title: "Resolve", target: "Resolved", subtitle: "Resolved", hint: "Needs fields in Jira"),
            Jira.TransitionItem(id: "41", title: "Escalate", target: "escalated", subtitle: "escalated", hint: "Needs fields in Jira"),
            Jira.TransitionItem(id: "51", title: "Closed", target: "Closed", enabled: true),
            Jira.TransitionItem(id: "71", title: "DONE", target: "Done", enabled: true),
        ]
        #expect(Jira.transitions(res) == want)
        #expect(Jira.transitions(IssueTransitionsResult(issue: info("To Do"))).isEmpty)
    }

    @Test func transitionsCap() {
        let many = (0..<(API.Limits.maxIssueTransitions + 5)).map {
            IssueTransition(id: String(repeating: "1", count: $0 + 1), name: "t", to: "")
        }
        #expect(Jira.transitions(IssueTransitionsResult(issue: info("x"), transitions: many)).count == API.Limits.maxIssueTransitions)
        #expect(API.Limits.maxIssueTransitions == 100, "api.MaxIssueTransitions")
    }

    @Test func texts() {
        #expect(Jira.changeStatusLabel() == "Change Status")
        #expect(Jira.needsInputHint() == "Needs fields in Jira")
        #expect(Jira.transitionsLoading() == "Loading…")
        #expect(Jira.noTransitions() == "No status change is available")
        #expect(Jira.loadTransitionsAction() == "Loading the status changes")
        #expect(Jira.transitionAction() == "Changing the status")
    }

    @Test func statusChanged() {
        let refreshed = info("In Progress")
        let stale = info("To Do")
        let start = Jira.TransitionItem(title: "Start Progress", target: "In Progress")
        #expect(Jira.statusChanged(start, refreshed) == "Status changed to In Progress", "the target wins")
        #expect(Jira.statusChanged(start, stale) == "Status changed to In Progress", "even over a stale issue")
        #expect(Jira.statusChanged(Jira.TransitionItem(title: "Start Progress"), refreshed) == "Status changed to In Progress",
                "no target: the issue's status")
        #expect(Jira.statusChanged(Jira.TransitionItem(title: "Start Progress"), info(rlo + " ")) == "Status changed to Start Progress",
                "neither: the transition's name")
    }

    @Test func transitionFailed() {
        let fallback = "Changing the status failed: the server returned an error"
        let cases: [(String, ErrorCode, String, String)] = [
            ("the site's reason", .serverError, "Transition is not allowed by the workflow",
             "The status could not be changed: Transition is not allowed by the workflow"),
            ("cleaned", .serverError, " " + rlo + "Not\nallowed" + zwsp, "The status could not be changed: Not allowed"),
            ("no reason: the usual sentence", .serverError, zwsp, fallback),
            ("another code: the usual sentence", .invalidArgument, "needs input", fallback),
            ("network", .networkError, "dial tcp: refused", fallback),
        ]
        for (name, code, message, want) in cases {
            #expect(Jira.transitionFailed(code: code, message: message, fallback: fallback) == want, "\(name)")
        }
        let long = Jira.transitionFailed(code: .serverError, message: String(repeating: "x", count: Jira.maxText + 10), fallback: fallback)
        #expect(long.utf8.count <= "The status could not be changed: ".utf8.count + Jira.maxText, "the reason is capped")

        // The overload over any error: the daemon's error, or the client's own.
        #expect(Jira.transitionFailed(RPCError(code: .serverError, message: "Not allowed")) == "The status could not be changed: Not allowed")
        #expect(Jira.transitionFailed(RPCError(code: .networkError, message: "refused"))
                    == "Changing the status failed: the server could not be reached")
        #expect(Jira.transitionFailed(RPCError(code: .methodNotFound, message: "unknown")) == "Changing the status is not available yet")
        #expect(Jira.transitionFailed(RPCClient.ClientError.timeout(method: "issue.transition")) == "Changing the status timed out")
    }
}
