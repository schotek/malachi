// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/IssueActionsController.swift;
// GTK: ui/internal/window/issue_actions.go (issueActions, subjectOf). The
// texts and items are ui/internal/jira/transitions.go
// (Jira.Transitions.cs).
//
// The Swift callbacks are events: onBusy is BusyChanged, onIssueChanged
// IssueChanged, and the toast closure ToastRequested; account stays a
// lookup given to the constructor. A load goes through the scope and is
// dropped once the window closed; a transition is sent even when the
// window closes right after the choice (PerformPastClose, a mutation on
// the way out), its reply reaching only an open window.

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// The Change Status menu of a Jira issue without the widgets:
/// <see cref="LoadTransitions"/> asks <c>issue.transitions</c> for the issue
/// of a message and hands the menu its items, <see cref="Perform"/> calls
/// <c>issue.transition</c> for the chosen one, shows the toast and hands
/// every view the refreshed issue (<see cref="IssueChanged"/>), so the card
/// changes at once, before the daemon's refresh reaches the list and the
/// pane through the usual notifications. Only an account with
/// <see cref="Capability.Transition"/> gets either call.
/// </summary>
/// <remarks>
/// Stale replies: a menu opens, closes and opens again while the site is
/// slow; only the newest load's reply reaches its completion
/// (<see cref="CancelLoad"/> drops the running one's when the menu closes).
/// A transition runs at most once per issue at a time
/// (<see cref="IsBusy"/>): the pill shows a spinner meanwhile
/// (<see cref="BusyChanged"/>) and the menu refuses a second choice. Create
/// it, and call it, on the UI thread; every event is raised there.
/// </remarks>
public sealed partial class IssueActionsController : IDisposable
{
    private readonly ControllerScope scope;
    private readonly RpcClient client;
    private readonly Func<AccountId, Account?> account;
    private readonly ILogger logger;
    private readonly HashSet<(AccountId Account, string Key)> running = [];

    // The load whose reply is still wanted; older ones are dropped.
    private int loadGeneration;

