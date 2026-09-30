// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraCompose.swift
// (Jira.CommentWindow); GTK: ui/internal/jira/compose.go (CommentWindow).

using System.Collections.Generic;
using Malachi.Core.Api;

namespace Malachi.Core.IssueTrackers;

/// <summary>jira.CommentWindow: how the compose window presents a comment draft.</summary>
public sealed record JiraCommentWindow
{
    /// <summary>The window's title.</summary>
    public string Title { get; init; } = "";

    /// <summary>The choices shown; none: no choice, the comment is public.</summary>
    public IReadOnlyList<JiraVisibilityOption> Visibilities { get; init; } = [];

    /// <summary>The chosen one.</summary>
    public CommentVisibility Visibility { get; init; } = CommentVisibility.Public;

    /// <summary>The toolbar controls kept, <see cref="Jira.CommentFormats"/> (a copy).</summary>
    public IReadOnlyList<JiraFormat> Formats { get; init; } = [];
}
