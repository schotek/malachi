// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/IssueActionsController.swift
// (Subject); GTK: ui/internal/window/issue_actions.go (issueSubject).

using Malachi.Core.Api;

namespace Malachi.Core.Controllers;

/// <content>What the Change Status menu acts on.</content>
public sealed partial class IssueActionsController
{
    /// <summary>
    /// The issue of a message (any message of it; the message's thread is
    /// the issue).
    /// </summary>
    /// <param name="Account">The Jira account.</param>
    /// <param name="Message">A message of the issue.</param>
    public readonly record struct Subject(AccountId Account, MessageId Message);
}
