// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/AccountWizard/Jira/JiraWizardWindowController.swift
// (JiraWizardProgress, jiraWizardLayout's button bar); GTK: jira_wizard.blp,
// the CenterBox at the bottom of each page (site_progress,
// credentials_progress, spaces_progress and the page's button). A page's
// button bar: the call under way at the start (a small spinner and its
// progress text, dim, shortened at its end; hidden while none runs) and the
// page's button, the suggested action, at the end, 12 px margins.

using Malachi.App.Localization;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Wizard;

/// <summary>The button bar of a page of the Jira account assistant.</summary>
public sealed partial class JiraWizardButtonBar : UserControl
{
    private readonly StackPanel progress;
    private readonly ProgressRing spinner;
    private readonly TextBlock progressText;

    /// <summary>A bar with its button and no call under way.</summary>
    public JiraWizardButtonBar()
    {
        IsTabStop = false;
        spinner = new ProgressRing { Width = 16, Height = 16, IsActive = false };
        progressText = new TextBlock
        {
            VerticalAlignment = VerticalAlignment.Center,
            TextTrimming = TextTrimming.CharacterEllipsis,
            TextWrapping = TextWrapping.NoWrap,
            Style = (Style)Application.Current.Resources["DimLabelStyle"],
        };
        AutomationProperties.SetLiveSetting(progressText, Microsoft.UI.Xaml.Automation.Peers.AutomationLiveSetting.Polite);
        progress = new StackPanel
        {
            Orientation = Orientation.Horizontal,
            Spacing = 6,
            VerticalAlignment = VerticalAlignment.Center,
            Visibility = Visibility.Collapsed,
        };
        progress.Children.Add(spinner);
        progress.Children.Add(progressText);
        Button = new Button
        {
            Style = (Style)Application.Current.Resources["AccentButtonStyle"],
            HorizontalAlignment = HorizontalAlignment.Right,
        };
        AutomationProperties.SetAutomationId(Button, "JiraNext");
        var grid = new Grid { Margin = new Thickness(12), ColumnSpacing = 8 };
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        grid.Children.Add(progress);
        Grid.SetColumn(Button, 1);
        grid.Children.Add(Button);
        Content = grid;
    }

    /// <summary>The page's button (Next, Save, Add Account).</summary>
    public Button Button { get; }

    /// <summary>
    /// The button's label, with its access key when <paramref name="label"/>
    /// is one of <paramref name="candidates"/> without its mnemonic
    /// (<see cref="WizardRows.WithMnemonic"/>).
    /// </summary>
    public void SetLabel(string label, params string[] candidates) =>
        MnemonicLabel.Apply(Button, WizardRows.WithMnemonic(label, candidates));

    /// <summary>A call runs (its progress text) or none (null).</summary>
    public void ShowProgress(string? text)
    {
        if (text is null)
        {
            spinner.IsActive = false;
            progress.Visibility = Visibility.Collapsed;
            return;
        }
        progressText.Text = text;
        spinner.IsActive = true;
        progress.Visibility = Visibility.Visible;
    }
}
