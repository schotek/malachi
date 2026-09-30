// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/JiraAccount/JiraAccountWindowController.swift
// (present, replaceToken, dismiss) and JiraAccountViewController.swift
// (refresh, showBusy, showBanner, save); GTK: ui/internal/jiraaccount/dialog.go
// (New, Present, TokenReplaced, build, wire, dismiss, onSave, refresh,
// rebuildSpaces, showBusy, showBanner). The texts, the rules and the calls
// live in Core's JiraAccountController; this shows what it holds (Refresh,
// on every change) and feeds back what the user did. A mail account is
// edited in the account wizard; a Jira account has no servers, so the
// Preferences and the main window open this for it instead
// (AccountsPage.EditorOf). Replace Token… opens the Jira account assistant
// in its edit mode over this window, which stays open with what was
// edited, and lists the spaces again once the new token is stored.
//
// Windows (docs/windows-port.md §11.3): an owned modal window (ModalDialog)
// of the Adw.Dialog's 600×680; its close button, Escape and Ctrl+W cancel
// it, as Cancel does; late replies are dropped from then on
// (JiraAccountController.Close).

using System;
using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Linq;
using Malachi.App.Shell;
using Malachi.App.Wizard;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using VirtualKey = Windows.System.VirtualKey;

namespace Malachi.App.Preferences;

/// <summary>The settings of a Jira account.</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The page's controller is closed with the window (Closed).")]
public sealed partial class JiraAccountWindow : Window
{
    /// <summary>jira_account.blp content-width.</summary>
    public const int ContentWidth = 600;

    /// <summary>jira_account.blp content-height.</summary>
    public const int ContentHeight = 680;

    private readonly AppState state;
    private readonly JiraAccountController controller;
    private readonly Action<AccountId, AccountConfig>? done;
    private readonly ILogger logger;
    private readonly ModalDialog modal;
    private readonly ObservableCollection<JiraSpaceItem> spaceItems = [];
    private readonly List<(VirtualFolder Folder, ToggleSwitch Switch)> folderRows = [];
    private readonly JiraStatusPicker statuses;

    // The lists: the senders first, then the three of the bots.
    private readonly List<JiraListEditor> editors = [];

    // The widgets being set from the controller: their events are not the user's.
    private bool applying;

    private JiraAccountWindow(AppState state, Account account, Action<AccountId, AccountConfig>? done)
    {
        this.state = state;
        this.done = done;
        logger = state.Logs.CreateLogger<JiraAccountWindow>();
        InitializeComponent();
        controller = new JiraAccountController(state.Client, account, state.Logs.CreateLogger<JiraAccountController>());
        Title = controller.Title;
        ExtendsContentIntoTitleBar = true;
        SetTitleBar(Header);
        Header.Title = controller.Title;
        var icon = System.IO.Path.Combine(AppContext.BaseDirectory, "Assets", "Malachi.ico");
        if (System.IO.File.Exists(icon))
        {
            AppWindow.SetIcon(icon);
        }
        Tracked = state.Windows.Track(this, WindowKind.Wizard, Root, ToastsHost);
        modal = new ModalDialog(this);
        statuses = new JiraStatusPicker(controller.Texts.ClosedStatuses, controller.Texts.ClosedStatusesSubtitle);
        Build();
        Wire();
        Refresh();
        Root.Loaded += (_, _) => NameBox.Focus(FocusState.Programmatic);
        Closed += (_, _) =>
        {
            controller.Close();
            if (modal.ReturnToOwner() is { } activated)
            {
                LogReturned(logger, activated);
            }
        };
    }

    /// <summary>The window as the shell tracks it.</summary>
    public TrackedWindow Tracked { get; }

