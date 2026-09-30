// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/JiraAccount/JiraStatusPickerView.swift;
// GTK: ui/internal/jiraaccount/statuses.go (statusPicker: apply,
// sameChoices, rebuild). The statuses of the site by category
// (Jira.StatusGroups), each category under its name in a pill of its colour
// (IssuePill, as the statuses of issues are shown), each status name a
// check box; and why the choice cannot be saved (Jira.StatusesProblem). The
// check boxes are built again only when the statuses to choose from
// changed (the listing arrived), otherwise only their ticks follow. The
// names come from the site and are plain text.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Controls;
using Malachi.App.Reader;
using Malachi.Core.IssueTrackers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Preferences;

/// <summary>The picker of a Jira account's closed statuses.</summary>
public sealed partial class JiraStatusPicker : UserControl
{
    private readonly List<CheckBox> checks = [];
    private IReadOnlyList<JiraStatusGroup> groups = [];
    private List<JiraStatusChoice> choices = [];

    // The check boxes being set from the controller.
    private bool applying;

    /// <summary>A picker titled <paramref name="title"/>, explained by <paramref name="subtitle"/>.</summary>
    public JiraStatusPicker(string title, string subtitle)
    {
        ArgumentNullException.ThrowIfNull(title);
        ArgumentNullException.ThrowIfNull(subtitle);
        InitializeComponent();
        TitleLabel.Text = title;
        SubtitleLabel.Text = subtitle;
        SubtitleLabel.Visibility = subtitle.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        AutomationProperties.SetName(Body, title);
    }

    /// <summary>A check box the user changed: its choice and whether it is ticked now.</summary>
    public Action<JiraStatusChoice, bool>? Toggled { get; set; }

    /// <summary>Shows the picker as the controller holds it.</summary>
    public void Apply(IReadOnlyList<JiraStatusGroup> groups, string problem)
    {
        ArgumentNullException.ThrowIfNull(groups);
        ArgumentNullException.ThrowIfNull(problem);
        if (!SameChoices(groups, this.groups))
        {
            this.groups = groups;
            Rebuild();
        }
        else
        {
            this.groups = groups;
            choices = [.. groups.SelectMany(g => g.Choices)];
            applying = true;
            for (var i = 0; i < checks.Count; i++)
            {
                checks[i].IsChecked = choices[i].Selected;
            }
            applying = false;
        }
        ProblemText.Text = problem;
        ProblemText.Visibility = problem.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    // sameChoices: the same check boxes under the same titles; only what is
    // ticked may differ.
    private static bool SameChoices(IReadOnlyList<JiraStatusGroup> a, IReadOnlyList<JiraStatusGroup> b) =>
        a.Count == b.Count && a.Zip(b).All(p =>
            p.First.Category == p.Second.Category && p.First.Title == p.Second.Title && p.First.Style == p.Second.Style
            && p.First.Choices.Count == p.Second.Choices.Count
            && p.First.Choices.Zip(p.Second.Choices).All(c => c.First.Name == c.Second.Name && c.First.Ids.SequenceEqual(c.Second.Ids)));

    private void Rebuild()
    {
        Body.Children.Clear();
        checks.Clear();
        choices = [];
        foreach (var g in groups)
        {
            var pill = new IssuePill { Text = g.Title, StatusStyle = g.Style, HorizontalAlignment = HorizontalAlignment.Left };
            var flow = new WrapBox { ChildSpacing = 14, LineSpacing = 6 };
            foreach (var choice in g.Choices)
            {
                var check = new CheckBox
                {
                    Content = new TextBlock { Text = choice.Name, MaxWidth = 220, TextTrimming = TextTrimming.CharacterEllipsis, MaxLines = 1 },
                    IsChecked = choice.Selected,
                    MinWidth = 0,
                };
                ToolTipService.SetToolTip(check, choice.Name);
                AutomationProperties.SetName(check, choice.Name);
                var i = choices.Count;
                check.Click += (_, _) =>
                {
                    if (!applying)
                    {
                        Toggled?.Invoke(choices[i], check.IsChecked == true);
                    }
                };
                choices.Add(choice);
                checks.Add(check);
                flow.Children.Add(check);
            }
            var section = new StackPanel { Spacing = 6 };
            section.Children.Add(pill);
            section.Children.Add(flow);
            Body.Children.Add(section);
        }
        Body.Visibility = groups.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
    }
}
