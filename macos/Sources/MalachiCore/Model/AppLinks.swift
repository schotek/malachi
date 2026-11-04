// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The website and its form for reporting a problem with the app: the
/// About panel's links and Report an Issue at the end of the status bar
/// (ui/main.go `websiteURL`, `reportIssueURL`).
public enum AppLinks {
    public static let website = URL(string: "https://malachimail.app")!
    public static let reportIssue = URL(string: "https://malachimail.app/#report")!
}
