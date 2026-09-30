// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Jira/JiraWizard.swift (Jira.ErrorClass);
// GTK: ui/internal/jira/wizard.go (ErrorClass).

namespace Malachi.Core.IssueTrackers;

/// <summary>jira.ErrorClass: the errors of the assistant's calls sorted by what the user can do about them.</summary>
public enum JiraErrorClass
{
    /// <summary>Anything else, also no reply (disconnected, timed out).</summary>
    Other,

    /// <summary>invalidArgument: the daemon refused the input.</summary>
    Invalid,

    /// <summary>serverError: the site answered, but not as Jira does.</summary>
    Server,

    /// <summary>offline, networkError, serverTimeout.</summary>
    Network,

    /// <summary>tlsError.</summary>
    Tls,

    /// <summary>authFailed: the site refused the token.</summary>
    AuthFailed,

    /// <summary>authRequired: no token typed and none stored.</summary>
    AuthRequired,

    /// <summary>conflict: an account for this site and address exists.</summary>
    Conflict,
}
