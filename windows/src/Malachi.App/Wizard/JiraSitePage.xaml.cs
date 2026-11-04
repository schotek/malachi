// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/AccountWizard/Jira/JiraSitePageController.swift
// (showCheck, showDetected, setBusy, showProblems, apply, handleReturn); GTK:
// ui/internal/accountwizard/jira.go (applySite, the site row's changed and
// entry-activated handlers). Every keystroke goes to the controller
// (JiraWizardController.SetSite), which checks the address and forgets the
// site found for another one; Return in the field and Next look the site up.

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

/// <summary>The Jira assistant's first page: the site's address.</summary>
public sealed partial class JiraSitePage : UserControl
{
    private readonly JiraWizardController wizard;

    // What the field allows (SiteChecked) and what the site is.
    private bool ok;
    private string problem = "";
    private string detected = "";
    private bool busy;

    /// <summary>The page of <paramref name="wizard"/>.</summary>
    public JiraSitePage(JiraWizardController wizard)
    {
        this.wizard = wizard;
        InitializeComponent();
        var texts = wizard.Texts;
        Description.Text = texts.SiteDescription;
        SiteRow.Header = texts.SiteAddress;
        AutomationProperties.SetName(SiteBox, texts.SiteAddress);
        SiteBox.PlaceholderText = Jira.SitePlaceholder;
        SiteBox.Text = wizard.SiteInput;
        Bar.SetLabel(wizard.NextLabel(JiraWizardPage.Site), L10n.T("_Next"));
        Bar.Button.Click += (_, _) => Next();
        Apply();
    }

    /// <summary>The field the page focuses when it appears (<c>focus-widget: site_row</c>).</summary>
    public Control InitialFocus => SiteBox;

    /// <summary>onSiteCheck: whether the address can be looked up, and what is wrong with it.</summary>
    public void ShowCheck(bool ok, string problem)
    {
        this.ok = ok;
        this.problem = problem;
        Apply();
    }

    /// <summary>onDetected: what the site turned out to be ("" while none was found).</summary>
    public void ShowDetected(string text)
    {
        detected = text;
        Apply();
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

    /// <summary>Flags the site field when <paramref name="fields"/> has it.</summary>
    public void ShowProblems(System.Collections.Generic.IReadOnlySet<Field> fields) =>
        WizardRows.SetError(this, SiteRow, fields.Contains(Field.Site));

    /// <summary>The page's control of <paramref name="field"/>, if it has one.</summary>
    public Control? ControlOf(Field field) => field == Field.Site ? SiteBox : null;

    // The text under the field: the problem with the address first, else
    // the site found; Next follows the field.
    private void Apply()
    {
        var text = problem.Length > 0 ? problem : detected;
        Status.Style = (Style)Resources[problem.Length > 0 ? "JiraProblemTextStyle" : "JiraStatusTextStyle"];
        Status.Text = text;
        Status.Visibility = text.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        SiteBox.IsEnabled = !busy;
        Bar.Button.IsEnabled = ok && !busy;
    }

    private void OnSiteChanged(object sender, TextChangedEventArgs e) => wizard.SetSite(SiteBox.Text);

    private void OnSiteKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key != VirtualKey.Enter)
        {
            return;
        }
        e.Handled = true;
        Next();
    }

    private void Next()
    {
        wizard.SetSite(SiteBox.Text);
        wizard.Next();
    }
}
