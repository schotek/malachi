// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/ContactsPaneViewController.swift;
// GTK: ui/internal/window/preferences_contacts.go (bindContacts,
// buildSuggest, contactsPrefsKey, render, accountGroup). Preferences →
// Contacts over Core's ContactsPreferencesController: the sections are
// built again only when what they show changed (the key), a change is
// shown at once and undone when the daemon refuses it (the controller),
// notify.contactsChanged and notify.accountsChanged reload the page (the
// application's AppContacts hands them on), and Allow… is the re-sign-in
// with the contacts scope in the browser (AppContacts.SignIn). Recipient
// Suggestions writes contacts-suggest-scope, which the compose windows'
// suggestions read for every search.

using System;
using System.Text;
using CommunityToolkit.WinUI.Controls;
using Malachi.App.Compose;
using Malachi.App.Main;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Preferences;

/// <summary>Preferences → Contacts.</summary>
public sealed partial class ContactsPreferencesPage : UserControl
{
    private readonly AppState state;
    private readonly ContactsPreferencesController controller;
    private string key = "";
    private bool syncing;
    private bool closed;

    /// <summary>The page of <paramref name="window"/>, its toasts in <paramref name="toasts"/>.</summary>
    public ContactsPreferencesPage(AppState state, Window window, IToasts toasts)
    {
        ArgumentNullException.ThrowIfNull(state);
        this.state = state;
        InitializeComponent();
        controller = new ContactsPreferencesController(state.Contacts.Options)
        {
            Scope = () => state.Settings.ContactsSuggestScope,
            SetScope = s => state.Settings.ContactsSuggestScope = s,
        };
        SuggestHeading.Text = ContactsText.SuggestionsGroup;
        AutomationProperties.SetName(SuggestGroup, ContactsText.SuggestionsGroup);
        ScopeAccount.Content = ContactsText.ScopeText(ContactSearchScope.Account);
        ScopeAll.Content = ContactsText.ScopeText(ContactSearchScope.All);
        EmptyText.Text = ContactsText.NoContactAccounts;
        controller.Changed += (_, _) => Render();
        controller.SignInRequested += (_, id) => state.Contacts.SignIn(id, window, toasts);
        state.Contacts.Changed += OnContactsChanged;
        controller.Start();
        Render();
    }

    /// <summary>The window closes: no answer is taken any more.</summary>
    public void Close()
    {
        if (closed)
        {
            return;
        }
        closed = true;
        state.Contacts.Changed -= OnContactsChanged;
        controller.Stop();
    }

    private void OnContactsChanged(object? sender, string method) => controller.Handle(method);

    private void Render()
    {
        if (closed)
        {
            return;
        }
        ErrorText.Text = controller.Error;
        ErrorText.Visibility = controller.Error.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        EmptyText.Visibility = controller.Empty ? Visibility.Visible : Visibility.Collapsed;
        syncing = true;
        try
        {
            ScopeAccount.IsChecked = controller.SuggestScope.Value != ContactSearchScope.All;
            ScopeAll.IsChecked = controller.SuggestScope.Value == ContactSearchScope.All;
            var sections = controller.Sections;
            var k = Key(sections);
            if (k == key)
            {
                return;
            }
            key = k;
            SectionsHost.Children.Clear();
            foreach (var s in sections)
            {
                SectionsHost.Children.Add(Section(s));
            }
        }
        finally
        {
            syncing = false;
        }
    }

    private string Key(System.Collections.Generic.IReadOnlyList<ContactsPreferencesSection> sections)
    {
        var b = new StringBuilder();
        foreach (var a in sections)
        {
            b.Append(a.Account.Id.Value).Append('|').Append(a.Title).Append('|').Append(a.Badge).Append('|')
                .Append(a.SyncOn).Append('|').Append(a.Consent).Append('|').Append(a.SignIn).Append('|')
                .Append(controller.IsSaving(a.Account.Id)).Append('|').Append(a.CardDAV).Append(';');
            foreach (var bk in a.Books)
            {
                b.Append(bk.Id.Value).Append('|').Append(bk.Name).Append('|').Append(bk.Visible).Append('|').Append(bk.Count).Append(';');
            }
            b.Append('\n');
        }
        return b.ToString();
    }

