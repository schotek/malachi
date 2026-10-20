// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/CalendarAccountGroups.swift
// (start, stop, render, renderCalendar, the actions, replacePasswordClicked,
// ask) and of CalendarPaneViewController.swift's removeAccount; GTK: none yet
// (Swift-first, the rows' texts and rules are ui/internal/calendar/
// preferences.go, Core's Cal.Prefs). Everything the section decides is
// Core's CalendarAccountController; this shows what it holds after every
// Changed and hands the clicks back.
//
// Which rows show: the page's Cal.Prefs.PreferencesLayout section names
// them, and the controller's own rules take some away as macOS does: the
// sync switch only for a Microsoft 365 account (ShowsSyncSwitch), the
// consent's row while its grant lacks the calendar scopes, the calendars,
// Default Calendar for New Events (with a calendar to choose) and Keep
// Events only while sync is on, and the Invitations rows only for a
// Microsoft 365 account (docs/calendar.md §5.8 "As built (M9)"). A change
// the controller refuses raises no Changed (an empty name, a calendar it
// does not list), so every handler draws the section again after it.
//
// Windows (named in windows/README.md): the dialogs are ContentDialogs on
// the Preferences window (Replace Password… with a PasswordBox, the
// consent's Sign In… / Cancel, the account pages' removal question with
// its check box); a CalDAV account's name is edited here (Account Name,
// committed on Enter and when the field loses the keyboard), since this
// is where the account's row on the Integrations page leads (its Edit,
// Calendar Settings); Remove Account, with the Integrations page's CalDAV
// sentence (Integrations.RowFor), sits at the end
// of the section, as PreferencesLayout orders it, where macOS ends the
// Account group with it; the calendar rows' check boxes carry the colour
// and the name as their label (a click on the name toggles them).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Malachi.Core.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Field = Malachi.Core.Calendars.Cal.CalDAVWizard.Field;
using VirtualKey = Windows.System.VirtualKey;

namespace Malachi.App.Preferences;

/// <summary>One account's section of Preferences → Calendar.</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The page stops the controller when the section goes or the window closes (Stop).")]
public sealed partial class CalendarAccountSection : UserControl
{
    private readonly AppState state;
    private readonly Window window;
    private readonly CalendarAccountController controller;

    // The section's rows as the page's layout names them.
    private Cal.PrefsSection layout;

    // The calendar rows shown, their cards, and the Default popup's choices.
    private IReadOnlyList<CalendarAccountController.CalendarRow> shownRows = [];
    private IReadOnlyList<CalendarAccountController.CalendarRow> shownDefaults = [];
    private readonly List<CalendarRowView> calendarRows = [];
    private IReadOnlyList<CalDAVWizardController.MailAccount> shownMailAccounts = [];

    // The widgets are being set from the controller: their events are not the user's.
    private bool syncing;

    /// <summary>The section of <paramref name="section"/>'s account over <paramref name="source"/>, not started.</summary>
    /// <param name="state">The application.</param>
    /// <param name="window">The Preferences window: the dialogs go on it.</param>
    /// <param name="source">The window's calendar source.</param>
    /// <param name="section">The account's section of <see cref="Cal.Prefs.PreferencesLayout"/>.</param>
    internal CalendarAccountSection(AppState state, Window window, ICalendarDataSource source, Cal.PrefsSection section)
    {
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(source);
        ArgumentNullException.ThrowIfNull(section);
        this.state = state;
        this.window = window;
        layout = section;
        InitializeComponent();
        var id = section.AccountId ?? default;
        // The samples refuse Remove Account and the sign-in: no daemon.
        controller = new CalendarAccountController(source, id, state.CalendarSamples ? null : state.Client);
        AutomationProperties.SetAutomationId(SectionGroup, "PrefsCalendarAccount:" + id.Value);
        Build();
        controller.Changed += (_, _) => Render();
        controller.AskQuestion = AskAsync;
    }

    /// <summary>The account's controller (the page answers its sign-in, toasts and Add CalDAV Calendar…).</summary>
    public CalendarAccountController Controller => controller;

