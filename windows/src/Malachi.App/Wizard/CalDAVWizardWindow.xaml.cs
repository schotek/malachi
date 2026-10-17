// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/AccountWizard/CalDAV/CalDAVSheetController.swift
// (present, dismiss, render, setChecks, sync, discoverClicked, trustClicked,
// addClicked, calendarToggled, mailChanged, handleReturn,
// controlTextDidChange); GTK: none yet (Swift-first, the rules and texts are
// ui/internal/calendar/caldav.go, Core's Cal.CalDAVWizard). The flow, its
// calls and the certificate's trust are Core's CalDAVWizardController; this
// window shows what it holds after every Changed and feeds the fields back.
//
// Windows (docs/windows-port.md §11.3): the sheet is an owned modal window
// over the window it was opened from (ModalDialog), 560×560 effective
// pixels like the sheet, as the Jira account assistant is; its close
// button, Escape and Ctrl+W cancel it (the tracked window's CloseWindow),
// as Cancel does, and the controller drops every late answer from then on
// (an Add already sent still reaches the daemon). The fields are the
// wizards' cards (a flagged field's title in the critical colour), the
// error a warning InfoBar. Return in a field runs Discover Calendars until
// the calendars are listed, then Add. Trust Certificate… asks the account
// wizard's "Trust This Certificate?" with the SHA-256 fingerprint on this
// window. Once account.add stored the account the caller's completion
// runs, the window's calendar source is asked for its snapshot anew (the
// sections and sidebars over it follow), and the window closes. Nothing
// here logs a field: the password goes from its box to the controller only
// and is cleared once the account is added.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Preferences;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Presentation;
using Malachi.Core.Text;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Field = Malachi.Core.Calendars.Cal.CalDAVWizard.Field;
using Phase = Malachi.Core.Calendars.Cal.CalDAVWizard.WizardPhase;
using VirtualKey = Windows.System.VirtualKey;

namespace Malachi.App.Wizard;

/// <summary>The Add CalDAV Calendar window.</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The controller is cancelled with the window (Closed).")]
public sealed partial class CalDAVWizardWindow : Window
{
    /// <summary>The sheet's width (CalDAVSheetController's content rect).</summary>
    public const int ContentWidth = 560;

    /// <summary>The sheet's height.</summary>
    public const int ContentHeight = 560;

    private readonly CalDAVWizardController wizard;
    private readonly Action<AccountId>? done;
    private readonly ILogger logger;
    private readonly ModalDialog modal;

    // The calendars and the mail accounts as listed now.
    private IReadOnlyList<CalDAVWizardController.ListedCalendar> shownCalendars = [];
    private readonly List<CalendarRowView> calendarRows = [];
    private IReadOnlyList<CalDAVWizardController.MailAccount> shownMailAccounts = [];

    // The widgets are being set from the controller: their events are not the user's.
    private bool applying;

    // The phase last drawn: a call that ends hands the keyboard back (RestoreFocus).
    private Phase lastPhase = Phase.Entering;

    private CalDAVWizardWindow(AppState state, ICalendarDataSource source, Action<AccountId>? done)
    {
        this.done = done;
        logger = state.Logs.CreateLogger<CalDAVWizardWindow>();
        InitializeComponent();
        wizard = new CalDAVWizardController(source);

        Title = Cal.CalDAVWizard.Title;
        ExtendsContentIntoTitleBar = true;
        SetTitleBar(Header);
        Header.Title = Cal.CalDAVWizard.Title;
        // The window keeps the sheet's name (Alt+Tab, UIA) after the title
        // bar names it (AccountWizardWindow).
        Header.Loaded += (_, _) => Title = Cal.CalDAVWizard.Title;
        var icon = System.IO.Path.Combine(AppContext.BaseDirectory, "Assets", "Malachi.ico");
        if (System.IO.File.Exists(icon))
        {
            AppWindow.SetIcon(icon);
        }
        Tracked = state.Windows.Track(this, WindowKind.Wizard, Root, ToastsHost);
        modal = new ModalDialog(this);
        Build();

        wizard.Changed += (_, _) => Render();
        // The account assistant's "Trust This Certificate?" on this window.
        wizard.ConfirmTrust = p => state.Alerts.ConfirmTrustCertificateAsync(this, p.Heading, p.Body, p.Details, p.ConfirmLabel);
        wizard.Done += (_, id) =>
        {
            try
            {
                done?.Invoke(id);
            }
            finally
            {
                wizard.Source.Refresh();
                Close();
            }
        };
        Root.Loaded += (_, _) => ServerBox.Focus(FocusState.Programmatic);
        Closed += (_, _) =>
        {
            wizard.Cancel();
            if (modal.ReturnToOwner() is { } activated)
            {
                LogReturned(logger, activated);
            }
        };
        Render();
    }

