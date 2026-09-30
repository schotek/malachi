// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraWizard.swift (Jira.Step); GTK:
// ui/internal/jira/wizard.go (Step).

namespace Malachi.Core.IssueTrackers;

/// <summary>jira.Step: a call of the assistant that can fail.</summary>
public enum JiraWizardStep
{
    /// <summary>account.detectSite, from the site page.</summary>
    Detect,

    /// <summary>account.listSpaces, from the credentials page.</summary>
    Spaces,

    /// <summary>account.add, or account.update when editing.</summary>
    Save,
}
