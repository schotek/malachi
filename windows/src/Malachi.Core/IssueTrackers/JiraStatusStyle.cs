// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraView.swift (Jira.StatusStyle);
// GTK: ui/internal/jira/jira.go (StatusStyle).

namespace Malachi.Core.IssueTrackers;

/// <summary>
/// jira.StatusStyle: how a status pill is coloured, by the status's
/// category. Go's values are the GTK style classes (in parentheses below);
/// the views map them to their colours (grey, blue, green; Plain has none).
/// </summary>
public enum JiraStatusStyle
{
    /// <summary>No colour: an unknown or empty category ("").</summary>
    Plain,

    /// <summary>To do: grey ("status-todo").</summary>
    Todo,

    /// <summary>In progress: blue ("status-in-progress").</summary>
    InProgress,

    /// <summary>Done: green ("status-done").</summary>
    Done,
}