    /// <summary>The window as the shell tracks it.</summary>
    public TrackedWindow Tracked { get; }

    /// <summary>
    /// Opens the window over <paramref name="owner"/> (the main window when
    /// null or hidden, shown first) over <paramref name="source"/>, the
    /// calendar source of the window it opens from (its snapshot names the
    /// mail accounts invitations can leave from). <paramref name="done"/>
    /// runs after account.add stored the account, before the window closes.
    /// Null when there is no window to open it over.
    /// </summary>
    public static CalDAVWizardWindow? Show(AppState state, Window? owner, ICalendarDataSource source, Action<AccountId>? done = null)
    {
        ArgumentNullException.ThrowIfNull(state);
        ArgumentNullException.ThrowIfNull(source);
        if (owner is null || !owner.AppWindow.IsVisible)
        {
            state.ShowMainWindow();
            owner = state.MainWindow;
        }
        if (owner is null || state.IsStopping)
        {
            return null;
        }
        var w = new CalDAVWizardWindow(state, source, done);
        w.wizard.Start();
        w.modal.Present(owner, ContentWidth, ContentHeight);
        LogPresented(w.logger);
        return w;
    }

    // The texts and the rows that never change.
    private void Build()
    {
        TitleText.Text = Cal.CalDAVWizard.Title;
        SubtitleText.Text = Cal.CalDAVWizard.Subtitle;
        Label(NameRow, NameBox, Field.Name);
        Label(ServerRow, ServerBox, Field.Server);
        Label(UsernameRow, UsernameBox, Field.Username);
        Label(PasswordRow, PasswordField, Field.Password);
        PasswordRow.Description = Cal.CalDAVWizard.PasswordNote;
        ServerBox.PlaceholderText = Cal.CalDAVWizard.ServerPlaceholder;
        DiscoverButton.Content = Cal.CalDAVWizard.DiscoverButton;
        CertificateRow.Header = Cal.CalDAVWizard.CertificateLabel;
        TrustButton.Content = Cal.CalDAVWizard.TrustButton;
        CalendarsHeading.Text = Cal.CalDAVWizard.CalendarsHeading;
        AutomationProperties.SetName(CalendarsBox, Cal.CalDAVWizard.CalendarsHeading);
        MailAccountRow.Header = Cal.Prefs.MailAccountSetting;
        AutomationProperties.SetName(MailAccountBox, Cal.Prefs.MailAccountSetting);
        FooterText.Text = Cal.CalDAVWizard.Footer;
        AddButton.Content = Cal.CalDAVWizard.AddButton;
    }

    private static void Label(CommunityToolkit.WinUI.Controls.SettingsCard row, Control field, Field f)
    {
        var label = Cal.CalDAVWizard.FieldLabel(f);
        row.Header = label;
        AutomationProperties.SetName(field, label);
    }

