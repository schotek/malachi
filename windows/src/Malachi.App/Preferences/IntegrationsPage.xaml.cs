// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/IntegrationsPaneViewController.swift;
// GTK: ui/internal/window/integrations_page.go (bindIntegrations,
// newServiceRow, addService, addCalDAVAccount) and the Integrations
// page's accountList of accounts_page.go. Core:
// MalachiCore/Model/Integrations.swift, ui/internal/integrations/integrations.go
// (here Malachi.Core.Model.Integrations).
//
// Preferences → Integrations: the accounts that are not mail accounts
// (Connected Services; their rows are the Accounts page's,
// AccountListSection over an AccountsPageController with Page
// Integrations, from its own account.list) and the catalogue of services
// that can be added (Available Services, Integrations.Catalog). A Jira row
// pauses and resumes its sync and edits its settings; a CalDAV row has no
// switch, and its Edit (Calendar Settings) shows the Calendar page, which
// keeps the CalDAV account's server, password, certificate and calendars;
// an eWay-CRM row pauses and resumes its saving and its Edit opens the
// eWay-CRM assistant in its edit mode (EwayWizardWindow).
// A row moves only within this page; the order sent puts the mail
// accounts first (Integrations.FullOrder). A catalogue card's Add… opens
// the service's assistant over the Preferences: the Jira account
// assistant (then the page reloads and names the account, as GTK's
// addJiraAccount), or the Add CalDAV Calendar window over this window's
// calendar source (AppState.ShowAddCalDAVCalendar, which asks the
// Calendar page's snapshot anew; the page reloads, as GTK's
// addCalDAVAccount, without a toast), or the eWay-CRM account assistant
// (EwayWizardWindow; the page reloads and names the account, as macOS's
// addEway). Both pages also follow
// notify.accountsChanged, so an account added or removed on the Calendar
// page or in the main window shows here too.

using Malachi.App.Resources;
using Malachi.App.Shell;
using Malachi.App.Wizard;
using Malachi.Core.Api;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Integrations = Malachi.Core.Model.Integrations;

namespace Malachi.App.Preferences;

/// <summary>The Integrations page of the preferences.</summary>
public sealed partial class IntegrationsPage : UserControl
{
    private readonly AppState state;
    private readonly Window window;
    private readonly AccountListSection list;

    /// <summary>The page of the preferences window <paramref name="window"/>.</summary>
    public IntegrationsPage(AppState state, Window window, IToasts toasts)
    {
        this.state = state;
        this.window = window;
        var texts = Integrations.Texts();
        list = new AccountListSection(
            state,
            window,
            toasts,
            Integrations.Page.Integrations,
            listId: "IntegrationsList",
            listName: texts.ConnectedTitle,
            emptyTitle: texts.EmptyTitle,
            emptySubtitle: texts.EmptySubtitle,
            emptyId: "IntegrationsEmpty");
        Controller = list.Controller;
        InitializeComponent();
        ListSlot.Child = list;
        PageDescription.Text = texts.Description;
        ConnectedHeader.Text = texts.ConnectedTitle;
        AvailableHeader.Text = texts.AvailableTitle;
        foreach (var service in Integrations.Catalog())
        {
            if (service.Kind == AccountKind.Jira)
            {
                ShowService(JiraService, AddJiraButton, service, texts.Add);
            }
            else if (service.Kind == AccountKind.CalDAV)
            {
                ShowService(CalDAVService, AddCalDAVButton, service, texts.Add);
            }
            else if (service.Kind == AccountKind.Eway)
            {
                ShowService(EwayService, AddEwayButton, service, texts.Add);
            }
        }
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

    // newServiceRow: the service's icon, name and description, and its
    // Add… button (its help text names the service, which the card's
    // header shows beside it).
    private static void ShowService(CommunityToolkit.WinUI.Controls.SettingsCard card, Button add, Integrations.Service service, string label)
    {
        card.Header = service.Name;
        card.Description = service.Description;
        card.HeaderIcon = Icons.Create(service.Icon);
        AutomationProperties.SetName(card, service.Name);
        add.Content = label;
        AutomationProperties.SetHelpText(add, service.Name);
    }

    // addService(jira) = jira_editors.go addJiraAccount: the Jira
    // assistant; the page reloads once it added the account and says so.
    private void OnAddJiraClick(object sender, RoutedEventArgs e) =>
        JiraWizardWindow.Show(state, window, done: (_, config) => Controller.AccountAdded(config));

    // addService(caldav) = addCalDAVAccount: the Add CalDAV Calendar window;
    // the page reloads once it added the account.
    private void OnAddCalDAVClick(object sender, RoutedEventArgs e) =>
        state.ShowAddCalDAVCalendar(window, _ => Controller.HandleAccountsChanged());

    // addService(eway) = macOS addEway: the eWay-CRM assistant; the page
    // reloads once it added the account and names it. The daemon fills the
    // address from the eWay-CRM user, which may have none (docs/eway.md D2):
    // the toast names the account by its name, else the brand, until the
    // list has it.
    private void OnAddEwayClick(object sender, RoutedEventArgs e) =>
        EwayWizardWindow.Show(state, window, done: (_, config) =>
            Controller.AccountAdded(config.Email.Length > 0
                ? config
                : config with { Email = config.Name.Length > 0 ? config.Name : Malachi.Core.Crm.CrmRules.Brand }));
}