    /// <summary>The account.</summary>
    public AccountId AccountId => controller.AccountId;

    /// <summary>Follows the source (and lists a CalDAV server's calendars).</summary>
    public void Start()
    {
        controller.Start();
        Render();
    }

    /// <summary>The section went or the window closed: late answers are dropped (a change already sent still reaches the daemon).</summary>
    public void Stop() => controller.Stop();

    /// <summary>The page's layout for the account changed (its kind, its rows).</summary>
    public void Apply(Cal.PrefsSection section)
    {
        ArgumentNullException.ThrowIfNull(section);
        if (section.Kind == layout.Kind && section.AccountKind == layout.AccountKind && section.Rows.SequenceEqual(layout.Rows))
        {
            return;
        }
        layout = section;
        Render();
    }

    // build: the texts and the fixed choices.
    private void Build()
    {
        NameRow.Header = Cal.CalDAVWizard.FieldLabel(Field.Name);
        AutomationProperties.SetName(NameBox, Cal.CalDAVWizard.FieldLabel(Field.Name));
        ServerRow.Header = Cal.CalDAVWizard.FieldLabel(Field.Server);
        UsernameRow.Header = Cal.CalDAVWizard.FieldLabel(Field.Username);
        PasswordRow.Header = Cal.CalDAVWizard.FieldLabel(Field.Password);
        PasswordRow.Description = Cal.CalDAVWizard.PasswordNote;
        ReplacePasswordButton.Content = Cal.Prefs.ReplacePassword;
        MailAccountRow.Header = Cal.Prefs.MailAccountSetting;
        AutomationProperties.SetName(MailAccountBox, Cal.Prefs.MailAccountSetting);
        CertificateRow.Header = Cal.CalDAVWizard.CertificateLabel;
        // No access key: two sections with a pin would share it.
        ForgetButton.Content = Mnemonic.Strip(L10n.T("_Forget"));

        CalendarHeading.Text = Cal.Prefs.AccountsGroup;
        SyncRow.Header = Cal.Prefs.SyncSetting;
        AutomationProperties.SetName(SyncSwitch, Cal.Prefs.SyncSetting);
        ConsentRow.Header = Cal.Text.ConsentLine;
        ConsentRow.Description = Cal.Prefs.SyncConsentNote;
        SignInButton.Content = Cal.Text.SignIn;
        DefaultRow.Header = Cal.Prefs.DefaultCalendarSetting;
        AutomationProperties.SetName(DefaultBox, Cal.Prefs.DefaultCalendarSetting);
        KeepRow.Header = Cal.Prefs.KeepSetting;
        AutomationProperties.SetName(KeepBox, Cal.Prefs.KeepSetting);
        foreach (var days in CalendarAccountController.KeepChoices)
        {
            KeepBox.Items.Add(new ComboBoxItem { Content = Cal.Prefs.KeepTitle(days) });
        }

        InvitationsHeading.Text = Cal.Prefs.InvitationsGroup;
        CardRow.Header = Cal.Prefs.CardSetting;
        AutomationProperties.SetName(CardSwitch, Cal.Prefs.CardSetting);
        TentativeRow.Header = Cal.Prefs.TentativeSetting;
        TentativeRow.Description = Cal.Prefs.TentativeSubtitle;
        AutomationProperties.SetName(TentativeSwitch, Cal.Prefs.TentativeSetting);
        AfterRow.Header = Cal.Prefs.AfterAnswerSetting;
        AutomationProperties.SetName(AfterBox, Cal.Prefs.AfterAnswerSetting);
        foreach (var a in Cal.Prefs.AfterAnswers)
        {
            AfterBox.Items.Add(new ComboBoxItem { Content = Cal.Prefs.AfterAnswerTitle(a) });
        }
        RemoveButton.Content = Cal.Prefs.RemoveAccount;
    }

    private bool Has(Cal.PrefsRow row) => layout.Rows.Contains(row);

    private static Visibility Shown(bool on) => on ? Visibility.Visible : Visibility.Collapsed;

