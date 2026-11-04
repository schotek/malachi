// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraWizard.swift (Jira.Page); GTK:
// ui/internal/jira/wizard.go (Page).

namespace Malachi.Core.IssueTrackers;

/// <summary>jira.Page: a page of the Jira account assistant, in order.</summary>
public enum JiraWizardPage
{
    /// <summary>The site's address (account.detectSite).</summary>
    Site,

    /// <summary>The sign-in, checked by account.listSpaces.</summary>
    Credentials,

    /// <summary>The spaces and the offline window (account.add).</summary>
    Spaces,
}
