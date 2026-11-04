// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/conversation_issue.go (convIssueSlot:
// showIssue, clearIssue, conversationIssueCard, conversationSetIssueBusy,
// conversationApplyIssue, convFirstMember); macOS:
// MessageView/ConversationIssueBox.swift. The issue card of a Jira
// conversation at the top of a layout's column: the timeline and the chat
// each have one (docs/plans/chat-view.md §6.1; ConversationView, their
// host, hands both the busy state and the refreshed issue of a
// transition). Its key opens the issue only on the account's own site, its
// status pill is the Change Status menu on an account that changes
// statuses, and its spinner shows a transition under way; a mail
// conversation has none (the card hides while its state is null). This
// class is the card's driver, not a view: the layout places the card
// (beside the timeline's gutter, across the chat's column).

using System;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Model;
using Malachi.Core.Presentation;

namespace Malachi.App.Reader.Conversation;

/// <summary>Drives the issue card of a layout of the conversation.</summary>
internal sealed class ConversationIssueBox
{
    private static readonly JiraCardRow[] NoRows = [];

    private readonly IssueCardView card;
    private readonly ReaderServices services;
    private readonly ConversationController ctrl;

    // The account the card on show acts in.
    private AccountId? account;

    /// <summary>The driver of <paramref name="card"/>; <paramref name="openIssue"/> opens the issue's URL.</summary>
    public ConversationIssueBox(IssueCardView card, ReaderServices services, ConversationController ctrl, Action<string> openIssue)
    {
        ArgumentNullException.ThrowIfNull(card);
        ArgumentNullException.ThrowIfNull(services);
        ArgumentNullException.ThrowIfNull(ctrl);
        this.card = card;
        this.services = services;
        this.ctrl = ctrl;
        card.Issues = services.Issues;
        card.Subject = () => FirstMember(ctrl.Model) is { } first ? IssueActionsController.SubjectOf(first) : null;
        card.OpenIssue = openIssue;
        card.State = null;
    }

    /// <summary>
    /// convFirstMember: the first member of <paramref name="m"/>, which names
    /// the conversation's account (and its issue).
    /// </summary>
    public static MessageSummary? FirstMember(ConversationModel? m) =>
        m?.Items.FirstOrDefault(it => it.Kind != ConversationItemKind.Truncated)?.Message;

    /// <summary>showIssue: the issue card of a Jira conversation; a mail conversation has none.</summary>
    public void Show(ConversationModel? m)
    {
        if (m?.Issue is not { } issue || FirstMember(m) is not { } first)
        {
            Clear();
            return;
        }
        var state = CardState(issue, first.AccountId);
        if (card.State is { } shown && account == first.AccountId && SameCard(shown, state))
        {
            return;
        }
        account = first.AccountId;
        card.State = state;
    }

    /// <summary>clearIssue: no card.</summary>
    public void Clear()
    {
        account = null;
        card.State = null;
    }

    /// <summary>conversationSetIssueBusy: a transition started or ended on an issue; the card follows when it shows that issue.</summary>
    public void BusyChanged(AccountId issueAccount, string key)
    {
        if (card.State is { } st && st.Account == issueAccount && st.Card.Key == Jira.Clean(key))
        {
            var busy = services.Issues.IsBusy(issueAccount, key);
            if (busy != st.Busy)
            {
                card.State = st with { Busy = busy };
            }
        }
    }

    /// <summary>conversationApplyIssue: the refreshed issue of a transition on the card at once when it is the issue on show.</summary>
    public void Apply(AccountId issueAccount, IssueInfo info)
    {
        ArgumentNullException.ThrowIfNull(info);
        if (card.State is not { } st || st.Account != issueAccount || st.Card.Key != Jira.Clean(info.Key))
        {
            return;
        }
        card.State = CardState(Jira.IssueCard(info, null), issueAccount);
    }

    /// <summary>win.change-status: the card's Change Status menu; false when there is none.</summary>
    public bool OpenStatusMenu() => card.OpenStatusMenu();

    // conversation_issue.go conversationIssueCard: the key a link on the
    // account's own site only, the status pill the Change Status menu on an
    // account that changes statuses.
    private IssueCardState CardState(JiraCard issue, AccountId issueAccount) => new()
    {
        Card = issue,
        Account = issueAccount,
        Openable = issue.Url.Length > 0 && Jira.IsIssueUrl(issue.Url, ctrl.IssueSite(issueAccount)),
        Menu = services.Issues.CanTransition(issueAccount) && issue.Status.Length > 0,
        Busy = issue.Key.Length > 0 && services.Issues.IsBusy(issueAccount, issue.Key),
    };

    // The same card, by value (a record compares its lists by reference, and
    // JiraCard is no API type the JSON context could encode).
    private static bool SameCard(IssueCardState a, IssueCardState b) =>
        a.Account == b.Account && a.Openable == b.Openable && a.Menu == b.Menu && a.Busy == b.Busy
        && a.Card with { Rows = NoRows } == b.Card with { Rows = NoRows }
        && a.Card.Rows.SequenceEqual(b.Card.Rows);
}
