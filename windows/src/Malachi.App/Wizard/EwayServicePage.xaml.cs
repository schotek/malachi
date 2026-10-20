// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/AccountWizard/EWay/EWayWizardWindowController.swift
// (EWayServicePageController: showCheck, setBusy, showProblem,
// handleReturn); GTK: ui/internal/accountwizard/eway.go (written in T7; the
// flow is eway_flow.go). Every keystroke goes to the controller
// (EwayWizardController.SetService), which checks the address (http is
// refused here, before anything is asked) and forgets the instance found
// for another one; Return in the field and Next look the address up.

using Malachi.Core.Controllers;
using Malachi.Core.Crm;
using Malachi.Core.I18n;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Windows.System;

namespace Malachi.App.Wizard;

/// <summary>The eWay-CRM assistant's first page: the web service address.</summary>
public sealed partial class EwayServicePage : UserControl
{
    private readonly EwayWizardController wizard;
    private bool ok;
    private string problem = "";
    private bool busy;

    /// <summary>The page of <paramref name="wizard"/>.</summary>
    public EwayServicePage(EwayWizardController wizard)
    {
        this.wizard = wizard;
        InitializeComponent();
        var texts = wizard.Texts;
        ServiceRow.Header = texts.ServiceLabel;
        AutomationProperties.SetName(ServiceBox, texts.ServiceLabel);
        AutomationProperties.SetHelpText(ServiceBox, texts.ServiceHint);
        Hint.Text = texts.ServiceHint;
        ServiceBox.Text = wizard.ServiceInput;
        Bar.SetLabel(wizard.NextLabel(CrmWizardPage.Service), L10n.T("_Next"));
        AutomationProperties.SetAutomationId(Bar.Button, "EwayNext");
        Bar.Button.Click += (_, _) => Next();
        Apply();
    }

    /// <summary>The field the page focuses when it appears.</summary>
    public Control InitialFocus => ServiceBox;

    /// <summary>onServiceCheck: whether the address can be looked up, and what is wrong with it.</summary>
    public void ShowCheck(bool ok, string problem)
    {
        this.ok = ok;
        this.problem = problem;
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

    /// <summary>Flags the address field when <paramref name="field"/> is it.</summary>
    public void ShowProblem(CrmWizardField field) => WizardRows.SetError(this, ServiceRow, field == CrmWizardField.Service);

    /// <summary>The page's control of <paramref name="field"/>, if it has one.</summary>
    public Control? ControlOf(CrmWizardField field) => field == CrmWizardField.Service ? ServiceBox : null;

    private void Apply()
    {
        Problem.Text = problem;
        Problem.Visibility = problem.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        ServiceBox.IsEnabled = !busy;
        Bar.Button.IsEnabled = ok && !busy;
    }

    private void OnServiceChanged(object sender, TextChangedEventArgs e) => wizard.SetService(ServiceBox.Text);

    private void OnServiceKeyDown(object sender, KeyRoutedEventArgs e)
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
        wizard.SetService(ServiceBox.Text);
        wizard.Next();
    }
}
