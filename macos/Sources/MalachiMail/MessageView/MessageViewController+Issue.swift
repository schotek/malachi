// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The Change Status menu on the message view's issue card: the message on
/// display names the issue (`transitionSubject`, the pill's menu and the
/// Message menu of a message window), the pill is a menu button on an
/// account with the capability, and the transition's outcome comes back
/// through the hub (`applyIssue`, `setIssueBusy`; Integration+Jira).
extension MessageViewController {
    /// The issue of the message on display, for the menus; nil for a mail
    /// message or nothing.
    var transitionSubject: IssueActionsController.Subject? {
        issueSubject(of: current)
    }

    /// Whether the account of `s` changes statuses (`Jira.canTransition`
    /// through the controller's lookup).
    func canTransition(_ s: MessageSummary) -> Bool {
        state.hooks.issueActions?()?.canTransition(s.accountId) ?? false
    }

    /// The refreshed issue of a transition (`IssueActionsController.
    /// onIssueChanged`): the card shows it at once when it is the issue on
    /// display; the message's own summary follows with the daemon's
    /// notifications.
    func applyIssue(_ info: IssueInfo, account: AccountID) {
        guard let s = current, s.accountId == account, let item = s.issue, Jira.clean(item.info.key) == Jira.clean(info.key) else {
            return
        }
        let site = delegate?.account(account)?.config.jira?.siteUrl ?? ""
        let card = Jira.issueCard(info, item: item)
        header.issueCard.show(card, openable: !card.url.isEmpty && Jira.isIssueURL(card.url, siteURL: site), transitions: canTransition(s))
        header.subject = card.summary.isEmpty ? header.subject : card.summary
    }

    /// A transition started or ended on the issue `key` of `account`
    /// (`IssueActionsController.onBusy`): the pill's spinner.
    func setIssueBusy(_ busy: Bool, account: AccountID, key: String) {
        guard let s = current, s.accountId == account else { return }
        header.issueCard.setBusy(busy, key: key)
    }

    /// The spinner as the controller knows it, for a card just shown.
    func refreshIssueBusy() {
        guard let s = current, let item = s.issue, let c = state.hooks.issueActions?() else { return }
        header.issueCard.setBusy(c.isBusy(account: s.accountId, key: item.info.key), key: item.info.key)
    }
}
