// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraTransitions.swift; GTK:
// ui/internal/jira/transitions.go (CanTransition, Transitions,
// ChangeStatusLabel, NeedsInputHint, TransitionsLoading, NoTransitions,
// LoadTransitionsAction, TransitionAction, StatusChanged,
// TransitionFailed).
//
// The status of an issue is changed from the Change Status menu (the
// status pill of the issue card, More Actions) on an account with the
// transition capability: the menu lists what issue.transitions returned,
// in the site's order, the transitions that need fields in Jira disabled
// with a hint, and choosing one calls issue.transition. The daemon
// refreshes the issue right after, so the event row and the new status
// arrive through the usual notifications; the client applies the result's
// issue to the card at once all the same. strings.EqualFold is
// CodePoints.EqualFold; TransitionFailed(Exception) is Swift's
// transitionFailed(_ error:).

using System;
using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Text;
using Malachi.Core.Transport;

namespace Malachi.Core.IssueTrackers;

public static partial class Jira
{
    /// <summary>jira.CanTransition: an account whose issues offer the Change Status menu.</summary>
    public static bool CanTransition(Account a)
    {
        ArgumentNullException.ThrowIfNull(a);
        return a.Can(Capability.Transition);
    }

    /// <summary>
    /// jira.Transitions: the items of the Change Status menu from the result
    /// of issue.transitions: the site's order, every string cleaned, a
    /// transition without an id skipped, one without a name shown by its
    /// target, at most <see cref="API.Limits.MaxIssueTransitions"/>. A
    /// transition that leads to the status the issue already has is left
    /// out: a workflow with global transitions offers one for every status,
    /// the current one included.
    /// </summary>
    public static IReadOnlyList<JiraTransitionItem> Transitions(IssueTransitionsResult res)
    {
        ArgumentNullException.ThrowIfNull(res);
        var output = new List<JiraTransitionItem>(res.Transitions.Count);
        var current = Clean(res.Issue.Status);
        foreach (var t in res.Transitions)
        {
            var id = t.Id.Trim();
            if (id.Length == 0)
            {
                continue;
            }
            var title = Clean(t.Name);
            var target = Clean(t.To);
            if (title.Length == 0)
            {
                title = target;
            }
            if (title.Length == 0)
            {
                continue;
            }
            if (current.Length > 0 && target.Length > 0 && CodePoints.EqualFold(target, current))
            {
                continue;
            }
            var needsInput = t.NeedsInput == true;
            output.Add(new JiraTransitionItem
            {
                Id = id,
                Title = title,
                Target = target,
                Subtitle = target.Length > 0 && !CodePoints.EqualFold(target, title) ? target : "",
                Enabled = !needsInput,
                Hint = needsInput ? NeedsInputHint() : "",
            });
            if (output.Count == API.Limits.MaxIssueTransitions)
            {
                break;
            }
        }
        return output;
    }

    /// <summary>jira.ChangeStatusLabel: the title of the menu.</summary>
    public static string ChangeStatusLabel() =>
        // TRANSLATORS: menu of the status changes a Jira issue allows.
        L10n.T("Change Status");

    /// <summary>jira.NeedsInputHint: why a transition is listed disabled.</summary>
    public static string NeedsInputHint() =>
        // TRANSLATORS: a status change of a Jira issue asks for more on the site and cannot be made here.
        L10n.T("Needs fields in Jira");

    /// <summary>jira.TransitionsLoading: the menu's only item while issue.transitions runs.</summary>
    public static string TransitionsLoading() => L10n.T("Loading…");

    /// <summary>jira.NoTransitions: the menu's only item when the site offers no status change on the issue.</summary>
    public static string NoTransitions() =>
        // TRANSLATORS: the only item of the Change Status menu of a Jira issue whose status cannot be changed.
        L10n.T("No status change is available");

    /// <summary>
    /// jira.LoadTransitionsAction: what failed when issue.transitions did,
    /// in the progressive form RpcErrorText takes ("Loading the status
    /// changes failed: …"); the menu's only item then.
    /// </summary>
    public static string LoadTransitionsAction() =>
        // TRANSLATORS: progressive; %s of "%s failed: …" when the status changes of a Jira issue could not be listed.
        L10n.T("Loading the status changes");

    /// <summary>
    /// jira.TransitionAction: what failed when issue.transition did, in the
    /// progressive form RpcErrorText takes ("Changing the status timed out").
    /// </summary>
    public static string TransitionAction() =>
        // TRANSLATORS: progressive; %s of "%s failed: …" when the status of a Jira issue could not be changed.
        L10n.T("Changing the status");

    /// <summary>
    /// jira.StatusChanged: the toast after issue.transition: the status the
    /// chosen transition leads to (the change was made even when the
    /// daemon's refresh did not finish in time, and <paramref name="issue"/>
    /// then still shows the old status), else the refreshed issue's status,
    /// else the transition's name.
    /// </summary>
    public static string StatusChanged(JiraTransitionItem chosen, IssueInfo issue)
    {
        ArgumentNullException.ThrowIfNull(chosen);
        ArgumentNullException.ThrowIfNull(issue);
        var status = chosen.Target;
        if (status.Length == 0)
        {
            status = Clean(issue.Status);
        }
        if (status.Length == 0)
        {
            status = chosen.Title;
        }
        // TRANSLATORS: toast; %s is the new status of a Jira issue, such as "In Progress".
        return L10n.T("Status changed to %s", status);
    }

    /// <summary>
    /// jira.TransitionFailed: the toast of a failed issue.transition: the
    /// site's own reason when it refused the change (a serverError whose
    /// message the daemon took from the site; cleaned again here), else
    /// <paramref name="fallback"/>, the client's usual sentence for the error
    /// (RpcErrorText with <see cref="TransitionAction"/>).
    /// </summary>
    public static string TransitionFailed(ErrorCode code, string message, string fallback)
    {
        if (code == ErrorCode.ServerError && Clean(message) is { Length: > 0 } reason)
        {
            // TRANSLATORS: toast; %s is the reason the Jira site gave, in its own words.
            return L10n.T("The status could not be changed: %s", reason);
        }
        return fallback;
    }

    /// <summary>
    /// <see cref="TransitionFailed(ErrorCode, string, string)"/> for any
    /// failure of the call: the daemon's code and message when it answered
    /// with an error, the usual sentence otherwise.
    /// </summary>
    public static string TransitionFailed(Exception error)
    {
        var fallback = RpcErrorText.Text(TransitionAction(), error);
        return error is RpcException e ? TransitionFailed(e.Code, e.Error.Message, fallback) : fallback;
    }
}
