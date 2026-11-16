// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/AccountWizard/OAuthPageController.swift
// (show, apply, setStarting, the four buttons); GTK:
// ui/internal/accountwizard/oauth.go (showOAuthStack, showOAuthPrompt,
// showOAuthUnavailable, the buttons' handlers). The flow is the
// controller's (account.oauthStart, the browser through the app's
// launcher, account.oauthWait until the browser comes back); this page
// shows WizardController.OAuthView and hands the clicks back. Only the
// prompt's button waits for account.oauthStart. The prompt's group of
// services to sync too (showServices, serviceToggled, mailOnlyClicked;
// docs/plans/account-services.md §3.2) shows WizardController.ServicesView:
// a check box per service, rebuilt only when the services change.

using System.Linq;
using Malachi.App.Localization;
using Malachi.Core.AccountServices;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using OAuthTexts = Malachi.Core.Wizard.OAuth;

namespace Malachi.App.Wizard;

/// <summary>The wizard's browser sign-in page.</summary>
public sealed partial class OAuthPage : UserControl
{
    private readonly WizardController wizard;

    /// <summary>The page of <paramref name="wizard"/>.</summary>
    public OAuthPage(WizardController wizard)
    {
        this.wizard = wizard;
        InitializeComponent();
        wizard.OAuthViewChanged += (_, view) => Show(view);
        wizard.OAuthStartingChanged += (_, starting) => SignInButton.IsEnabled = !starting;
        wizard.ServicesChanged += (_, view) => ShowServices(view);
        if (wizard.CurrentOAuthView is { } current)
        {
            Show(current);
        }
        ShowServices(wizard.CurrentServicesView);
    }

    /// <summary>The button that takes the focus when the page comes up.</summary>
    public Control? InitialFocus => WaitingPage.Visibility == Visibility.Visible ? ReopenButton
        : UnavailablePage.Visibility == Visibility.Visible ? (PasswordButton.Visibility == Visibility.Visible ? PasswordButton : null)
        : SignInButton;

    // oauth.go showOAuthStack: one child of the stack.
    private void Show(WizardController.OAuthView view)
    {
        StatusPage visible = PromptPage;
        switch (view)
        {
            case WizardController.OAuthView.Prompt prompt:
                PromptPage.Description = prompt.Description;
                MnemonicLabel.Apply(SignInButton, WizardRows.WithMnemonic(prompt.SignInLabel, OAuthTexts.OAuthSignInLabel(wizard.OAuth?.Name ?? "")));
                break;
            case WizardController.OAuthView.Waiting:
                visible = WaitingPage;
                break;
            case WizardController.OAuthView.Unavailable unavailable:
                visible = UnavailablePage;
                UnavailablePage.Description = unavailable.Description;
                PasswordButton.Visibility = unavailable.PasswordAlternative ? Visibility.Visible : Visibility.Collapsed;
                break;
        }
        foreach (var page in (StatusPage[])[PromptPage, WaitingPage, UnavailablePage])
        {
            page.Visibility = page == visible ? Visibility.Visible : Visibility.Collapsed;
        }
        // A page behind another need not spin.
        WaitingPage.Spinning = visible == WaitingPage;
    }

    // The group of services to sync too (Swift showServices); null hides it.
    private void ShowServices(WizardController.ServicesView? v)
    {
        ServicesGroup.Visibility = v is null ? Visibility.Collapsed : Visibility.Visible;
        if (v is null)
        {
            return;
        }
        ServicesTitle.Text = v.Title;
        ServicesNote.Text = v.Note;
        MailOnlyButton.Content = v.MailOnly ?? "";
        MailOnlyButton.Visibility = v.MailOnly is null ? Visibility.Collapsed : Visibility.Visible;
        var checks = ServicesChecks.Children.OfType<CheckBox>().ToList();
        if (!checks.Select(c => (AccountSync.Service)c.Tag).SequenceEqual(v.Rows.Select(r => r.Service)))
        {
            ServicesChecks.Children.Clear();
            foreach (var row in v.Rows)
            {
                var check = new CheckBox { Tag = row.Service };
                AutomationProperties.SetAutomationId(check, "OAuthSync-" + AccountSync.Name(row.Service));
                check.Click += OnServiceClick;
                ServicesChecks.Children.Add(check);
            }
            checks = [.. ServicesChecks.Children.OfType<CheckBox>()];
        }
        foreach (var (check, row) in checks.Zip(v.Rows))
        {
            check.Content = row.Title;
            check.IsChecked = row.On;
        }
    }

    private void OnServiceClick(object sender, RoutedEventArgs e)
    {
        if (sender is CheckBox { Tag: AccountSync.Service service } check)
        {
            wizard.SetService(service, check.IsChecked == true);
        }
    }

    private void OnMailOnlyClick(object sender, RoutedEventArgs e) => wizard.SignInWithMailOnly();

    private void OnSignInClick(object sender, RoutedEventArgs e) => wizard.SignInWithProvider();

    private void OnReopenClick(object sender, RoutedEventArgs e) => wizard.ReopenBrowser();

    private void OnCancelClick(object sender, RoutedEventArgs e) => wizard.CancelOAuth();

    private void OnPasswordClick(object sender, RoutedEventArgs e) => wizard.UseAppPassword();
}
