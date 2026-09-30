// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the state ui/internal/window/issue_card.go's show and setBusy
// give the card (url, key, account, the menu button or the plain pill, the
// spinner); macOS: MessageView/IssueCardView.swift (show). What the view
// of an issue card draws, computed by ReaderController (and later the
// conversation view's controller) from IssueReading. No required members:
// the XAML compiler makes an empty one for the card's dependency property.

using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Presentation;

/// <summary>The issue card over the headers of a Jira message.</summary>
public sealed record IssueCardState
{
    /// <summary>What the card shows (<see cref="Jira.IssueCard"/>).</summary>
    public JiraCard Card { get; init; } = new();

    /// <summary>The account of the message on display.</summary>
    public AccountId Account { get; init; }

    /// <summary>
    /// The key opens <see cref="JiraCard.Url"/>: a link to an issue of the
    /// account's own site (<see cref="Jira.IsIssueUrl"/>); otherwise the key
    /// is plain text.
    /// </summary>
    public bool Openable { get; init; }

    /// <summary>
    /// The status pill is the Change Status menu: the account changes
    /// statuses (<see cref="Jira.CanTransition"/>) and the issue has one.
    /// </summary>
    public bool Menu { get; init; }

    /// <summary>A transition runs on the issue: the pill shows a spinner and takes no click.</summary>
    public bool Busy { get; init; }
}
