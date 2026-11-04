// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/IssueActionsController.swift
// (Loaded); GTK: ui/internal/window/issue_actions.go (loadedTransitions).

using System.Collections.Generic;
using Malachi.Core.Api;
using Malachi.Core.IssueTrackers;

namespace Malachi.Core.Controllers;

/// <content>What the menu shows.</content>
public sealed partial class IssueActionsController
{
    /// <summary>What <c>issue.transitions</c> answered, as the menu shows it.</summary>
    /// <param name="Issue">The issue as the daemon last synchronised it.</param>
    /// <param name="Items">The menu's items (<see cref="Jira.Transitions"/>).</param>
    public sealed record Loaded(IssueInfo Issue, IReadOnlyList<JiraTransitionItem> Items);
}
