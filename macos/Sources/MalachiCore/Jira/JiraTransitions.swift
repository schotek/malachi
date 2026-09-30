// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// ui/internal/jira/transitions.go: the Change Status menu of a Jira issue
// (the status pill of the issue card, More Actions, the Message menu) on
// an account with `Capability.transition`: the menu lists what
// issue.transitions returned, in the site's order, the transitions that
// need fields in Jira disabled with a hint, and choosing one calls
// issue.transition (`IssueActionsController`). The daemon refreshes the
// issue right after, so the event row and the new status arrive through
// the usual notifications; the client applies the result's issue to the
// card at once all the same. One to one with the Go file; every text goes
// through L10n with the GTK msgid as the key.

import Foundation

extension Jira {
    /// jira.TransitionItem: one entry of the Change Status menu.
    public struct TransitionItem: Sendable, Equatable {
        /// Goes to issue.transition.
        public var id = ""
        /// The transition's name, as the site's own status menu shows it;
        /// `target` the status it leads to ("" when the site did not say).
        public var title = ""
        public var target = ""
        /// `target` when it differs from `title` ("Start Progress" → "In
        /// Progress"), "" otherwise.
        public var subtitle = ""
        /// False for a transition that needs fields in Jira (`needsInput`):
        /// the item is listed disabled, with `hint` saying why ("" when
        /// enabled).
        public var enabled = false
        public var hint = ""

        public init(id: String = "", title: String = "", target: String = "", subtitle: String = "", enabled: Bool = false, hint: String = "") {
            self.id = id
            self.title = title
            self.target = target
            self.subtitle = subtitle
            self.enabled = enabled
            self.hint = hint
        }
    }

    /// jira.CanTransition: an account whose issues offer the Change Status
    /// menu.
    public static func canTransition(_ a: Account) -> Bool {
        a.can(.transition)
    }

    /// jira.Transitions: the items of the Change Status menu from the
    /// result of issue.transitions: the site's order, every string
    /// cleaned, a transition without an id skipped, one without a name
    /// shown by its target, at most `API.Limits.maxIssueTransitions`. A
    /// transition that leads to the status the issue already has is left
    /// out: a workflow with global transitions offers one for every
    /// status, the current one included.
    public static func transitions(_ res: IssueTransitionsResult) -> [TransitionItem] {
        var out: [TransitionItem] = []
        out.reserveCapacity(res.transitions.count)
        let current = clean(res.issue.status)
        for t in res.transitions {
            let id = trimSpace(t.id)
            if id.isEmpty {
                continue
            }
            var title = clean(t.name)
            let target = clean(t.to)
            if title.isEmpty {
                title = target
            }
            if title.isEmpty {
                continue
            }
            if !current.isEmpty, !target.isEmpty, equalFold(target, current) {
                continue
            }
            let needsInput = t.needsInput == true
            var item = TransitionItem(id: id, title: title, target: target, enabled: !needsInput)
            if !target.isEmpty, !equalFold(target, title) {
                item.subtitle = target
            }
            if needsInput {
                item.hint = needsInputHint()
            }
            out.append(item)
            if out.count == API.Limits.maxIssueTransitions {
                break
            }
        }
        return out
    }

    /// strings.EqualFold: equal under Unicode simple case folding.
    private static func equalFold(_ a: String, _ b: String) -> Bool {
        a.unicodeScalars.count == b.unicodeScalars.count
            && zip(a.unicodeScalars, b.unicodeScalars).allSatisfy { fold($0) == fold($1) }
    }

    private static func fold(_ r: Unicode.Scalar) -> String {
        r.properties.lowercaseMapping
    }

    /// jira.ChangeStatusLabel: the title of the menu.
    public static func changeStatusLabel() -> String {
        // TRANSLATORS: menu of the status changes a Jira issue allows.
        L10n.T("Change Status")
    }

    /// jira.NeedsInputHint: why a transition is listed disabled.
    public static func needsInputHint() -> String {
        // TRANSLATORS: a status change of a Jira issue asks for more on the site and cannot be made here.
        L10n.T("Needs fields in Jira")
    }

    /// jira.TransitionsLoading: the menu's only item while
    /// issue.transitions runs.
    public static func transitionsLoading() -> String {
        L10n.T("Loading…")
    }

    /// jira.NoTransitions: the menu's only item when the site offers no
    /// status change on the issue.
    public static func noTransitions() -> String {
        // TRANSLATORS: the only item of the Change Status menu of a Jira issue whose status cannot be changed.
        L10n.T("No status change is available")
    }

    /// jira.LoadTransitionsAction: what failed when issue.transitions did,
    /// in the progressive form `rpcErrorText` takes ("Loading the status
    /// changes failed: …"); the menu's only item then.
    public static func loadTransitionsAction() -> String {
        // TRANSLATORS: progressive; %s of "%s failed: …" when the status changes of a Jira issue could not be listed.
        L10n.T("Loading the status changes")
    }

    /// jira.TransitionAction: what failed when issue.transition did, in
    /// the progressive form `rpcErrorText` takes ("Changing the status
    /// timed out").
    public static func transitionAction() -> String {
        // TRANSLATORS: progressive; %s of "%s failed: …" when the status of a Jira issue could not be changed.
        L10n.T("Changing the status")
    }

    /// jira.StatusChanged: the toast after issue.transition: the status
    /// the chosen transition leads to (the change was made even when the
    /// daemon's refresh did not finish in time, and `issue` then still
    /// shows the old status), else the refreshed issue's status, else the
    /// transition's name.
    public static func statusChanged(_ chosen: TransitionItem, _ issue: IssueInfo) -> String {
        var status = chosen.target
        if status.isEmpty {
            status = clean(issue.status)
        }
        if status.isEmpty {
            status = chosen.title
        }
        // TRANSLATORS: toast; %s is the new status of a Jira issue, such as "In Progress".
        return L10n.T("Status changed to %s", status)
    }

    /// jira.TransitionFailed: the toast of a failed issue.transition: the
    /// site's own reason when it refused the change (a serverError whose
    /// message the daemon took from the site; cleaned again here), else
    /// `fallback`, the client's usual sentence for the error
    /// (`rpcErrorText` with `transitionAction`).
    public static func transitionFailed(code: ErrorCode, message: String, fallback: String) -> String {
        if code == .serverError {
            let reason = clean(message)
            if !reason.isEmpty {
                // TRANSLATORS: toast; %s is the reason the Jira site gave, in its own words.
                return L10n.T("The status could not be changed: %s", reason)
            }
        }
        return fallback
    }

    /// `transitionFailed` for any error of the call: the daemon's code and
    /// message when it is an `RPCError`, the usual sentence otherwise.
    public static func transitionFailed(_ error: any Error) -> String {
        let fallback = rpcErrorText(transitionAction(), error)
        guard let e = error as? RPCError else { return fallback }
        return transitionFailed(code: e.code, message: e.message, fallback: fallback)
    }
}