    /// <summary>The menus of a window over <paramref name="client"/>, on the calling (UI) thread.</summary>
    /// <param name="client">The daemon.</param>
    /// <param name="account">The account of an id as the window knows it; null (unknown) offers nothing.</param>
    /// <param name="logger">Receives the failures of the calls.</param>
    /// <param name="pending">Counts the background work; one of its own when null.</param>
    public IssueActionsController(
        RpcClient client, Func<AccountId, Account?> account, ILogger<IssueActionsController>? logger = null, PendingWork? pending = null)
    {
        ArgumentNullException.ThrowIfNull(client);
        ArgumentNullException.ThrowIfNull(account);
        this.client = client;
        this.account = account;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <summary>
    /// A transition started (Busy true) or ended either way, with the
    /// issue's key as the daemon knows it (Swift <c>onBusy</c>).
    /// </summary>
    public event EventHandler<(AccountId Account, string Key, bool Busy)>? BusyChanged;

    /// <summary>
    /// The issue <c>issue.transition</c> returned: the views showing it apply
    /// it to their cards (Swift <c>onIssueChanged</c>).
    /// </summary>
    public event EventHandler<(AccountId Account, IssueInfo Issue)>? IssueChanged;

    /// <summary>A sentence for the window's toast.</summary>
    public event EventHandler<string>? ToastRequested;

    /// <summary>
    /// issue_actions.go <c>subjectOf</c>: the subject of message
    /// <paramref name="s"/>, its issue; null for a message without one.
    /// </summary>
    public static Subject? SubjectOf(MessageSummary s)
    {
        ArgumentNullException.ThrowIfNull(s);
        return s.Issue is null || s.Id.Value is not { Length: > 0 } ? null : new Subject(s.AccountId, s.Id);
    }

    /// <summary>Whether the account offers the menu (<see cref="Jira.CanTransition"/>).</summary>
    public bool CanTransition(AccountId id) => account(id) is { } a && Jira.CanTransition(a);

    /// <summary>Whether a transition runs on the issue <paramref name="key"/> of <paramref name="accountId"/> right now.</summary>
    public bool IsBusy(AccountId accountId, string key) => running.Contains((accountId, key));

    /// <summary>
    /// Asks <c>issue.transitions</c> for the issue of <paramref name="subject"/>
    /// and gives <paramref name="completion"/> the items (or the error, for
    /// the menu's only item:
    /// <c>RpcErrorText.Text(Jira.LoadTransitionsAction(), error)</c>), unless
    /// a newer load started or <see cref="CancelLoad"/> was called meanwhile.
    /// Returns false, and asks nothing, when the account cannot change
    /// statuses.
    /// </summary>
    public bool LoadTransitions(Subject subject, Action<Outcome<Loaded>> completion)
    {
        ArgumentNullException.ThrowIfNull(completion);
        if (!CanTransition(subject.Account))
        {
            return false;
        }
        var generation = ++loadGeneration;
        var parameters = new IssueTransitionsParams { AccountId = subject.Account, MessageId = subject.Message };
        scope.Perform(client, API.IssueTransitions, parameters, outcome =>
        {
            if (generation != loadGeneration)
            {
                return;
            }
            if (outcome.TryGetValue(out var res, out var error))
            {
                completion(Outcome.Success(new Loaded(res.Issue, Jira.Transitions(res))));
                return;
            }
            LogFailed(logger, "issue.transitions", error!.Message);
            completion(Outcome.Failure<Loaded>(error));
        });
        return true;
    }

    /// <summary>Drops the reply of the load that runs, if any (the menu closed).</summary>
    public void CancelLoad() => loadGeneration++;

    /// <summary>
    /// Performs <paramref name="item"/> on the issue of
    /// <paramref name="subject"/> (<paramref name="issue"/> is what
    /// <c>issue.transitions</c> returned with it: its key names the spinner).
    /// Nothing happens for a disabled item, for an account without the
    /// capability, or while a transition already runs on the issue. On
    /// success the toast says the new status and <see cref="IssueChanged"/>
    /// carries the refreshed issue; on failure the toast says why. Returns
    /// whether the call was made.
    /// </summary>
    public bool Perform(Subject subject, JiraTransitionItem item, IssueInfo issue)
    {
        ArgumentNullException.ThrowIfNull(item);
        ArgumentNullException.ThrowIfNull(issue);
        if (!item.Enabled || item.Id.Length == 0 || !CanTransition(subject.Account))
        {
            return false;
        }
        var key = (subject.Account, issue.Key);
        if (!running.Add(key))
        {
            return false;
        }
        scope.Raise(BusyChanged, this, (subject.Account, issue.Key, true));
        var parameters = new IssueTransitionParams { AccountId = subject.Account, MessageId = subject.Message, TransitionId = item.Id };
        scope.PerformPastClose(client, API.IssueTransition, parameters, outcome =>
        {
            running.Remove(key);
            if (outcome.TryGetValue(out var res, out var error))
            {
                scope.Raise(IssueChanged, this, (subject.Account, res.Issue));
                scope.Raise(ToastRequested, this, Jira.StatusChanged(item, res.Issue));
            }
            else
            {
                LogFailed(logger, "issue.transition", error!.Message);
                scope.Raise(ToastRequested, this, Jira.TransitionFailed(error));
            }
            scope.Raise(BusyChanged, this, (subject.Account, issue.Key, false));
        });
        return true;
    }

    /// <summary>The window went away: late replies are dropped.</summary>
    public void Close() => scope.Close();

    /// <inheritdoc/>
    public void Dispose() => Close();

    [LoggerMessage(Level = LogLevel.Warning, Message = "{Method}: {Error}")]
    private static partial void LogFailed(ILogger logger, string method, string error);
}
