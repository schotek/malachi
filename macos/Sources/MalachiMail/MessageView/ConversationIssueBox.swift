// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The issue card of a Jira conversation at the top of a layout's column
/// (the timeline's and the chat's each have one, ConversationHostViewController):
/// its key opens the issue only on the account's own site, its status pill
/// is the Change Status menu on an account that changes statuses, and its
/// spinner shows a transition under way. Hidden for a mail conversation.
/// `leading` keeps the card in the timeline's column of cards, beside its
/// gutter; the chat's card spans the column.
@MainActor
final class ConversationIssueBox: NSView {
    private let state: AppState
    private let controller: ConversationController
    private let card = IssueCardView()

    init(state: AppState, controller: ConversationController, leading: CGFloat) {
        self.state = state
        self.controller = controller
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        addSubview(card)
        NSLayoutConstraint.activate([
            card.topAnchor.constraint(equalTo: topAnchor, constant: ConversationMetrics.itemGap),
            card.bottomAnchor.constraint(equalTo: bottomAnchor),
            card.leadingAnchor.constraint(equalTo: leadingAnchor, constant: leading),
            card.trailingAnchor.constraint(equalTo: trailingAnchor),
        ])
        isHidden = true
        card.onOpen = { [weak self] url in self?.openIssueLink(url) }
        card.statusMenu = IssueTransitionMenu(state: state) { [weak self] in self?.transitionSubject }
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    /// The issue card of `model` (nil: none shown), once on top.
    func show(_ model: Conversation.Model?) {
        guard let model, let issue = model.issue, let account = firstMember(model)?.accountId else {
            card.show(nil, openable: false)
            isHidden = true
            return
        }
        showCard(issue, account: account)
        isHidden = false
    }

    private func showCard(_ issue: Jira.Card, account: AccountID) {
        let site = controller.issueSite(account)
        let actions = state.hooks.issueActions?()
        card.show(
            issue, openable: !issue.url.isEmpty && Jira.isIssueURL(issue.url, siteURL: site),
            transitions: actions?.canTransition(account) ?? false)
        if let actions {
            card.setBusy(actions.isBusy(account: account, key: issue.key), key: issue.key)
        }
    }

    private func firstMember(_ model: Conversation.Model) -> MessageSummary? {
        model.items.first { $0.kind != .truncated }?.message
    }

    /// The issue of the conversation on show, for the Change Status menu:
    /// any member names it (the first one here); nil for a mail
    /// conversation or none.
    var transitionSubject: IssueActionsController.Subject? {
        guard let model = controller.model, model.issue != nil else { return nil }
        return issueSubject(of: firstMember(model))
    }

    /// The refreshed issue of a transition (`IssueActionsController.
    /// onIssueChanged`): the card shows it at once when it is the issue
    /// on show; the members follow with the daemon's notifications.
    func applyIssue(_ info: IssueInfo, account: AccountID) {
        guard let model = controller.model, let issue = model.issue, let member = firstMember(model),
              member.accountId == account, Jira.clean(info.key) == issue.key else { return }
        showCard(Jira.issueCard(info), account: account)
    }

    /// A transition started or ended on the issue `key` of `account`
    /// (`IssueActionsController.onBusy`): the pill's spinner.
    func setBusy(_ busy: Bool, account: AccountID, key: String) {
        guard let model = controller.model, let member = firstMember(model), member.accountId == account else { return }
        card.setBusy(busy, key: key)
    }

    private func openIssueLink(_ url: String) {
        guard let model = controller.model, let account = firstMember(model)?.accountId else { return }
        let window = window
        let toasts = state.toasts
        openIssue(url, site: controller.issueSite(account)) { text in
            windowToast(text, in: window, or: toasts)
        }
    }
}
