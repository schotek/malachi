// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/IntegrationsPaneViewController.swift
// (addTrailingGroups, openService, addJira, addEway, addCalDAV,
// addAIAccount, editAIAccount, rowUsage, refreshUsage); GTK:
// ui/internal/window/integrations_page.go (bindIntegrations,
// newServiceRow, addService, addCalDAVAccount), ai_editors.go
// (addAIAccount, editAIAccount, refreshUsage, usageLine) and the
// Integrations page's accountList of accounts_page.go. Core:
// MalachiCore/Model/Integrations.swift, ui/internal/integrations/integrations.go
// (here Malachi.Core.Model.Integrations).
//
// Preferences → Integrations: the accounts that are not mail accounts
// (Connected Services; their rows are the Accounts page's,
// AccountListSection over an AccountsPageController with Page
// Integrations, from its own account.list), the local AI integrations that
// are connected (IntegrationsPage.Local.cs), and the catalogue of what can
// be added (Available Services, Integrations.Catalog, through Offered: a
// local integration is in the catalogue only while it is not connected). A
// Jira row pauses and resumes its sync and edits its settings; a CalDAV row
// has no switch, and its Edit (Calendar Settings) shows the Calendar page;
// an eWay-CRM row pauses and resumes its saving and its Edit opens the
// eWay-CRM assistant in its edit mode; an AI provider account's row (kind
// ai) has the "API key" capsule, its provider and model with the tokens the
// daemon's assistant used through it today (assistant.usage {days: 0},
// asked again on notify.assistantUsageChanged and whenever the page comes
// up), and its Edit (and Sign In while the key is refused) opens its
// dialog (AiAccountWindow).
// A row moves only within this page; the order sent puts the mail
// accounts first (Integrations.FullOrder). A catalogue card's Add… opens
// what its entry names, told apart by the entry's Key (three entries share
// the kind ai): the Jira account assistant, the Add CalDAV Calendar window
// over this window's calendar source, the eWay-CRM account assistant, the
// window of a local integration (ClaudeIntegrationWindow of Claude Desktop
// or of Claude Code, ChatGptIntegrationWindow), or the dialog that adds an AI provider
// account of that entry's provider (AiAccountWindow; macOS §15.8). The page
// reloads once an account was added and names it. Both account pages also
// follow notify.accountsChanged, so an account added or removed elsewhere
// shows here too.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.Linq;
using Malachi.App.Resources;
using Malachi.App.Shell;
using Malachi.App.Wizard;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Boards;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Malachi.Core.Transport;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Integrations = Malachi.Core.Model.Integrations;
using SettingsCard = CommunityToolkit.WinUI.Controls.SettingsCard;

namespace Malachi.App.Preferences;

/// <summary>The Integrations page of the preferences.</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The window closes the page and its bridge controller (Close).")]
public sealed partial class IntegrationsPage : UserControl
{
    private readonly AppState state;
    private readonly Window window;
    private readonly IToasts toasts;
    private readonly AccountListSection list;

    // The catalogue's cards by their entry's key.
    private readonly Dictionary<string, SettingsCard> cardsByKey = [];

    // The daemon's notifications the page follows.
    private readonly List<IDisposable> subscriptions = [];

    // The newest assistant.usage asked per account: a late answer is dropped.
    private readonly Dictionary<AccountId, int> usageGen = [];
    private bool closed;

    /// <summary>The page of the preferences window <paramref name="window"/>.</summary>
    public IntegrationsPage(AppState state, Window window, IToasts toasts)
    {
        this.state = state;
        this.window = window;
        this.toasts = toasts;
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
            var card = ServiceCard(service, texts.Add);
            cardsByKey[service.Key] = card;
            ServiceCards.Children.Add(card);
        }
        // The usage lines of the AI provider accounts follow their rows.
        Controller.RowsChanged += (_, rows) => UsageForRows(rows);
        subscriptions.Add(state.Notifications.AddAssistantUsageChanged(n => RefreshUsage(n.AccountId)));
        InitializeLocal();
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