    /// <summary>
    /// Opens the settings of <paramref name="account"/> over
    /// <paramref name="owner"/> (the main window when null or hidden, shown
    /// first) and asks the daemon for its spaces and statuses.
    /// <paramref name="done"/> runs after account.update succeeded, before
    /// the window closes (a page that had nothing to save closes without it),
    /// and after the assistant stored a new token, over the window that stays
    /// (jira_editors.go <c>editJiraAccount</c>). Null when there is no window
    /// to open it over.
    /// </summary>
    public static JiraAccountWindow? Show(AppState state, Window? owner, Account account, Action<AccountId, AccountConfig>? done = null)
    {
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(account);
        if (owner is null || !owner.AppWindow.IsVisible)
        {
            state.ShowMainWindow();
            owner = state.MainWindow;
        }
        if (owner is null || state.IsStopping)
        {
            return null;
        }
        var w = new JiraAccountWindow(state, account, done);
        w.modal.Present(owner, ContentWidth, ContentHeight);
        w.controller.Start();
        LogPresented(w.logger);
        return w;
    }

    // build: the texts and the rows that depend on the account; nothing is
    // wired yet, so setting the widgets reports nothing.
    private void Build()
    {
        var t = controller.Texts;
        SiteTitle.Text = t.SiteTitle;
        AddressRow.Header = t.SiteAddress;
        NameRow.Header = t.AccountName;
        AutomationProperties.SetName(NameBox, t.AccountName);
        // Once: from here on the field is where the name changes.
        NameBox.Text = controller.Form.Name;
        UserRow.Header = t.SignedInAs;
        TokenButton.Content = t.ReplaceToken;
        SaveButton.Content = JiraAccountController.SaveLabel;
        Localization.MnemonicLabel.Apply(SaveButton, WizardRows.WithMnemonic(JiraAccountController.SaveLabel, L10n.T("_Save")));

        SpacesTitle.Text = t.SpacesTitle;
        SpacesDescription.Text = t.SpacesDescription;
        SpacesDescription.Visibility = t.SpacesDescription.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        NoSpaces.Text = t.NoSpaces;
        SpacesList.ItemsSource = spaceItems;
        AutomationProperties.SetName(SpacesList, t.SpacesTitle);

        SyncTitle.Text = t.SyncTitle;
        OfflineRow.Header = t.KeepOffline;
        OfflineRow.Description = t.KeepOfflineSubtitle;
        AutomationProperties.SetName(OfflineBox, t.KeepOffline);
        foreach (var label in JiraAccountController.OfflineLabels)
        {
            OfflineBox.Items.Add(label);
        }
        OnlyMineRow.Header = t.OnlyMine;
        OnlyMineRow.Description = t.OnlyMineSubtitle;
        AutomationProperties.SetName(OnlyMineSwitch, t.OnlyMine);
        ShowEventsRow.Header = t.ShowEvents;
        AutomationProperties.SetName(ShowEventsSwitch, t.ShowEvents);

        FoldersTitle.Text = t.FoldersTitle;
        foreach (var v in Jira.VirtualFolders)
        {
            var title = Jira.VirtualFolderTitle(v);
            var toggle = new ToggleSwitch();
            AutomationProperties.SetName(toggle, title);
            var folder = v;
            toggle.Toggled += (_, _) =>
            {
                if (!applying)
                {
                    controller.SetFolder(folder, toggle.IsOn);
                }
            };
            FoldersGroup.Children.Add(new CommunityToolkit.WinUI.Controls.SettingsCard { Header = title, Content = toggle });
            folderRows.Add((v, toggle));
        }
        FoldersGroup.Children.Add(statuses);

        NotificationTitle.Text = t.NotificationTitle;
        ModeRow.Header = t.NotificationMode;
        AutomationProperties.SetName(ModeBox, t.NotificationMode);
        foreach (var label in JiraAccountController.NotificationLabels)
        {
            ModeBox.Items.Add(label);
        }
        var senders = new JiraListEditor(JiraListKind.Senders, t.Senders, t.SendersSubtitle, t.Add, t.Remove);
        senders.SetPlaceholder(controller.SendersPlaceholder);
        NotificationGroup.Children.Add(senders);

        BotsTitle.Text = t.BotsTitle;
        editors.Add(senders);
        editors.Add(new JiraListEditor(JiraListKind.BotNames, t.BotNames, t.BotNamesSubtitle, t.Add, t.Remove));
        editors.Add(new JiraListEditor(JiraListKind.MetadataFilters, t.HiddenLines, t.HiddenLinesSubtitle, t.Add, t.Remove));
        editors.Add(new JiraListEditor(JiraListKind.AuthorPrefixes, t.NamePrefixes, t.NamePrefixesSubtitle, t.Add, t.Remove));
        foreach (var e in editors.Skip(1))
        {
            BotsGroup.Children.Add(e);
        }
    }

