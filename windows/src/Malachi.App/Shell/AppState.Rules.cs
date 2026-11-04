// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/AppState.swift (rules); GTK:
// ui/main.go (the mail rules). The application's mail rules
// (Rules/AppRules.cs), made after the address books.

using Malachi.App.MailRules;

namespace Malachi.App.Shell;

/// <summary>The mail rules' application-wide state.</summary>
public sealed partial class AppState
{
    /// <summary>The mail rules (the Rules page, Create Rule from Message…, Apply Rules…).</summary>
    public AppRules Rules { get; private set; } = null!;

    // After the address books: the controller over the daemon and the routing of notify.rulesChanged.
    private void InitializeRules() => Rules = new AppRules(this);

    // Quit's Release: the routing goes.
    private void CloseRules() => Rules?.Dispose();
}