    // render: the fields, the buttons, the call under way, the error, the
    // certificate, the calendars, the mail account.
    private void Render()
    {
        if (wizard.IsClosed)
        {
            return;
        }
        var w = wizard.Wizard;
        applying = true;
        try
        {
            var editable = w.FieldsEnabled;
            NameBox.IsEnabled = editable;
            ServerBox.IsEnabled = editable;
            UsernameBox.IsEnabled = editable;
            PasswordField.IsEnabled = editable;
            // The server's display name fills an empty Account Name.
            if (NameBox.FocusState == FocusState.Unfocused && NameBox.Text != w.Name)
            {
                NameBox.Text = w.Name;
            }
            if (w.Phase == Phase.Added)
            {
                PasswordField.Password = "";
            }
            DiscoverButton.IsEnabled = w.CanDiscover;
            var progress = w.Progress;
            ProgressText.Text = progress;
            ProgressSpinner.IsActive = progress.Length > 0;
            ProgressSpinner.Visibility = progress.Length > 0 ? Visibility.Visible : Visibility.Collapsed;

            var error = wizard.ErrorText;
            ErrorBar.Message = error;
            ErrorBar.IsOpen = error.Length > 0;
            var code = w.Error?.Code.Value;
            WizardRows.SetError(Root, ServerRow, code == ErrorCode.InvalidArgument);
            WizardRows.SetError(Root, PasswordRow, code is ErrorCode.AuthFailed or ErrorCode.AuthRequired);
            ServerChangedText.Text = w.ServerChanged;
            ServerChangedText.Visibility = w.ServerChanged.Length > 0 ? Visibility.Visible : Visibility.Collapsed;

            var cert = w.CertificateText;
            var offer = w.TrustOffer is not null;
            CertificateText.Text = cert;
            CertificateText.Visibility = cert.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
            ToolTipService.SetToolTip(CertificateText, cert.Length > 0 ? cert : null);
            TrustButton.Visibility = offer ? Visibility.Visible : Visibility.Collapsed;
            CertificateRow.Visibility = cert.Length == 0 && !offer ? Visibility.Collapsed : Visibility.Visible;

            RenderCalendars(w);
            RenderMailAccounts(w);

            AddButton.IsEnabled = w.CanAdd;
        }
        finally
        {
            applying = false;
        }
        if (w.Phase != lastPhase && lastPhase is Phase.Discovering or Phase.Adding)
        {
            RestoreFocus(w);
        }
        lastPhase = w.Phase;
    }

    // A call disables the fields and the buttons while it runs, which takes
    // the keyboard from them (Windows-only: AppKit keeps the first
    // responder). Once it ends the keyboard goes where the next step is:
    // the first calendar after the listing, the flagged field after an
    // error, else Discover Calendars.
    private void RestoreFocus(Cal.CalDAVWizard w)
    {
        if (Root.XamlRoot is not { } root || FocusManager.GetFocusedElement(root) is Control { IsEnabled: true } and not ScrollViewer)
        {
            return;
        }
        var code = w.Error?.Code.Value;
        Control target = w.Phase == Phase.Listed && calendarRows.Count > 0 ? calendarRows[0].Box
            : code == ErrorCode.InvalidArgument ? ServerBox
            : code is ErrorCode.AuthFailed or ErrorCode.AuthRequired ? PasswordField
            : DiscoverButton.IsEnabled ? DiscoverButton
            : CancelButton;
        DispatcherQueue.TryEnqueue(() => target.Focus(FocusState.Programmatic));
    }

