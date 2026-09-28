// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/AccountWizard/TestingPageController.swift
// (show, apply, showButtons, the buttons); GTK:
// ui/internal/accountwizard/wizard.go (runTest, showResults, showButtons,
// hideButtons, onEdit, onAdd) and trust.go (the rows' Trust
// Certificate…). The page shows WizardController.TestingView: the spinner
// while a call runs, the results with the buttons the outcome allows
// afterwards. The results' texts come from the daemon and the controller
// and are plain text. The rows' icons take the colours the macOS client
// gives them (success, critical, the dim default for a warning or an
// unknown state); the big icon stays dim, as an Adw.StatusPage's.

using CommunityToolkit.WinUI.Controls;
using Malachi.App.Localization;
using Malachi.App.Resources;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.Wizard;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Wizard;

/// <summary>The wizard's Connection Test page.</summary>
public sealed partial class TestingPage : UserControl
{
    private readonly WizardController wizard;

    /// <summary>The page of <paramref name="wizard"/>.</summary>
    public TestingPage(WizardController wizard)
    {
        this.wizard = wizard;
        InitializeComponent();
        wizard.TestingChanged += (_, view) => Show(view);
        ShowLabels();
    }

    /// <summary>The button that takes the focus when the results come up: the suggested one.</summary>
    public Control? InitialFocus => AddButton.Visibility == Visibility.Visible ? AddButton
        : RetryButton.Visibility == Visibility.Visible ? RetryButton
        : EditButton.Visibility == Visibility.Visible ? EditButton : null;

    private void ShowLabels()
    {
        MnemonicLabel.Apply(EditButton, WizardRows.WithMnemonic(wizard.EditLabel, L10n.T("_Edit Servers"), L10n.T("_Sign In Again")));
        MnemonicLabel.Apply(AddAnywayButton, WizardRows.WithMnemonic(wizard.AddAnywayLabel, L10n.T("Add _Anyway"), L10n.T("Save _Anyway")));
        MnemonicLabel.Apply(AddButton, WizardRows.WithMnemonic(wizard.AddLabel, L10n.T("_Add Account"), L10n.T("_Save")));
    }

    private void Show(WizardController.TestingView view)
    {
        switch (view)
        {
            case WizardController.TestingView.Progress progress:
                ProgressPage.Title = progress.Title;
                ProgressPage.Spinning = true;
                ProgressPage.Visibility = Visibility.Visible;
                ResultsPage.Visibility = Visibility.Collapsed;
                ShowButtons(WizardController.WizardButtons.None);
                break;
            case WizardController.TestingView.Results results:
                var v = results.View;
                ResultsPage.IconName = v.Icon;
                ResultsPage.Title = v.Title;
                ResultsPage.Description = v.Description ?? "";
                Apply(v.Imap, ImapResultRow, ImapResultIcon, ImapResultText);
                Apply(v.Smtp, SmtpResultRow, SmtpResultIcon, SmtpResultText);
                Apply(v.Graph, GraphResultRow, GraphResultIcon, GraphResultText);
                ImapTrustButton.Visibility = v.Imap?.Trust == true ? Visibility.Visible : Visibility.Collapsed;
                SmtpTrustButton.Visibility = v.Smtp?.Trust == true ? Visibility.Visible : Visibility.Collapsed;
                // Edit Servers, or Sign In Again for a browser sign-in.
                ShowLabels();
                ShowButtons(v.Buttons);
                ProgressPage.Spinning = false;
                ProgressPage.Visibility = Visibility.Collapsed;
                ResultsPage.Visibility = Visibility.Visible;
                break;
        }
    }

    private static void Apply(WizardController.EndpointRow? row, SettingsCard card, FontIcon icon, TextBlock text)
    {
        card.Visibility = row is null ? Visibility.Collapsed : Visibility.Visible;
        if (row is null)
        {
            return;
        }
        text.Text = row.Text;
        icon.FontFamily = Icons.SymbolFont;
        icon.Glyph = Icons.Glyph(row.Icon);
        var brush = row.Icon switch
        {
            "emblem-ok-symbolic" => "SystemFillColorSuccessBrush",
            "dialog-error-symbolic" => "SystemFillColorCriticalBrush",
            _ => "TextFillColorSecondaryBrush",
        };
        if (Application.Current.Resources.TryGetValue(brush, out var b) && b is Brush fill)
        {
            icon.Foreground = fill;
        }
    }

    private void ShowButtons(WizardController.WizardButtons b)
    {
        EditButton.Visibility = b.Edit ? Visibility.Visible : Visibility.Collapsed;
        RetryButton.Visibility = b.Retry ? Visibility.Visible : Visibility.Collapsed;
        AddAnywayButton.Visibility = b.AddAnyway ? Visibility.Visible : Visibility.Collapsed;
        AddButton.Visibility = b.Add ? Visibility.Visible : Visibility.Collapsed;
    }

    private void OnTrustClick(object sender, RoutedEventArgs e) =>
        wizard.TrustCertificate(ReferenceEquals(sender, ImapTrustButton) ? Endpoint.Imap : Endpoint.Smtp);

    private void OnEditClick(object sender, RoutedEventArgs e) => wizard.Edit();

    private void OnRetryClick(object sender, RoutedEventArgs e) => wizard.Retry();

    private void OnAddAnywayClick(object sender, RoutedEventArgs e) => wizard.AddAnyway();

    private void OnAddClick(object sender, RoutedEventArgs e) => wizard.Add();
}
