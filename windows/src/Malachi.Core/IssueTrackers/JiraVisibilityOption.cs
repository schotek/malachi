// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraCompose.swift
// (Jira.VisibilityOption); GTK: ui/internal/jira/compose.go
// (VisibilityOption).

using Malachi.Core.Api;

namespace Malachi.Core.IssueTrackers;

/// <summary>jira.VisibilityOption: one choice of who reads a comment.</summary>
/// <param name="Visibility">public or internal.</param>
/// <param name="Label">What the choice says.</param>
public sealed record JiraVisibilityOption(CommentVisibility Visibility, string Label);