    // render: the heading, the groups that apply and their values.
    private void Render()
    {
        if (controller.IsClosed)
        {
            return;
        }
        var c = controller;
        var account = c.Account;
        var loaded = account is not null;
        syncing = true;
        try
        {
            AccountGroup.Visibility = Shown(loaded && c.IsCalDAV);
            CalendarGroup.Visibility = Shown(loaded && c.HasCalendarCapability && Has(Cal.PrefsRow.Calendars));
            // The invitation settings are a mail account's; a CalDAV account
            // receives no mail (preferences.go PreferencesLayout), and macOS
            // shows them for a Microsoft 365 account only.
            InvitationsGroup.Visibility = Shown(loaded && c.IsGraph && Has(Cal.PrefsRow.Card));
            RemoveButton.Visibility = Shown(loaded && c.IsCalDAV && Has(Cal.PrefsRow.Remove));
            BusyRing.IsActive = c.Saving || c.Removing;
            BusyRing.Visibility = Shown(c.Saving || c.Removing);
            if (account is null)
            {
                return;
            }
            var title = DisplayText.Clean(Core.Model.AccountsPage.AccountRowTitle(account));
            NameText.Text = title;
            ToolTipService.SetToolTip(NameText, title);
            var badge = FolderTree.AccountHeaderBadge(account);
            CapsuleText.Text = badge;
            Capsule.Visibility = Shown(badge.Length > 0);
            AutomationProperties.SetName(SectionGroup, title);

            RenderCalendar();

            CardSwitch.IsOn = c.InvitationCard;
            TentativeSwitch.IsOn = c.ShowUnanswered;
            AfterBox.SelectedIndex = Math.Max(0, IndexOf(Cal.Prefs.AfterAnswers, c.AfterAnswer));

            if (c.IsCalDAV)
            {
                RenderAccount();
            }
            RemoveButton.IsEnabled = !c.Removing;
        }
        finally
        {
            syncing = false;
        }
    }

    // The Account group of a CalDAV account.
    private void RenderAccount()
    {
        var c = controller;
        if (NameBox.FocusState == FocusState.Unfocused && NameBox.Text != c.Name)
        {
            NameBox.Text = c.Name;
        }
        ServerRow.Visibility = Shown(Has(Cal.PrefsRow.Server));
        UsernameRow.Visibility = Shown(Has(Cal.PrefsRow.Username));
        PasswordRow.Visibility = Shown(Has(Cal.PrefsRow.ReplacePassword));
        MailAccountRow.Visibility = Shown(Has(Cal.PrefsRow.MailAccount));
        CertificateRow.Visibility = Shown(Has(Cal.PrefsRow.Certificate));
        var server = DisplayText.Clean(c.ServerHost);
        ServerText.Text = server;
        ToolTipService.SetToolTip(ServerText, server.Length > 0 ? server : null);
        var username = DisplayText.Clean(c.Username);
        UsernameText.Text = username;
        ToolTipService.SetToolTip(UsernameText, username.Length > 0 ? username : null);
        CertificateText.Text = c.CertificateText;
        ForgetButton.Visibility = Shown(c.CertificatePinned);
        var accounts = c.MailAccounts;
        if (!accounts.SequenceEqual(shownMailAccounts) || MailAccountBox.Items.Count == 0)
        {
            shownMailAccounts = accounts;
            MailAccountBox.Items.Clear();
            MailAccountBox.Items.Add(new ComboBoxItem { Content = Cal.Prefs.NoMailAccount });
            foreach (var a in accounts)
            {
                // A string content is shown by a TextBlock's Text, never parsed.
                MailAccountBox.Items.Add(new ComboBoxItem { Content = DisplayText.Clean(a.Name) });
            }
        }
        var i = -1;
        if (c.MailAccountId is { } id)
        {
            for (var k = 0; k < shownMailAccounts.Count; k++)
            {
                if (shownMailAccounts[k].Id == id)
                {
                    i = k;
                    break;
                }
            }
        }
        MailAccountBox.SelectedIndex = i + 1;
        AccountError.Text = c.Error;
        AccountError.Visibility = Shown(c.Error.Length > 0);
    }

