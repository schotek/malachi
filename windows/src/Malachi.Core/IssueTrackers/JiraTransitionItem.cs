// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraTransitions.swift
// (Jira.TransitionItem); GTK: ui/internal/jira/transitions.go
// (TransitionItem).

namespace Malachi.Core.IssueTrackers;

/// <summary>jira.TransitionItem: one entry of the Change Status menu.</summary>
public sealed record JiraTransitionItem
{
    /// <summary>What goes to issue.transition.</summary>
    public string Id { get; init; } = "";

    /// <summary>The transition's name, as the site's own status menu shows it.</summary>
    public string Title { get; init; } = "";

    /// <summary>The status it leads to; "" when the site did not say.</summary>
    public string Target { get; init; } = "";

    /// <summary>
    /// <see cref="Target"/> when it differs from <see cref="Title"/>
    /// ("Start Progress" → "In Progress"), "" otherwise.
    /// </summary>
    public string Subtitle { get; init; } = "";

    /// <summary>
    /// False for a transition that needs fields in Jira: the item is listed
    /// disabled, with <see cref="Hint"/> saying why.
    /// </summary>
    public bool Enabled { get; init; }

    /// <summary>Why the item is disabled; "" when enabled.</summary>
    public string Hint { get; init; } = "";
}
