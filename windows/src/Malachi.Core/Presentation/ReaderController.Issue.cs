// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the Jira half of ui/internal/window/message_view.go
// (renderHeaders' issue card and subject, renderEvent, applyIssue, and
// showMessage's readsWithoutBody) and issue_card.go (show, setBusy);
// macOS: MessageView/MessageViewController+Issue.swift. A message of a Jira
// account gets the issue card over its headers (IssueCard), the issue's
// summary is its subject (the card has the key), and an event (a status or
// assignee change) is its changes in place of a body, which is never
// fetched. Not in an attached message's view, which has no card.

using System;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Model;

namespace Malachi.Core.Presentation;

/// <content>The issue card of a Jira message.</content>
public sealed partial class ReaderController
{
    /// <summary>
    /// The site of a Jira account (<see cref="JiraConfig.SiteUrl"/>, "" when
    /// unknown or not Jira): the only site the card's key may open. Set by
    /// the application; without it no key opens.
    /// </summary>
    public Func<AccountId, string>? IssueSite { get; set; }

    /// <summary>Whether an account changes the statuses of its issues (<see cref="Jira.CanTransition"/>).</summary>
    public Func<AccountId, bool>? CanTransition { get; set; }

    /// <summary>Whether a transition runs on an issue key of an account (<see cref="Controllers.IssueActionsController.IsBusy"/>).</summary>
    public Func<AccountId, string, bool>? IssueBusy { get; set; }

    /// <summary>The issue card over the headers; null hides it (a mail message).</summary>
    [ObservableProperty]
    public partial IssueCardState? IssueCard { get; private set; }

    /// <summary>
    /// message_view.go <c>applyIssue</c>: the refreshed issue of a transition
    /// (<see cref="Controllers.IssueActionsController.IssueChanged"/>) on the
    /// card at once when it is the issue on display; the message's own
    /// summary follows with the daemon's notifications.
    /// </summary>
    public void ApplyIssue(AccountId account, IssueInfo info)
    {
        ArgumentNullException.ThrowIfNull(info);
        if (closed || Mode == ReaderMode.Embedded || Current is not { Issue: { } item } s || s.AccountId != account
            || Jira.Clean(item.Key) != Jira.Clean(info.Key))
        {
            return;
        }
        var refreshed = s with { Issue = item.WithInfo(info) };
        if (IssueReading.Read(refreshed, null, Site(account)) is not { } r)
        {
            return;
        }
        IssueCard = CardOf(r, account);
        Subject = r.Subject;
    }

    /// <summary>
    /// issue_card.go <c>setBusy</c>: a transition started or ended on the
    /// issue <paramref name="key"/> (as the daemon names it) of
    /// <paramref name="account"/>; the card shows the spinner while one runs
    /// on its issue, another issue's is ignored.
    /// </summary>
    public void IssueBusyChanged(AccountId account, string key)
    {
        ArgumentNullException.ThrowIfNull(key);
        if (IssueCard is not { } card || card.Account != account || card.Card.Key != Jira.Clean(key))
        {
            return;
        }
        var busy = IssueBusy?.Invoke(account, key) ?? false;
        if (busy != card.Busy)
        {
            IssueCard = card with { Busy = busy };
        }
    }

    // renderHeaders' issue half: the card and the subject of a Jira message;
    // null for mail.
    private IssueReading? RenderIssue(MessageSummary s, Message? m)
    {
        if (Mode == ReaderMode.Embedded || IssueReading.Read(s, m, Site(s.AccountId)) is not { } r)
        {
            IssueCard = null;
            return null;
        }
        IssueCard = CardOf(r, s.AccountId);
        Subject = r.Subject;
        return r;
    }

    // renderEvent: an event of an issue (a status or assignee change) in
    // place of a body: its changes as sentences, no bars.
    private void RenderEvent(string text)
    {
        CancelSpinner();
        Links = [];
        HintVisible = false;
        HideBars();
        ShowText(text);
    }

    private IssueCardState CardOf(IssueReading r, AccountId account) => new()
    {
        Card = r.Card,
        Account = account,
        Openable = r.Openable,
        Menu = (CanTransition?.Invoke(account) ?? false) && r.Card.Status.Length > 0,
        Busy = r.Card.Key.Length > 0 && (IssueBusy?.Invoke(account, r.Card.Key) ?? false),
    };

    private string Site(AccountId account) => IssueSite?.Invoke(account) ?? "";
}
