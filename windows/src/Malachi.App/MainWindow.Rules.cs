// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Integration+Rules.swift (wireRules:
// the message list's Create Rule from Message… and the sidebar's Apply
// Rules…); GTK: ui/internal/window/context_menus.go (CreateRule and
// ApplyRules in messageMenuActions and folderMenuActions), docs/plans/rules.md
// R14. The two context-menu items of the mail rules, over the application's
// AppRules: Create Rule from Message… opens the rule editor prefilled from
// the message of the one row clicked, Apply Rules… of a folder asks the
// question, shows Applying rules and then the result as a toast on this
// window.

namespace Malachi.App;

/// <summary>The main window's mail rules.</summary>
public sealed partial class MainWindow
{
    /// <summary>The Integration exists: the context menus offer the rules' items.</summary>
    internal void AttachRules()
    {
        var rules = state.Rules;
        ListPane.CreateRule = message => rules.CreateFromMessage(this, message);
        SidebarPane.RulesApply = account => rules.Controller.AppliesTo(account);
        SidebarPane.ApplyRules = folder => _ = rules.ApplyAsync(this, folder, Toasts);
    }
}
