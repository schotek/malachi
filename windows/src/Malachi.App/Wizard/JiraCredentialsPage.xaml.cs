// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/AccountWizard/Jira/JiraCredentialsPageController.swift
// (show, apply, applyEnabled, setBusy, showProblems, sync, handleReturn); GTK:
// ui/internal/accountwizard/jira.go (showCredentialPage, applyCredentials,
// the login and token rows' handlers). Every keystroke goes to the
// controller (JiraWizardController.SetCredentials); Return in the address
// moves to the token, Return in the token is Next. The token is never
// logged: it goes from the PasswordBox to the controller and no further.

using System.Collections.Generic;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Windows.System;
using Field = Malachi.Core.Controllers.JiraWizardController.Field;

namespace Malachi.App.Wizard;

/// <summary>The Jira assistant's sign-in page.</summary>
public sealed partial class JiraCredentialsPage : UserControl
{
    private readonly JiraWizardController wizard;
    private JiraCredentialPage fields;
    private bool busy;

    // Set while the address is filled from the controller: not a keystroke.
    private bool applying;

    /// <summary>The page of <paramref name="wizard"/>.</summary>
    public JiraCredentialsPage(JiraWizardController wizard)
    {
        this.wizard = wizard;
        fields = wizard.CredentialPage;
        InitializeComponent();
        Bar.Button.Click += (_, _) => Next();
        Apply();
    }

    /// <summary>The address while it is still to type, the token otherwise.</summary>
    public Control InitialFocus =>
        fields.ShowsLogin && wizard.LoginEditable && LoginBox.Text.Length == 0 ? LoginBox : TokenBox;

    /// <summary>onCredentialPage: the page for the site's deployment.</summary>
    public void Show(JiraCredentialPage page)
    {
        fields = page;
        Apply();
    }

    /// <summary>A call runs (its progress text) or none (null).</summary>
    public void SetBusy(string? progress)
    {
        busy = progress is not null;
        Bar.ShowProgress(progress);
        ApplyEnabled();
    }

    /// <summary>The page's banner; null hides it.</summary>
    public void ShowBanner(string? text)
    {
        Banner.Message = text ?? "";
        Banner.IsOpen = text is not null;
    }

    /// <summary>Flags the address and the token among <paramref name="problems"/>.</summary>
    public void ShowProblems(IReadOnlySet<Field> problems)
    {
        WizardRows.SetError(this, LoginRow, problems.Contains(Field.Login));
        WizardRows.SetError(this, TokenRow, problems.Contains(Field.Token));
    }

    /// <summary>The page's control of <paramref name="field"/>, if it has one.</summary>
    public Control? ControlOf(Field field) => field switch
    {
        Field.Login => LoginBox,
        Field.Token => TokenBox,
        _ => null,
    };

    private void Apply()
    {
        Help.Text = fields.Help;
        Help.Visibility = fields.Help.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        HelpButton.Content = fields.HelpButton;
        HelpButton.Visibility = fields.HelpButton.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        LoginRow.Header = fields.LoginLabel;
        AutomationProperties.SetName(LoginBox, fields.LoginLabel);
        LoginRow.Visibility = fields.ShowsLogin ? Visibility.Visible : Visibility.Collapsed;
        TokenRow.Header = fields.TokenLabel;
        AutomationProperties.SetName(TokenBox, fields.TokenLabel);
        if (LoginBox.Text != wizard.Login)
        {
            applying = true;
            LoginBox.Text = wizard.Login;
            applying = false;
        }
        Bar.SetLabel(wizard.NextLabel(JiraWizardPage.Credentials), L10n.T("_Next"), L10n.T("_Save"));
        ApplyEnabled();
    }

    private void ApplyEnabled()
    {
        LoginBox.IsEnabled = !busy && wizard.LoginEditable;
        TokenBox.IsEnabled = !busy;
        HelpButton.IsEnabled = !busy;
        Bar.Button.IsEnabled = !busy;
    }

    // What the rows hold now, to the controller.
    private void Sync()
    {
        if (!applying)
        {
            wizard.SetCredentials(LoginBox.Text, TokenBox.Password);
        }
    }

    private void Next()
    {
        Sync();
        wizard.Next();
    }

    private void OnHelpClick(object sender, RoutedEventArgs e) => wizard.OpenTokenHelp();

    private void OnLoginChanged(object sender, TextChangedEventArgs e) => Sync();

    private void OnTokenChanged(object sender, RoutedEventArgs e) => Sync();

    // Enter in the address goes to the token.
    private void OnLoginKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key != VirtualKey.Enter)
        {
            return;
        }
        e.Handled = true;
        Sync();
        TokenBox.Focus(FocusState.Keyboard);
    }

    // Enter in the token is Next.
    private void OnTokenKeyDown(object sender, KeyRoutedEventArgs e)
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