    // renderCalendar: the cards again only when the calendars listed
    // changed (in place otherwise, so the keyboard stays on a check box), the
    // switches' and popups' values every time.
    private void RenderCalendar()
    {
        var c = controller;
        var rows = c.SyncOn ? c.Rows : [];
        var defaults = c.SyncOn ? c.DefaultChoices : [];
        var consent = c.IsGraph && c.SyncOn && c.ConsentRequired;
        if (calendarRows.Count != rows.Count || !rows.Select(r => r.RemoteId).SequenceEqual(shownRows.Select(r => r.RemoteId)))
        {
            foreach (var view in calendarRows)
            {
                view.Box.Click -= OnCalendarClick;
            }
            calendarRows.Clear();
            CalendarList.Children.Clear();
            for (var i = 0; i < rows.Count; i++)
            {
                var view = new CalendarRowView(this, "PrefsCalendarCalendar:" + i) { Box = { Tag = i } };
                view.Box.Click += OnCalendarClick;
                calendarRows.Add(view);
                CalendarList.Children.Add(view.Card);
            }
        }
        shownRows = rows;
        for (var i = 0; i < calendarRows.Count; i++)
        {
            var r = rows[i];
            calendarRows[i].Update(r.Name, r.Color, r.Note);
            calendarRows[i].Box.IsEnabled = r.CanToggle;
            calendarRows[i].Box.IsChecked = r.Enabled;
        }
        if (DefaultBox.Items.Count != defaults.Count
            || !defaults.Select(d => (d.RemoteId, d.Name)).SequenceEqual(shownDefaults.Select(d => (d.RemoteId, d.Name))))
        {
            DefaultBox.Items.Clear();
            foreach (var d in defaults)
            {
                DefaultBox.Items.Add(new ComboBoxItem { Content = DisplayText.Clean(d.Name) });
            }
        }
        shownDefaults = defaults;
        SyncRow.Visibility = Shown(c.ShowsSyncSwitch && Has(Cal.PrefsRow.Sync));
        ConsentRow.Visibility = Shown(consent);
        CalendarList.Visibility = Shown(rows.Count > 0);
        DefaultRow.Visibility = Shown(c.SyncOn && shownDefaults.Count > 0 && Has(Cal.PrefsRow.DefaultCalendar));
        KeepRow.Visibility = Shown(c.SyncOn && Has(Cal.PrefsRow.Keep));
        SyncSwitch.IsOn = c.SyncOn;
        var selected = -1;
        for (var i = 0; i < shownDefaults.Count; i++)
        {
            if (shownDefaults[i].IsDefault)
            {
                selected = i;
                break;
            }
        }
        DefaultBox.SelectedIndex = selected;
        KeepBox.SelectedIndex = IndexOf(CalendarAccountController.KeepChoices, c.KeepDays);
        // A refusal shows once: above the Account group of a CalDAV account.
        var error = c.IsCalDAV ? "" : c.Error;
        CalendarError.Text = error;
        CalendarError.Visibility = Shown(error.Length > 0);
    }

    private static int IndexOf<T>(IReadOnlyList<T> list, T value)
        where T : IEquatable<T>
    {
        for (var i = 0; i < list.Count; i++)
        {
            if (list[i].Equals(value))
            {
                return i;
            }
        }
        return -1;
    }

    // Actions. A refused change raises no Changed: the section is drawn again after each.

    private void OnSyncToggled(object sender, RoutedEventArgs e)
    {
        if (syncing)
        {
            return;
        }
        controller.SetSync(SyncSwitch.IsOn);
        Render();
    }

    private void OnSignInClick(object sender, RoutedEventArgs e) => controller.SignIn();

    private void OnCalendarClick(object sender, RoutedEventArgs e)
    {
        if (syncing || sender is not CheckBox { Tag: int i } box || i < 0 || i >= shownRows.Count || shownRows[i].RemoteId is not { } id)
        {
            return;
        }
        controller.SetEnabled(box.IsChecked == true, id);
        Render();
    }

