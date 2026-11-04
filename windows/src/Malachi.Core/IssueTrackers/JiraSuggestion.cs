// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraSettings.swift
// (Jira.Suggestion); GTK: ui/internal/jira/settings.go (Suggestion).

namespace Malachi.Core.IssueTrackers;

/// <summary>jira.Suggestion: an entry the settings page offers to add with one click.</summary>
/// <param name="Value">The entry.</param>
/// <param name="Label">The button's text.</param>
public sealed record JiraSuggestion(string Value, string Label);