    // The found calendars: the cards again only when the calendars listed
    // changed (in place otherwise); the check boxes follow the selection,
    // usable while listed.
    private void RenderCalendars(Cal.CalDAVWizard w)
    {
        var listed = wizard.ListedCalendars;
        CalendarsGroup.Visibility = w.Listed is null ? Visibility.Collapsed : Visibility.Visible;
        FoundText.Text = w.FoundLine;
        FoundText.Visibility = w.FoundLine.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        if (calendarRows.Count != listed.Count || !listed.Select(c => c.Href).SequenceEqual(shownCalendars.Select(c => c.Href)))
        {
            foreach (var view in calendarRows)
            {
                view.Box.Click -= OnCalendarClick;
            }
            calendarRows.Clear();
            CalendarsList.Children.Clear();
            for (var i = 0; i < listed.Count; i++)
            {
                var view = new CalendarRowView(Root, "CalDAVCalendar:" + i) { Box = { Tag = i } };
                view.Box.Click += OnCalendarClick;
                calendarRows.Add(view);
                CalendarsList.Children.Add(view.Card);
            }
        }
        shownCalendars = listed;
        var usable = w.Phase == Phase.Listed;
        for (var i = 0; i < calendarRows.Count; i++)
        {
            var c = listed[i];
            calendarRows[i].Update(c.Name, c.Color, c.Note);
            calendarRows[i].Box.IsChecked = c.Selected;
            calendarRows[i].Box.IsEnabled = usable;
        }
    }

    // The mail-account popup: None, then the accounts that can send.
    private void RenderMailAccounts(Cal.CalDAVWizard w)
    {
        MailAccountRow.Visibility = w.ShowsMailAccount ? Visibility.Visible : Visibility.Collapsed;
        var accounts = wizard.MailAccounts;
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
        var i = w.MailAccount is { } id ? IndexOf(accounts, id) : -1;
        MailAccountBox.SelectedIndex = i >= 0 ? i + 1 : 0;
        MailAccountBox.IsEnabled = w.FieldsEnabled;
    }

    private static int IndexOf(IReadOnlyList<CalDAVWizardController.MailAccount> accounts, AccountId id)
    {
        for (var i = 0; i < accounts.Count; i++)
        {
            if (accounts[i].Id == id)
            {
                return i;
            }
        }
        return -1;
    }

    // sync: the four fields as typed.
    private void Sync() => wizard.Edit(NameBox.Text, ServerBox.Text, UsernameBox.Text, PasswordField.Password);

    private void OnFieldChanged(object sender, TextChangedEventArgs e)
    {
        if (!applying)
        {
            Sync();
        }
    }

    private void OnPasswordChanged(object sender, RoutedEventArgs e)
    {
        if (!applying)
        {
            Sync();
        }
    }

    // handleReturn: Discover Calendars until the calendars are listed, then Add.
    private void OnFieldKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key != VirtualKey.Enter)
        {
            return;
        }
        e.Handled = true;
        Sync();
        var w = wizard.Wizard;
        if (w.Phase == Phase.Listed && w.CanAdd)
        {
            wizard.Add();
        }
        else if (w.CanDiscover)
        {
            wizard.Discover();
        }
    }

    private void OnDiscoverClick(object sender, RoutedEventArgs e)
    {
        Sync();
        wizard.Discover();
    }

    private void OnTrustClick(object sender, RoutedEventArgs e) => wizard.Trust();

    private void OnAddClick(object sender, RoutedEventArgs e)
    {
        Sync();
        wizard.Add();
    }

    private void OnCancelClick(object sender, RoutedEventArgs e) => Close();

    private void OnCalendarClick(object sender, RoutedEventArgs e)
    {
        if (applying || sender is not CheckBox { Tag: int i } box || i < 0 || i >= shownCalendars.Count)
        {
            return;
        }
        wizard.Toggle(shownCalendars[i].Href, box.IsChecked == true);
    }

    private void OnMailAccountChanged(object sender, SelectionChangedEventArgs e)
    {
        if (applying || wizard.IsClosed)
        {
            return;
        }
        var i = MailAccountBox.SelectedIndex - 1;
        AccountId? chosen = null;
        if (i >= 0 && i < shownMailAccounts.Count)
        {
            chosen = shownMailAccounts[i].Id;
        }
        wizard.SetMailAccount(chosen);
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "Add CalDAV Calendar opened")]
    private static partial void LogPresented(ILogger logger);

    [LoggerMessage(Level = LogLevel.Debug, Message = "Add CalDAV Calendar closed, its owner enabled, activated {Activated}")]
    private static partial void LogReturned(ILogger logger, bool activated);
}
