// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/AccountWizard/IdentityPageController.swift
// (setBusy, showProblems, focus, setIdentity, sync, handleReturn); GTK:
// ui/internal/accountwizard/wizard.go (wire: the identity rows'
// entry-activated and changed handlers, setBusy, askPassword,
// requirePassword). Every keystroke goes to the controller
// (WizardController.SetIdentity), which clears the flags and the banner
// when the address or the password changes; Return in the address moves to
// the password (or is Next when the password row is hidden), Return in the
// password is Next, Return in the name does nothing. The password is never
// logged: it goes from the PasswordBox to the controller and no further.

using System.ComponentModel;
using Malachi.App.Localization;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Wizard;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Windows.System;

namespace Malachi.App.Wizard;

/// <summary>The wizard's first page: the name, the address and the password.</summary>
public sealed partial class IdentityPage : UserControl
{
    private readonly WizardController wizard;
    private bool busy;

    // Set while the fields are filled from the controller: their change is
    // not a keystroke. (A TextBox raises TextChanged later, with the text
    // the controller holds already, which changes nothing there.)
    private bool applying;

    /// <summary>The page of <paramref name="wizard"/>.</summary>
    public IdentityPage(WizardController wizard)
    {
        this.wizard = wizard;
        InitializeComponent();
        wizard.IdentityProblemsShown += (_, p) => ShowProblems(p.Problems, p.Banner);
        wizard.IdentityChanged += (_, id) => SetIdentity(id);
        wizard.BusyChanged += (_, on) => SetBusy(on);
        wizard.PropertyChanged += OnWizardChanged;
        ShowLabels();
    }

    /// <summary>The field the dialog focuses first (<c>focus-widget: email_row</c>).</summary>
    public Control InitialFocus => EmailBox;

    /// <summary>Moves the keyboard focus to <paramref name="field"/>.</summary>
    public void FocusField(WizardController.IdentityField field)
    {
        Control target = field == WizardController.IdentityField.Password && wizard.PasswordVisible ? PasswordField : EmailBox;
        target.Focus(FocusState.Programmatic);
    }

    private void ShowLabels()
    {
        PasswordRow.Header = wizard.PasswordTitle;
        AutomationProperties.SetName(PasswordField, wizard.PasswordTitle);
        PasswordRow.Visibility = wizard.PasswordVisible ? Visibility.Visible : Visibility.Collapsed;
        MnemonicLabel.Apply(NextButton, WizardRows.WithMnemonic(wizard.NextLabel, L10n.T("_Next"), L10n.T("_Test Connection")));
        SetBusy(busy);
    }

    private void OnWizardChanged(object? sender, PropertyChangedEventArgs e)
    {
        if (e.PropertyName is nameof(WizardController.NextLabel) or nameof(WizardController.PasswordVisible) or nameof(WizardController.EmailEditable))
        {
            ShowLabels();
        }
    }

    private void SetBusy(bool on)
    {
        busy = on;
        NameBox.IsEnabled = !on;
        EmailBox.IsEnabled = !on && wizard.EmailEditable;
        PasswordField.IsEnabled = !on;
        NextButton.IsEnabled = !on;
    }

    private void ShowProblems(IdentityProblems p, string? banner)
    {
        WizardRows.SetError(EmailRow, p.Email);
        WizardRows.SetError(PasswordRow, p.Password);
        Banner.Message = banner ?? "";
        Banner.IsOpen = banner is not null;
    }

    private void SetIdentity(Identity id)
    {
        applying = true;
        if (NameBox.Text != id.DisplayName)
        {
            NameBox.Text = id.DisplayName;
        }
        if (EmailBox.Text != id.Email)
        {
            EmailBox.Text = id.Email;
        }
        if (PasswordField.Password != id.Password)
        {
            PasswordField.Password = id.Password;
        }
        applying = false;
    }

    // What the rows hold now, to the controller.
    private void Sync()
    {
        if (!applying)
        {
            wizard.SetIdentity(NameBox.Text, EmailBox.Text, PasswordField.Password);
        }
    }

    private void OnTextChanged(object sender, TextChangedEventArgs e) => Sync();

    private void OnPasswordChanged(object sender, RoutedEventArgs e) => Sync();

    private void OnNextClick(object sender, RoutedEventArgs e)
    {
        Sync();
        wizard.Next();
    }

    // entry-activated of email_row: the password next, or Next itself when
    // the account signs in without one.
    private void OnEmailKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key != VirtualKey.Enter)
        {
            return;
        }
        e.Handled = true;
        Sync();
        if (wizard.PasswordVisible)
        {
            PasswordField.Focus(FocusState.Keyboard);
        }
        else if (NextButton.IsEnabled)
        {
            wizard.Next();
        }
    }

    // entry-activated of password_row: Next.
    private void OnPasswordKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key != VirtualKey.Enter)
        {
            return;
        }
        e.Handled = true;
        Sync();
        if (NextButton.IsEnabled)
        {
            wizard.Next();
        }
    }
}
