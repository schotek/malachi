// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/AccountWizard/SignInHintPageController.swift
// (show, browserClicked); GTK: ui/internal/accountwizard/linked.go
// (showGOAHint, onGOABrowser). The hint's text and whether the browser is
// offered come from the controller (WizardController.GoaHintShown).

using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Wizard;

/// <summary>The wizard's page for an address whose sign-in belongs to GNOME Online Accounts.</summary>
public sealed partial class SignInHintPage : UserControl
{
    private readonly WizardController wizard;

    /// <summary>The page of <paramref name="wizard"/>.</summary>
    public SignInHintPage(WizardController wizard)
    {
        this.wizard = wizard;
        InitializeComponent();
        wizard.GoaHintShown += (_, hint) =>
        {
            GoaHint.Description = hint.Text;
            BrowserButton.Visibility = hint.Browser ? Visibility.Visible : Visibility.Collapsed;
        };
    }

    private void OnBrowserClick(object sender, RoutedEventArgs e) => wizard.UseBrowser();
}
