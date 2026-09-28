// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Integration.swift (wireHooks:
// openPreferences and addAccount; editAccount); GTK: ui/main.go
// (app.preferences, app.add-account) and ui/internal/window/sync.go
// (editAccount, signInAgain's edit wizard asking for the password). The
// application's entry points to the Preferences and the account wizard,
// registered in the shell's hooks: the primary menu's Preferences (Ctrl+,)
// and Add Account…, the No Accounts page's button, and the banners' and
// the status popover's Edit Account… (AppHooks.EditAccount).

using Malachi.App.Shell;
using Malachi.App.Wizard;

namespace Malachi.App.Preferences;

/// <summary>Registers the Preferences and the account wizard with the shell.</summary>
internal static class PreferencesEntryPoints
{
    /// <summary>Sets the hooks of <paramref name="state"/> (once the Integration exists).</summary>
    public static void Install(AppState state)
    {
        var hooks = state.Hooks;
        hooks.OpenPreferences = () => PreferencesWindow.Show(state);
        // The sidebar learns of the new account through notify.accountsChanged.
        hooks.AddAccount = window => AccountWizardWindow.Show(state, window);
        hooks.EditAccount = (window, id, reason) =>
        {
            if (state.Integration?.Mailbox.Model.Account(id) is { } account)
            {
                AccountWizardWindow.Show(state, window, account, requestPassword: reason);
            }
        };
    }
}
