// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/AccountWizard/Jira/JiraSpacesPageController.swift
// (JiraSpaceCellView.apply); GTK: ui/internal/accountwizard/jira.go
// (jiraSpaceRow, newSpaceRow, showSpaces). The view model of one space on
// the assistant's spaces page: its check box titled "KEY – Name" (the site's
// text, cleaned by Jira.SpaceTitle, plain) and the estimate of its issues,
// applied in place when the same spaces come again with new estimates, so
// the list keeps its scroll position.

using System.ComponentModel;
using System.Runtime.CompilerServices;
using Malachi.Core.IssueTrackers;
using Microsoft.UI.Xaml;

namespace Malachi.App.Wizard;

/// <summary>One space of the Jira assistant's list.</summary>
public sealed partial class JiraSpaceItem : INotifyPropertyChanged
{
    private JiraSpaceRow row;
    private bool isChecked;
    private bool isEnabled = true;

    /// <summary>The item of <paramref name="row"/>.</summary>
    public JiraSpaceItem(JiraSpaceRow row, bool isChecked)
    {
        this.row = row;
        this.isChecked = isChecked;
    }

    /// <inheritdoc/>
    public event PropertyChangedEventHandler? PropertyChanged;

    /// <summary>The space's id.</summary>
    public string Id => row.Id;

    /// <summary>"KEY – Name".</summary>
    public string Title => row.Title;

    /// <summary>The estimate of its issues in the offline window ("" when unknown).</summary>
    public string Count => row.Count;

    /// <summary>The estimate is shown.</summary>
    public Visibility CountVisibility => row.Count.Length > 0 ? Visibility.Visible : Visibility.Collapsed;

    /// <summary>The check box.</summary>
    public bool IsChecked
    {
        get => isChecked;
        set
        {
            if (isChecked != value)
            {
                isChecked = value;
                Changed();
            }
        }
    }

    /// <summary>The check box takes a click (no call under way).</summary>
    public bool IsEnabled
    {
        get => isEnabled;
        set
        {
            if (isEnabled != value)
            {
                isEnabled = value;
                Changed();
            }
        }
    }

    /// <summary>Shows <paramref name="next"/> (the same space), raising only what changed.</summary>
    public void Update(JiraSpaceRow next, bool selected)
    {
        var old = row;
        row = next;
        if (old.Title != next.Title)
        {
            Changed(nameof(Title));
        }
        if (old.Count != next.Count)
        {
            Changed(nameof(Count));
            Changed(nameof(CountVisibility));
        }
        IsChecked = selected;
    }

    private void Changed([CallerMemberName] string? name = null) => PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(name));
}
