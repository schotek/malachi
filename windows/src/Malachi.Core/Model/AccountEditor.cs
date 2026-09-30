// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/AccountsPage.swift
// (AccountEditor); GTK: ui/internal/window/accounts_page.go (editorKind).

namespace Malachi.Core.Model;

/// <summary>
/// What edits an account (<see cref="AccountsPage.EditorOf"/>): the mail
/// account wizard, or what a Jira account has for it
/// (<see cref="AccountsPage.JiraEditorFor"/>).
/// </summary>
public enum AccountEditor
{
    /// <summary>The mail account wizard in its edit mode.</summary>
    MailWizard,

    /// <summary>The Jira account's settings or its assistant (<see cref="JiraEditor"/>).</summary>
    Jira,
}