    // wire: the widgets to the controller and the controller to the widgets.
    private void Wire()
    {
        statuses.Toggled = (choice, on) =>
        {
            if (!applying)
            {
                controller.SetStatus(choice, on);
            }
        };
        foreach (var e in editors)
        {
            var kind = e.Kind;
            e.AddRequested = text => controller.AddEntry(kind, text);
            e.RemoveRequested = i => controller.RemoveEntry(kind, i);
            e.SuggestionChosen = value => controller.AddSuggestion(kind, value);
            e.Typed = () => controller.EntryTyped(kind);
        }
        controller.Changed += (_, _) => Refresh();
        controller.BusyChanged += (_, text) => ShowBusy(text);
        controller.BannerChanged += (_, text) => ShowBanner(text);
        controller.CloseRequested += (_, _) => Close();
        controller.Done += (_, saved) =>
        {
            try
            {
                done?.Invoke(saved.Id, saved.Config);
            }
            finally
            {
                Close();
            }
        };
        controller.ReplaceTokenRequested += (_, account) => ReplaceToken(account);
    }

    // replaceToken: the account assistant for a new token, over this
    // window. It stores the account as it is with the token it was given;
    // what the page has edited stays in the page.
    private void ReplaceToken(Account account) =>
        JiraWizardWindow.Show(state, this, account, done: (id, cfg) =>
        {
            controller.TokenReplaced();
            done?.Invoke(id, cfg);
        });

    // refresh: what the controller holds now.
    private void Refresh()
    {
        var c = controller;
        var was = applying;
        applying = true;
        try
        {
            var editable = !c.Saving;
            var site = c.Site;
            AddressText.Text = site.Address;
            DeploymentLabel.Text = site.Deployment;
            UserLabel.Text = site.User;
            ToolTipService.SetToolTip(UserLabel, site.User.Length > 0 ? site.User : null);
            UserDetail.Text = site.UserDetail;
            UserDetail.Visibility = site.UserDetail.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
            TokenRow.Header = site.TokenLabel;
            foreach (var row in new Control[] { AddressRow, NameRow, UserRow, TokenRow })
            {
                row.IsEnabled = editable;
            }

            ShowSpaces(c.SpaceRows, c.SelectedSpaces, editable);
            var problem = c.SpaceRows.Count > 0 ? c.SpacesProblem : "";
            SpacesProblem.Text = problem;
            SpacesProblem.Visibility = problem.Length > 0 ? Visibility.Visible : Visibility.Collapsed;

            OfflineBox.SelectedIndex = c.OfflineIndex;
            OnlyMineSwitch.IsOn = c.Form.OnlyMine;
            ShowEventsSwitch.IsOn = c.Form.ShowEvents;
            OfflineRow.IsEnabled = editable;
            OnlyMineRow.IsEnabled = editable;
            ShowEventsRow.IsEnabled = editable;

            foreach (var (folder, toggle) in folderRows)
            {
                toggle.IsOn = c.FolderShown(folder);
                toggle.IsEnabled = editable;
            }
            statuses.Apply(c.StatusGroups, c.StatusesProblem);
            statuses.IsEnabled = editable;

            ModeBox.SelectedIndex = c.NotificationIndex;
            if (c.NotificationHint.Length > 0)
            {
                ModeRow.Description = c.NotificationHint;
            }
            else
            {
                ModeRow.ClearValue(CommunityToolkit.WinUI.Controls.SettingsCard.DescriptionProperty);
            }
            ModeRow.IsEnabled = editable;
            foreach (var e in editors)
            {
                e.Apply(c.Entries(e.Kind), c.Suggestions(e.Kind), c.Problem(e.Kind));
                e.IsEnabled = editable && (e.Kind != JiraListKind.Senders || c.SendersEditable);
            }

            SaveButton.IsEnabled = c.CanSave;
        }
        finally
        {
            applying = was;
        }
    }