    // accountGroup: the name with the capsule, Sync Contacts, the consent,
    // the books, the CardDAV note.
    private ChipGroup Section(ContactsPreferencesSection a)
    {
        var id = a.Account.Id;
        var group = new ChipGroup();
        AutomationProperties.SetAutomationId(group, "PrefsContactsAccount:" + id.Value);
        AutomationProperties.SetName(group, a.Title);
        var panel = new StackPanel { Spacing = (double)Resources["SettingsCardSpacing"] };
        group.Children.Add(panel);

        var heading = new CapsulePanel();
        var title = new TextBlock { Text = a.Title, Style = (Style)Resources["SettingsGroupHeaderStyle"] };
        AutomationProperties.SetHeadingLevel(title, Microsoft.UI.Xaml.Automation.Peers.AutomationHeadingLevel.Level2);
        heading.Children.Add(title);
        if (a.Badge.Length > 0)
        {
            var capsule = new Border
            {
                Style = (Style)Application.Current.Resources["KindBadgeBorderStyle"],
                Child = new TextBlock { Style = (Style)Application.Current.Resources["KindBadgeTextStyle"], Text = a.Badge },
            };
            AutomationProperties.SetAutomationId(capsule, "PrefsContactsAccountKind");
            AutomationProperties.SetName(capsule, a.Badge);
            heading.Children.Add(capsule);
        }
        panel.Children.Add(heading);

        var toggle = new ToggleSwitch { IsOn = a.SyncOn, IsEnabled = !controller.IsSaving(id) };
        AutomationProperties.SetAutomationId(toggle, "PrefsContactsSync");
        AutomationProperties.SetName(toggle, ContactsText.SyncContacts);
        toggle.Toggled += (_, _) =>
        {
            if (!syncing)
            {
                controller.SetSync(id, toggle.IsOn);
            }
        };
        panel.Children.Add(new SettingsCard { Header = ContactsText.SyncContacts, Content = toggle });

        if (a.Consent)
        {
            var card = new SettingsCard { Header = ContactsText.ConsentBanner(a.Title) };
            AutomationProperties.SetAutomationId(card, "PrefsContactsConsent");
            if (a.SignIn)
            {
                var allow = new Button { Content = ContactsText.Allow, Style = (Style)Application.Current.Resources["AccentButtonStyle"] };
                AutomationProperties.SetAutomationId(allow, "PrefsContactsAllow");
                allow.Click += (_, _) => controller.Allow(id);
                card.Content = allow;
            }
            else
            {
                card.Description = ContactsText.ConsentOnline;
            }
            panel.Children.Add(card);
        }

        foreach (var bk in a.Books)
        {
            var name = ContactBook.BookName(bk);
            var check = new CheckBox { IsChecked = bk.Visible, MinWidth = 0 };
            AutomationProperties.SetAutomationId(check, "PrefsContactsBook:" + bk.Id.Value);
            AutomationProperties.SetName(check, name);
            var book = bk.Id;
            check.Click += (_, _) =>
            {
                if (!syncing)
                {
                    controller.SetVisible(book, check.IsChecked == true);
                }
            };
            panel.Children.Add(new SettingsCard
            {
                Header = name,
                Description = ContactsText.ContactCount(Math.Max(bk.Count, 0)),
                Content = check,
            });
        }
        if (a.CardDAV)
        {
            panel.Children.Add(new TextBlock
            {
                Text = ContactsText.CardDAVNote,
                Style = (Style)Resources["SettingsGroupDescriptionStyle"],
                TextWrapping = TextWrapping.Wrap,
            });
        }
        return group;
    }

    private void OnScopeChecked(object sender, RoutedEventArgs e)
    {
        if (syncing)
        {
            return;
        }
        controller.SetSuggestScope(ReferenceEquals(sender, ScopeAll) ? ContactSearchScope.All : ContactSearchScope.Account);
    }

    /// <summary>The accounts and the books anew (the page came up).</summary>
    public void Refresh() => controller.Handle(API.Notify.AccountsChanged);
}