    /// <summary>
    /// The page came up: the local integrations are looked at again (Claude
    /// Code installed or signed in, the bridge registered elsewhere, ChatGPT
    /// connected) and so are the usage lines.
    /// </summary>
    public void Refresh()
    {
        if (closed)
        {
            return;
        }
        RefreshLocalAgain();
        foreach (var row in Controller.Rows)
        {
            if (row.Account.Config.ProtocolKind == AccountKind.Ai)
            {
                RefreshUsage(row.Id);
            }
        }
    }

    /// <summary>The window closed: the page's calls and notifications are dropped.</summary>
    public void Close()
    {
        closed = true;
        foreach (var s in subscriptions)
        {
            s.Dispose();
        }
        subscriptions.Clear();
        CloseLocal();
        list.Close();
    }

    /// <summary>
    /// The dialog of AI provider account <paramref name="account"/> over this
    /// window, on Replace Key… for <paramref name="replaceKey"/> (the
    /// application's EditAiAccount); the page reloads once it saved.
    /// </summary>
    public void EditAiAccount(Account account, bool replaceKey)
    {
        ArgumentNullException.ThrowIfNull(account);
        list.EditAiAccount(account, replaceKey);
    }

    /// <summary>
    /// ai_editors.go addAIAccount: the dialog that adds an account of
    /// <paramref name="provider"/> over this window (the catalogue's Add…,
    /// the application's AddAiAccount); the page reloads once it added the
    /// account and names it.
    /// </summary>
    public void AddAiAccount(AiProvider provider) =>
        AiAccountWindow.Show(state, window, provider, done: (_, config) => Controller.AccountAdded(AccountListSection.AiNamed(config)));

    // newServiceRow: the entry's icon, name with its capsule and
    // description, and its Add… button (its help text names the entry).
    private SettingsCard ServiceCard(Integrations.Service service, string label)
    {
        var (cardId, addId) = AutomationIds(service);
        var add = new Button { Content = label, Tag = service.Key };
        AutomationProperties.SetAutomationId(add, addId);
        AutomationProperties.SetHelpText(add, service.Name);
        add.Click += OnAddClick;
        var card = new SettingsCard
        {
            Header = TitleWithBadge(service.Name, service.Badge),
            Description = service.Description,
            HeaderIcon = Icons.Create(service.Icon),
            Content = add,
        };
        AutomationProperties.SetAutomationId(card, cardId);
        AutomationProperties.SetName(card, service.Name);
        return card;
    }