    // rebuildSpaces: one check box per space, titled "KEY – Name"; the same
    // spaces again only follow their ticks.
    private void ShowSpaces(IReadOnlyList<JiraSpaceRow> rows, IReadOnlySet<string> selected, bool editable)
    {
        var same = rows.Count == spaceItems.Count && rows.Select(r => r.Id).SequenceEqual(spaceItems.Select(i => i.Id));
        if (!same)
        {
            spaceItems.Clear();
            foreach (var r in rows)
            {
                spaceItems.Add(new JiraSpaceItem(r, selected.Contains(r.Id)));
            }
        }
        for (var i = 0; i < rows.Count; i++)
        {
            spaceItems[i].Update(rows[i], selected.Contains(rows[i].Id));
            spaceItems[i].IsEnabled = editable;
        }
        SpacesList.Visibility = rows.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
        NoSpaces.Visibility = rows.Count > 0 ? Visibility.Collapsed : Visibility.Visible;
    }

    // showBusy: the progress of a call (null when none runs) with a
    // spinner; the page follows (Save waits).
    private void ShowBusy(string? text)
    {
        ProgressText.Text = text ?? "";
        ProgressSpinner.IsActive = text is not null;
        Progress.Visibility = text is not null ? Visibility.Visible : Visibility.Collapsed;
        Refresh();
    }

    // showBanner: the page's banner; null hides it.
    private void ShowBanner(string? text)
    {
        Banner.Message = text ?? "";
        Banner.IsOpen = text is not null;
    }

    // onSave: the name as typed, what is still in the field of a list (it
    // is meant to be in the list), then account.update. An entry that
    // cannot be added keeps the window open with its field focused and the
    // reason under it. A list that does not matter in the mode chosen is
    // not looked at: its field is disabled and could not take the focus.
    private void Save()
    {
        controller.SetName(NameBox.Text);
        foreach (var e in editors)
        {
            if (!e.IsEnabled || !e.Pending)
            {
                continue;
            }
            if (!e.Commit())
            {
                e.Field.Focus(FocusState.Programmatic);
                return;
            }
        }
        controller.Save();
    }

    private void OnSaveClick(object sender, RoutedEventArgs e) => Save();

    private void OnCancelClick(object sender, RoutedEventArgs e) => Close();

    private void OnReplaceTokenClick(object sender, RoutedEventArgs e) => controller.ReplaceToken();

    private void OnNameChanged(object sender, TextChangedEventArgs e)
    {
        if (!applying)
        {
            controller.SetName(NameBox.Text);
        }
    }

    // name_row activates-default: Save.
    private void OnNameKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == VirtualKey.Enter && SaveButton.IsEnabled)
        {
            e.Handled = true;
            Save();
        }
    }

    private void OnSpaceClick(object sender, RoutedEventArgs e)
    {
        if (!applying && sender is CheckBox { DataContext: JiraSpaceItem item } box)
        {
            controller.SetSpace(item.Id, box.IsChecked == true);
        }
    }

    private void OnOfflineChanged(object sender, SelectionChangedEventArgs e)
    {
        if (!applying && OfflineBox.SelectedIndex >= 0)
        {
            controller.SetOfflineIndex(OfflineBox.SelectedIndex);
        }
    }

    private void OnOnlyMineToggled(object sender, RoutedEventArgs e)
    {
        if (!applying)
        {
            controller.SetOnlyMine(OnlyMineSwitch.IsOn);
        }
    }

    private void OnShowEventsToggled(object sender, RoutedEventArgs e)
    {
        if (!applying)
        {
            controller.SetShowEvents(ShowEventsSwitch.IsOn);
        }
    }

    private void OnModeChanged(object sender, SelectionChangedEventArgs e)
    {
        if (!applying && ModeBox.SelectedIndex >= 0)
        {
            controller.SetNotificationIndex(ModeBox.SelectedIndex);
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "Jira account settings opened")]
    private static partial void LogPresented(ILogger logger);

    [LoggerMessage(Level = LogLevel.Debug, Message = "Jira account settings closed, its owner enabled, activated {Activated}")]
    private static partial void LogReturned(ILogger logger, bool activated);
}
