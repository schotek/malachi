// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/AccountWizard/Jira/JiraSpacesPageController.swift
// (show, showProblem, showEmail, setBusy, showProblems, applyRows,
// applyEnabled, the controls' actions); GTK: ui/internal/accountwizard/jira.go
// (showSpaces, newSpaceRow, applySpaces, showEmailField, the spaces page's
// handlers). The same spaces again (new estimates for another offline
// window) are updated in place, so the list keeps its scroll position;
// others replace the items. A check box's click goes to the controller
// (JiraWizardController.SetSpace), which says why the account cannot be
// added yet; Return in the address is Add Account.

using System.Collections.Generic;
using System.Collections.ObjectModel;
using System.Linq;
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

/// <summary>The Jira assistant's last page: the spaces and the options.</summary>
public sealed partial class JiraSpacesPage : UserControl
{
    private readonly JiraWizardController wizard;
    private readonly ObservableCollection<JiraSpaceItem> items = [];
    private string problem = "";
    private bool busy;

    // Set while the controls are filled from the controller: not the user's.
    private bool applying;

    /// <summary>The page of <paramref name="wizard"/>.</summary>
    public JiraSpacesPage(JiraWizardController wizard)
    {
        this.wizard = wizard;
        InitializeComponent();
        var texts = wizard.Texts;
        Description.Text = texts.SpacesDescription;
        NoSpaces.Text = texts.NoSpaces;
        SpacesList.ItemsSource = items;
        AutomationProperties.SetName(SpacesList, wizard.PageTitle(JiraWizardPage.Spaces));

        applying = true;
        OfflineRow.Header = texts.KeepOffline;
        OfflineRow.Description = texts.KeepOfflineSubtitle;
        AutomationProperties.SetName(OfflineBox, texts.KeepOffline);
        foreach (var label in JiraWizardController.OfflineLabels)
        {
            OfflineBox.Items.Add(label);
        }
        OfflineBox.SelectedIndex = wizard.OfflineIndex;
        OnlyMineRow.Header = texts.OnlyMine;
        OnlyMineRow.Description = texts.OnlyMineSubtitle;
        AutomationProperties.SetName(OnlyMineSwitch, texts.OnlyMine);
        OnlyMineSwitch.IsOn = wizard.OnlyMine;
        EmailRow.Header = JiraWizardController.EmailLabel;
        AutomationProperties.SetName(EmailBox, JiraWizardController.EmailLabel);
        applying = false;

        Bar.SetLabel(wizard.NextLabel(JiraWizardPage.Spaces), L10n.T("_Add Account"));
        Bar.Button.Click += (_, _) => Next();
        ApplyRows();
    }

    /// <summary>The address when it is asked for, else nothing in particular.</summary>
    public Control? InitialFocus => EmailRow.Visibility == Visibility.Visible ? EmailBox : null;

    /// <summary>onSpaces: the list, and which spaces are chosen.</summary>
    public void Show(IReadOnlyList<JiraSpaceRow> rows, IReadOnlySet<string> selected)
    {
        var same = rows.Count == items.Count && rows.Select(r => r.Id).SequenceEqual(items.Select(i => i.Id));
        if (same)
        {
            for (var i = 0; i < rows.Count; i++)
            {
                items[i].Update(rows[i], selected.Contains(rows[i].Id));
            }
        }
        else
        {
            items.Clear();
            foreach (var r in rows)
            {
                items.Add(new JiraSpaceItem(r, selected.Contains(r.Id)) { IsEnabled = !busy });
            }
        }
        ApplyRows();
    }

    /// <summary>onSpacesProblem: why the account cannot be added yet ("" when it can).</summary>
    public void ShowProblem(string text)
    {
        problem = text;
        Problem.Text = text;
        Problem.Visibility = text.Length > 0 && items.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
        ApplyEnabled();
    }

    /// <summary>onEmailField: the account's address row, shown or hidden, and its text.</summary>
    public void ShowEmail(bool shown, string email)
    {
        if (EmailBox.Text != email)
        {
            applying = true;
            EmailBox.Text = email;
            applying = false;
        }
        EmailRow.Visibility = shown ? Visibility.Visible : Visibility.Collapsed;
    }

    /// <summary>A call runs (its progress text) or none (null).</summary>
    public void SetBusy(string? progress)
    {
        busy = progress is not null;
        Bar.ShowProgress(progress);
        foreach (var item in items)
        {
            item.IsEnabled = !busy;
        }
        ApplyEnabled();
    }

    /// <summary>The page's banner; null hides it.</summary>
    public void ShowBanner(string? text)
    {
        Banner.Message = text ?? "";
        Banner.IsOpen = text is not null;
    }

    /// <summary>Flags the address when <paramref name="fields"/> has it.</summary>
    public void ShowProblems(IReadOnlySet<Field> fields) =>
        WizardRows.SetError(this, EmailRow, fields.Contains(Field.Email));

    /// <summary>The page's control of <paramref name="field"/>, if it has one.</summary>
    public Control? ControlOf(Field field) => field == Field.Email ? EmailBox : null;

    private void ApplyRows()
    {
        SpacesList.Visibility = items.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
        NoSpaces.Visibility = items.Count > 0 ? Visibility.Collapsed : Visibility.Visible;
        ShowProblem(problem);
    }

    private void ApplyEnabled()
    {
        OfflineBox.IsEnabled = !busy;
        OnlyMineSwitch.IsEnabled = !busy;
        EmailBox.IsEnabled = !busy;
        Bar.Button.IsEnabled = !busy && problem.Length == 0 && items.Count > 0;
    }

    private void Next()
    {
        wizard.SetEmail(EmailBox.Text);
        wizard.Next();
    }

    private void OnSpaceClick(object sender, RoutedEventArgs e)
    {
        if (sender is CheckBox { DataContext: JiraSpaceItem item } box)
        {
            var on = box.IsChecked == true;
            item.IsChecked = on;
            wizard.SetSpace(item.Id, on);
        }
    }

    private void OnOfflineChanged(object sender, SelectionChangedEventArgs e)
    {
        if (!applying && OfflineBox.SelectedIndex >= 0)
        {
            wizard.SetOfflineIndex(OfflineBox.SelectedIndex);
        }
    }

    private void OnOnlyMineToggled(object sender, RoutedEventArgs e)
    {
        if (!applying)
        {
            wizard.SetOnlyMine(OnlyMineSwitch.IsOn);
        }
    }

    private void OnEmailChanged(object sender, TextChangedEventArgs e)
    {
        if (!applying)
        {
            wizard.SetEmail(EmailBox.Text);
        }
    }

    private void OnEmailKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key != VirtualKey.Enter)
        {
            return;
        }
        // entry-activated of email_row: the page's button, whose rules are the flow's.
        e.Handled = true;
        Next();
    }
}
