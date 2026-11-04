// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/AccountsPaneViewController.swift
// (addClicked; the rows are AccountListSection's); GTK:
// ui/internal/window/accounts_page.go (bindAccounts: the add button and
// the mail accounts' accountList). The page lists the mail accounts only
// (Integrations.ShowsInAccounts: kind imap or graph); a Jira or CalDAV
// account is on the Integrations page (IntegrationsPage), and its "+" is a
// plain button that opens the mail account wizard: Add Jira Account… and
// Add CalDAV Calendar… are the Integrations page's catalogue (as well as
// the main window's … menu and the No Accounts page's buttons). The
// page's logic is Core's AccountsPageController with Page Accounts; the
// list, its keys and its drags are AccountListSection's.

using Malachi.App.Shell;
using Malachi.App.Wizard;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Integrations = Malachi.Core.Model.Integrations;

namespace Malachi.App.Preferences;

/// <summary>The Accounts page of the preferences.</summary>
public sealed partial class AccountsPage : UserControl
{
    private readonly AppState state;
    private readonly Window window;
    private readonly AccountListSection list;

    /// <summary>The page of the preferences window <paramref name="window"/>.</summary>
    public AccountsPage(AppState state, Window window, IToasts toasts)
    {
        this.state = state;
        this.window = window;
        list = new AccountListSection(
            state,
            window,
            toasts,
            Integrations.Page.Accounts,
            listId: "AccountList",
            listName: L10n.T("Mail Accounts"),
            emptyTitle: L10n.T("No accounts yet"),
            emptySubtitle: L10n.T("Use the + button to add one"),
            emptyId: "AccountsEmpty");
        Controller = list.Controller;
        InitializeComponent();
        ListSlot.Child = list;
    }

    /// <summary>The page's logic.</summary>
    public AccountsPageController Controller { get; }

    /// <summary>
    /// Whether Ctrl+Up and Ctrl+Down move a row now: a row is selected and
    /// the list has the keyboard focus (GTK's shortcuts are the row's).
    /// </summary>
    public bool CanMove => list.CanMove;

    /// <summary>Moves the selected row by <paramref name="delta"/> (Ctrl+Up, Ctrl+Down).</summary>
    public void MoveSelected(int delta) => list.MoveSelected(delta);

    /// <summary>The window closed: the page's calls and notifications are dropped.</summary>
    public void Close() => list.Close();

    // accounts_page.go bindAccounts' prefs.add-account: the mail account
    // wizard; the page reloads once it added the account and says so.
    private void OnAddClick(object sender, RoutedEventArgs e) =>
        AccountWizardWindow.Show(state, window, done: (_, config) => Controller.AccountAdded(config));
}
