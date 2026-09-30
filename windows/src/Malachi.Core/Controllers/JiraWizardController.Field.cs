// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/JiraWizardController.swift
// (Field); GTK: ui/internal/accountwizard/jira_flow.go (jiraField).

namespace Malachi.Core.Controllers;

/// <content>The fields of the Jira assistant.</content>
public sealed partial class JiraWizardController
{
    /// <summary>A field the UI can flag and focus.</summary>
    public enum Field
    {
        /// <summary>The site's address.</summary>
        Site,

        /// <summary>The Atlassian account's e-mail address (Jira Cloud).</summary>
        Login,

        /// <summary>The API token or personal access token.</summary>
        Token,

        /// <summary>The account's address on a Data Center site that hides it.</summary>
        Email,
    }
}