    /// <summary>
    /// A row's title with its capsule after it (widget.NewSourcePill: the
    /// neutral pill, "This computer" or "API key"); the title alone without one.
    /// </summary>
    internal static object TitleWithBadge(string title, string badge)
    {
        if (badge.Length == 0)
        {
            return title;
        }
        var resources = Application.Current.Resources;
        var panel = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 6 };
        panel.Children.Add(new TextBlock { Text = title, VerticalAlignment = VerticalAlignment.Center, TextTrimming = TextTrimming.CharacterEllipsis });
        var text = new TextBlock
        {
            Style = (Style)resources["IssuePillTextStyle"],
            Text = badge,
        };
        // The brush lives in the theme dictionaries of Pills.xaml; a lookup
        // from code that misses it keeps the pill style's own colour rather
        // than throwing.
        if (resources.TryGetValue("BulkPillForegroundBrush", out var brush) && brush is Microsoft.UI.Xaml.Media.Brush foreground)
        {
            text.Foreground = foreground;
        }
        panel.Children.Add(new Border
        {
            Style = (Style)resources["IssuePillBorderStyle"],
            Child = text,
        });
        return panel;
    }

    // The UI Automation ids of an entry's card and its Add… button; the
    // services keep the ids they had before the catalogue had the AI.
    private static (string Card, string Add) AutomationIds(Integrations.Service service)
    {
        var name = service.Local switch
        {
            Integrations.LocalKind.ClaudeDesktop or Integrations.LocalKind.ClaudeCode or Integrations.LocalKind.ChatGpt => LocalAutomationName(service.Local),
            _ => service.Kind?.Value switch
            {
                AccountKind.Jira => "Jira",
                AccountKind.CalDAV => "CalDAV",
                AccountKind.Eway => "Eway",
                AccountKind.Ai => service.Provider?.Value switch
                {
                    AiProvider.Anthropic => "Anthropic",
                    AiProvider.OpenAi => "OpenAi",
                    _ => "OpenAiCompatible",
                },
                _ => "Other",
            },
        };
        return (name + "Service", "Add" + name + "Integration");
    }

    // addService: what the entry the button belongs to adds.
    private void OnAddClick(object sender, RoutedEventArgs e)
    {
        if (closed || (sender as FrameworkElement)?.Tag is not string key
            || Integrations.Catalog().FirstOrDefault(s => s.Key == key) is not { } service)
        {
            return;
        }
        switch (service.Local)
        {
            case Integrations.LocalKind.ClaudeDesktop:
            case Integrations.LocalKind.ClaudeCode:
            case Integrations.LocalKind.ChatGpt:
                OpenLocal(service.Local);
                return;
            case Integrations.LocalKind.None:
            default:
                break;
        }
        switch (service.Kind?.Value)
        {
            case AccountKind.Jira:
                // jira_editors.go addJiraAccount: the page reloads once the
                // assistant added the account and says so.
                JiraWizardWindow.Show(state, window, done: (_, config) => Controller.AccountAdded(config));
                break;
            case AccountKind.CalDAV:
                // addCalDAVAccount: the Add CalDAV Calendar window; the page
                // reloads once it added the account.
                state.ShowAddCalDAVCalendar(window, _ => Controller.HandleAccountsChanged());
                break;
            case AccountKind.Eway:
                // macOS addEway: the daemon fills the address from the
                // eWay-CRM user, which may have none (docs/eway.md D2): the
                // toast names the account by its name, else the brand.
                EwayWizardWindow.Show(state, window, done: (_, config) =>
                    Controller.AccountAdded(config.Email.Length > 0
                        ? config
                        : config with { Email = config.Name.Length > 0 ? config.Name : Malachi.Core.Crm.CrmRules.Brand }));
                break;
            case AccountKind.Ai:
                AddAiAccount(service.Provider ?? AiProvider.OpenAiCompatible);
                break;
            default:
                break;
        }
    }

    // The usage lines (ai_editors.go)

    // A row of an AI provider account that has no line yet asks for one.
    private void UsageForRows(IReadOnlyList<AccountRow> rows)
    {
        foreach (var row in rows)
        {
            if (row.Account.Config.ProtocolKind == AccountKind.Ai && !usageGen.ContainsKey(row.Id))
            {
                RefreshUsage(row.Id);
            }
        }
    }

    // refreshUsage: assistant.usage for the tokens account id used today
    // (the daemon counts by local days); the row shows "<n> tokens today".
    // An account without a row, or a failed call, changes nothing.
    private async void RefreshUsage(AccountId id)
    {
        if (closed || !Controller.Rows.Any(r => r.Id == id && r.Account.Config.ProtocolKind == AccountKind.Ai))
        {
            return;
        }
        var gen = (usageGen.TryGetValue(id, out var g) ? g : 0) + 1;
        usageGen[id] = gen;
        AssistantUsageResult result;
        try
        {
            result = await state.Client.CallAsync(API.AssistantUsage, new AssistantUsageParams { AccountId = id, Days = 0 });
        }
        catch (Exception e) when (e is RpcException or RpcClientException or TimeoutException or OperationCanceledException)
        {
            return;
        }
        if (closed || !usageGen.TryGetValue(id, out var latest) || latest != gen)
        {
            return;
        }
        long tokens = 0;
        foreach (var day in result.Days)
        {
            tokens += day.InputTokens + day.OutputTokens + day.CacheReadInputTokens + day.CacheCreationInputTokens;
        }
        list.SetUsage(id, UsageLine(tokens));
    }

    /// <summary>usageLine: "<n> tokens today" with the digits grouped.</summary>
    internal static string UsageLine(long tokens) =>
        GettextFormat.Format(Assistant.ApiText().UsageToday, [Board.Text.TriageTokens(tokens, CultureInfo.CurrentCulture)]);
}
