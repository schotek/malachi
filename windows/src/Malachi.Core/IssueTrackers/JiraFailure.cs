// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraWizard.swift (Jira.Failure);
// GTK: ui/internal/jira/wizard.go (Failure).

namespace Malachi.Core.IssueTrackers;

/// <summary>
/// jira.Failure: what the assistant does after a failed step: it shows
/// <paramref name="Page"/> with <paramref name="Banner"/>. An empty banner
/// means the client's general sentence for the error (RpcErrorText) with
/// <paramref name="What"/>, the step's action.
/// </summary>
/// <param name="Page">The page shown.</param>
/// <param name="Banner">The banner; "" for the general sentence.</param>
/// <param name="What">The step's action, for the general sentence.</param>
public sealed record JiraFailure(JiraWizardPage Page, string Banner, string What);
