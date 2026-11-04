// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/AccountWizard/EWay/EWayWizardWindowController.swift
// (EWayCredentialsPageController: apply, setBusy, showProblem, sync,
// handleReturn); GTK: ui/internal/accountwizard/eway.go (written in T7; the
// flow is eway_flow.go). Every keystroke goes to the controller
// (EwayWizardController.SetName, SetCredentials); Return in the name or
// the user name moves on, Return in the password is the page's button. The
// password is never logged: it goes from the PasswordBox to the controller
// and no further.

using Malachi.Core.Controllers;
using Malachi.Core.Crm;
using Malachi.Core.I18n;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Windows.System;

namespace Malachi.App.Wizard;

/// <summary>The eWay-CRM assistant's sign-in page.</summary>
public sealed partial class EwayCredentialsPage : UserControl
{
    private readonly EwayWizardController wizard;
    private bool busy;

    // Set while the fields are filled from the controller: not keystrokes.
    private bool applying;

    /// <summary>The page of <paramref name="wizard"/>.</summary>
    public EwayCredentialsPage(EwayWizardController wizard)
    {
        this.wizard = wizard;
        InitializeComponent();
        var texts = wizard.Texts;
        NameRow.Header = texts.NameLabel;
        AutomationProperties.SetName(NameBox, texts.NameLabel);
        UserRow.Header = texts.UserLabel;
        AutomationProperties.SetName(UserBox, texts.UserLabel);
        PasswordRow.Header = wizard.PasswordLabel;
        AutomationProperties.SetName(PasswordBox, wizard.PasswordLabel);
        FollowBox.Content = texts.FollowThreads;
        FollowHint.Text = texts.FollowThreadsHint;
        AutomationProperties.SetHelpText(FollowBox, texts.FollowThreadsHint);
        Bar.SetLabel(wizard.NextLabel(CrmWizardPage.Credentials), L10n.T("_Add Account"), L10n.T("_Save"));
        AutomationProperties.SetAutomationId(Bar.Button, "EwaySignIn");
        Bar.Button.Click += (_, _) => Next();
        applying = true;
        UserBox.Text = wizard.User;
        NameBox.Text = wizard.Name;
        FollowBox.IsChecked = wizard.FollowThreads;
        applying = false;
        Apply();
    }

    /// <summary>The user name while it is still to type, the password otherwise.</summary>
    public Control InitialFocus => UserBox.Text.Trim().Length == 0 ? UserBox : PasswordBox;

    /// <summary>The address the page shows (the found instance's, or the edited account's).</summary>
    public void ShowService(string url) => ServiceUrl.Text = url;

    /// <summary>onName: the name field's text (the default name, until the user types one).</summary>
    public void ShowName(string name)
    {
        if (NameBox.Text == name)
        {
            return;
        }
        applying = true;
        NameBox.Text = name;
        applying = false;
    }

    /// <summary>A call runs (its progress text) or none (null).</summary>
    public void SetBusy(string? progress)
    {
        busy = progress is not null;
        Bar.ShowProgress(progress);
        Apply();
    }

    /// <summary>The page's banner; null hides it.</summary>
    public void ShowBanner(string? text)
    {
        Banner.Message = text ?? "";
        Banner.IsOpen = text is not null;
    }

    /// <summary>Flags the user name or the password field.</summary>
    public void ShowProblem(CrmWizardField field)
    {
        WizardRows.SetError(this, UserRow, field == CrmWizardField.User);
        WizardRows.SetError(this, PasswordRow, field == CrmWizardField.Password);
    }

    /// <summary>The page's control of <paramref name="field"/>, if it has one.</summary>
    public Control? ControlOf(CrmWizardField field) => field switch
    {
        CrmWizardField.User => UserBox,
        CrmWizardField.Password => PasswordBox,
        _ => null,
    };

    private void Apply()
    {
        ServiceUrl.Text = wizard.ServiceUrl;
        NameBox.IsEnabled = !busy;
        UserBox.IsEnabled = !busy;
        PasswordBox.IsEnabled = !busy;
        FollowBox.IsEnabled = !busy;
        Bar.Button.IsEnabled = !busy;
    }

    // What the rows hold now, to the controller.
    private void Sync()
    {
        if (applying)
        {
            return;
        }
        wizard.SetName(NameBox.Text);
        wizard.SetCredentials(UserBox.Text, PasswordBox.Password);
    }

    private void Next()
    {
        Sync();
        wizard.SetFollowThreads(FollowBox.IsChecked == true);
        wizard.Next();
    }

    private void OnNameChanged(object sender, TextChangedEventArgs e)
    {
        if (!applying)
        {
            wizard.SetName(NameBox.Text);
        }
    }

    private void OnUserChanged(object sender, TextChangedEventArgs e) => Sync();

    private void OnPasswordChanged(object sender, RoutedEventArgs e) => Sync();

    private void OnFollowClick(object sender, RoutedEventArgs e) => wizard.SetFollowThreads(FollowBox.IsChecked == true);

    // Enter in the name goes to the user name, in the user name to the password.
    private void OnFieldKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key != VirtualKey.Enter)
        {
            return;
        }
        e.Handled = true;
        Sync();
        (ReferenceEquals(sender, NameBox) ? (Control)UserBox : PasswordBox).Focus(FocusState.Keyboard);
    }

    // Enter in the password is the page's button.
    private void OnPasswordKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key != VirtualKey.Enter)
        {
            return;
        }
        e.Handled = true;
        if (Bar.Button.IsEnabled)
        {
            Next();
        }
    }
}