    private void OnDefaultChanged(object sender, SelectionChangedEventArgs e)
    {
        var i = DefaultBox.SelectedIndex;
        if (syncing || i < 0 || i >= shownDefaults.Count)
        {
            return;
        }
        controller.SetDefaultCalendar(shownDefaults[i].RemoteId);
        Render();
    }

    private void OnKeepChanged(object sender, SelectionChangedEventArgs e)
    {
        var i = KeepBox.SelectedIndex;
        var choices = CalendarAccountController.KeepChoices;
        if (syncing || i < 0 || i >= choices.Count)
        {
            return;
        }
        controller.SetKeepDays(choices[i]);
        Render();
    }

    private void OnCardToggled(object sender, RoutedEventArgs e)
    {
        if (syncing)
        {
            return;
        }
        controller.SetInvitationCard(CardSwitch.IsOn);
        Render();
    }

    private void OnTentativeToggled(object sender, RoutedEventArgs e)
    {
        if (syncing)
        {
            return;
        }
        controller.SetShowUnanswered(TentativeSwitch.IsOn);
        Render();
    }

    private void OnAfterChanged(object sender, SelectionChangedEventArgs e)
    {
        var i = AfterBox.SelectedIndex;
        if (syncing || i < 0 || i >= Cal.Prefs.AfterAnswers.Count)
        {
            return;
        }
        controller.SetAfterAnswer(Cal.Prefs.AfterAnswers[i]);
        Render();
    }

    private void OnMailAccountChanged(object sender, SelectionChangedEventArgs e)
    {
        if (syncing || MailAccountBox.SelectedIndex < 0)
        {
            return;
        }
        var i = MailAccountBox.SelectedIndex - 1;
        AccountId? chosen = null;
        if (i >= 0 && i < shownMailAccounts.Count)
        {
            chosen = shownMailAccounts[i].Id;
        }
        controller.SetMailAccount(chosen);
        Render();
    }

    // The account's name: written when the field is left or on Enter; an
    // empty name is refused and the field shows the stored one again.
    private void CommitName()
    {
        if (syncing || controller.IsClosed)
        {
            return;
        }
        controller.SetName(NameBox.Text);
        if (NameBox.Text.Trim().Length == 0)
        {
            syncing = true;
            NameBox.Text = controller.Name;
            syncing = false;
        }
        Render();
    }

    private void OnNameLostFocus(object sender, RoutedEventArgs e) => CommitName();

    private void OnNameKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == VirtualKey.Enter)
        {
            e.Handled = true;
            CommitName();
        }
    }

    private void OnForgetClick(object sender, RoutedEventArgs e)
    {
        controller.ForgetCertificate();
        Render();
    }

    // replacePasswordClicked: the new password in a dialog on the window; it
    // goes to the keyring with account.update and is not kept.
    private async void OnReplacePasswordClick(object sender, RoutedEventArgs e)
    {
        var password = await state.Alerts.PasswordAsync(
            window, Cal.Prefs.ReplacePasswordTitle, Cal.CalDAVWizard.PasswordNote, Cal.CalDAVWizard.FieldLabel(Field.Password), L10n.T("Replace"));
        if (password is null || controller.IsClosed)
        {
            return;
        }
        controller.ReplacePassword(password);
    }

    // removeAccount: the Accounts page's question, then account.remove; the
    // section goes with the source's next snapshot.
    private void OnRemoveClick(object sender, RoutedEventArgs e) =>
        controller.Remove(async p => await state.Alerts.ConfirmDestructiveExtraAsync(window, p.Heading, p.Body, p.ConfirmLabel, p.ExtraLabel, p.ExtraDefault));

    // ask: the consent's explanation, Sign In… or Cancel.
    private async System.Threading.Tasks.Task<Cal.Answer> AskAsync(Cal.Question q) =>
        await state.Alerts.ConfirmAsync(window, q.Heading, q.Body, q.Confirm, L10n.T("_Cancel")) ? Cal.Answer.Confirm : Cal.Answer.Cancel;
}
