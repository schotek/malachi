// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/AccountsPage.swift (JiraEditor);
// GTK: ui/internal/window/accounts_page.go (jiraEditorKind).

namespace Malachi.Core.Model;

/// <summary>
/// What edits a Jira account (<see cref="AccountsPage.JiraEditorFor"/>): its
/// settings (JiraAccountController), or the account assistant in its edit
/// mode, which asks for a new token (JiraWizardController).
/// </summary>
public enum JiraEditor
{
    /// <summary>The account's settings.</summary>
    Settings,

    /// <summary>The assistant asking for a new API token.</summary>
    Token,
}
