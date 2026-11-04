// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Model/AppLinks.swift; GTK: ui/main.go
// (websiteURL, reportIssueURL).
//
// The website and its form for reporting a problem with the app: the About
// dialog's links and Report an Issue at the end of the status line.

namespace Malachi.Core.Model;

/// <summary>The project's website and its report form.</summary>
public static class AppLinks
{
    /// <summary>The project's website (main.go SetWebsite).</summary>
    public const string Website = "https://malachimail.app";

    /// <summary>The form for reporting a problem (main.go SetIssueURL).</summary>
    public const string ReportIssue = "https://malachimail.app/#report";
}
