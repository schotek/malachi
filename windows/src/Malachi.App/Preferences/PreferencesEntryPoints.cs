// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Integration.swift (wireHooks:
// openPreferences and addAccount; editAccount) and Integration+Jira.swift;
// GTK: ui/main.go (app.preferences, app.add-account, app.add-jira-account),
// ui/internal/window/sync.go (editAccount, signInAgain's edit wizard asking
// for the password) and jira_editors.go (editJiraAccount). The application's
// entry points to the Preferences, the account wizard and the Jira account
// assistant, registered in the shell's hooks: the primary menu's Preferences
// (Ctrl+,), Add Account… and Add Jira Account…, the No Accounts page's
// buttons, and the banners' and the status popover's Edit Account…
// (AppHooks.EditAccount). A Jira account is edited in its settings
// (AccountsPage.EditorOf), or in the assistant asking for a new token when
// the route names why (a sign-in banner's reason, JiraEditorFor).

using Malachi.App.Shell;
using Malachi.App.Wizard;
using Malachi.Core.Api;
using Malachi.Core.Model;
using Microsoft.UI.Xaml;
using Pages = Malachi.Core.Model.AccountsPage;

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
        hooks.AddJiraAccount = window => JiraWizardWindow.Show(state, window);
        hooks.EditAccount = (window, id, reason) =>
        {
            if (state.Integration?.Mailbox.Model.Account(id) is not { } account)
            {
                return;
            }
            if (Pages.EditorOf(account) == AccountEditor.Jira)
            {
                EditJiraAccount(state, window, account, reason);
                return;
            }
            AccountWizardWindow.Show(state, window, account, requestPassword: reason);
        };
    }

    /// <summary>
    /// jira_editors.go <c>editJiraAccount</c>: the assistant on its token page
    /// when the route names the reason (JiraEditorFor), the settings
    /// otherwise, whose Replace Token… opens the assistant over them.
    /// <paramref name="done"/> runs once either stored the account.
    /// </summary>
    public static void EditJiraAccount(
        AppState state, Window? owner, Account account, ErrorCode? reason, System.Action<AccountId, AccountConfig>? done = null)
    {
        if (Pages.JiraEditorFor(reason) == JiraEditor.Token)
        {
            JiraWizardWindow.Show(state, owner, account, reason, done);
            return;
        }
        JiraAccountWindow.Show(state, owner, account, done);
    }
}
