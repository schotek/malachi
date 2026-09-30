// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The Change Status menu of a Jira issue without the widgets
/// (ui/internal/jira/transitions.go for the texts and items; Swift-first:
/// the GTK menu follows): `loadTransitions` asks issue.transitions for the
/// issue of a message and hands the menu its items, `perform` calls
/// issue.transition for the chosen one, shows the toast and hands every
/// view the refreshed issue (`onIssueChanged`), so the card changes at
/// once, before the daemon's refresh reaches the list and the pane through
/// the usual notifications. Only an account with `Capability.transition`
/// (`account`, the window's lookup) gets either call.
///
/// Stale replies: a menu opens, closes and opens again while the site is
/// slow; only the newest load's reply reaches its `completion`
/// (`cancelLoad` drops the running one's when the menu closes). A
/// transition runs at most once per issue at a time (`isBusy`): the pill
/// shows a spinner meanwhile (`onBusy`) and the menu refuses a second
/// choice.
@MainActor
public final class IssueActionsController {
    /// What the menu acts on: the issue of a message (any message of it;
    /// the message's thread is the issue).
    public struct Subject: Sendable, Hashable {
        public var accountId: AccountID
        public var messageId: MessageID

        public init(accountId: AccountID, messageId: MessageID) {
            self.accountId = accountId
            self.messageId = messageId
        }
    }

    /// What issue.transitions answered, as the menu shows it.
    public struct Loaded: Sendable, Equatable {
        /// The issue as the daemon last synchronised it.
        public var issue: IssueInfo
        public var items: [Jira.TransitionItem]

        public init(issue: IssueInfo, items: [Jira.TransitionItem]) {
            self.issue = issue
            self.items = items
        }
    }

    /// An issue a transition runs on.
    private struct Running: Hashable {
        var account: AccountID
        var key: String
    }

    /// The account of an id as the window knows it; nil (unknown) offers
    /// nothing.
    public var account: @MainActor (AccountID) -> Account?
    /// Called when a transition starts (`busy` true) and when it ended
    /// either way, with the issue's key as the daemon knows it.
    public var onBusy: (@MainActor (_ account: AccountID, _ key: String, _ busy: Bool) -> Void)?
    /// Called with the issue issue.transition returned: the views showing
    /// it apply it to their cards.
    public var onIssueChanged: (@MainActor (_ account: AccountID, _ issue: IssueInfo) -> Void)?
    /// The toasts (window.go `Toast`).
    public var toast: @MainActor (String) -> Void

    private let client: RPCClient
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "issues")
    /// The load whose reply is still wanted; older ones are dropped.
    private var loadGeneration = 0
    private var running: Set<Running> = []

    public init(client: RPCClient, account: @escaping @MainActor (AccountID) -> Account?, toast: @escaping @MainActor (String) -> Void) {
        self.client = client
        self.account = account
        self.toast = toast
    }

    /// Whether the account offers the menu (`Jira.canTransition`).
    public func canTransition(_ id: AccountID) -> Bool {
        guard let a = account(id) else { return false }
        return Jira.canTransition(a)
    }

    /// Whether a transition runs on the issue `key` of `account` right now.
    public func isBusy(account: AccountID, key: String) -> Bool {
        running.contains(Running(account: account, key: key))
    }

    /// Asks issue.transitions for the issue of `subject` and gives
    /// `completion` the items (or the error, for the menu's only item:
    /// `rpcErrorText(Jira.loadTransitionsAction(), error)`), unless a newer
    /// load started or `cancelLoad` was called meanwhile. Returns false,
    /// and asks nothing, when the account cannot change statuses.
    @discardableResult
    public func loadTransitions(
        _ subject: Subject, completion: @escaping @MainActor (Result<Loaded, any Error>) -> Void
    ) -> Bool {
        guard canTransition(subject.accountId) else { return false }
        loadGeneration += 1
        let generation = loadGeneration
        let client = client
        let params = IssueTransitionsParams(accountId: subject.accountId, messageId: subject.messageId)
        Task { [weak self] in
            let outcome: Result<IssueTransitionsResult, any Error>
            do {
                outcome = .success(try await client.call(API.IssueTransitions.self, params))
            } catch {
                outcome = .failure(error)
            }
            guard let self, self.loadGeneration == generation else { return }
            switch outcome {
            case .success(let res):
                completion(.success(Loaded(issue: res.issue, items: Jira.transitions(res))))
            case .failure(let err):
                self.log.warning("issue.transitions: \(String(describing: err), privacy: .public)")
                completion(.failure(err))
            }
        }
        return true
    }

    /// Drops the reply of the load that runs, if any (the menu closed).
    public func cancelLoad() {
        loadGeneration += 1
    }

    /// Performs `item` on the issue of `subject` (`issue` is what
    /// issue.transitions returned with it: its key names the spinner).
    /// Nothing happens for a disabled item, for an account without the
    /// capability, or while a transition already runs on the issue. On
    /// success the toast says the new status and `onIssueChanged` carries
    /// the refreshed issue; on failure the toast says why. Returns whether
    /// the call was made.
    @discardableResult
    public func perform(_ subject: Subject, _ item: Jira.TransitionItem, issue: IssueInfo) -> Bool {
        guard item.enabled, !item.id.isEmpty, canTransition(subject.accountId) else { return false }
        let key = Running(account: subject.accountId, key: issue.key)
        guard !running.contains(key) else { return false }
        running.insert(key)
        onBusy?(subject.accountId, issue.key, true)
        let client = client
        let params = IssueTransitionParams(accountId: subject.accountId, messageId: subject.messageId, transitionId: item.id)
        Task { [weak self] in
            let outcome: Result<IssueTransitionResult, any Error>
            do {
                outcome = .success(try await client.call(API.IssueTransition.self, params))
            } catch {
                outcome = .failure(error)
            }
            guard let self else { return }
            self.running.remove(key)
            switch outcome {
            case .success(let res):
                self.onIssueChanged?(subject.accountId, res.issue)
                self.toast(Jira.statusChanged(item, res.issue))
            case .failure(let err):
                self.log.warning("issue.transition: \(String(describing: err), privacy: .public)")
                self.toast(Jira.transitionFailed(err))
            }
            self.onBusy?(subject.accountId, issue.key, false)
        }
        return true
    }
}
