// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/RuleRowCell.swift (apply);
// GTK: ui/internal/window/rules_page.go (ruleRow and its apply). The view
// model of one row of the Rules page's list: Core's RuleRow applied in
// place (KeyedListSync's view overload, docs/windows-port.md §7.5), so that
// the list keeps the row's container, its focus and its selection across a
// change. The name, the conditions, the actions and the problem may come
// from mail (a prefilled sender, a list id) and are shown as plain text.

using System.ComponentModel;
using System.Runtime.CompilerServices;
using Malachi.Core.Rules;
using Microsoft.UI.Xaml;

namespace Malachi.App.Preferences;

/// <summary>One rule's row in the Rules page.</summary>
public sealed class RuleRowView : INotifyPropertyChanged
{
    private RuleRow row;

    /// <summary>The row of <paramref name="row"/>.</summary>
    public RuleRowView(RuleRow row) => this.row = row;

    /// <inheritdoc/>
    public event PropertyChangedEventHandler? PropertyChanged;

    /// <summary>The rule's id.</summary>
    public string Id => row.Id;

    /// <summary>The rule's name.</summary>
    public string Title => row.Title;

    /// <summary>The conditions in one line.</summary>
    public string Conditions => row.Conditions;

    /// <summary>The actions in one line.</summary>
    public string Actions => row.Actions;

    /// <summary>The account the rule is for, or All Mail Accounts.</summary>
    public string Scope => row.Scope;

    /// <summary>How often the rule acted and when last.</summary>
    public string Status => row.Status;

    /// <summary>Why the daemon turned the rule off, "" when it did not.</summary>
    public string Problem => row.Problem;

    /// <summary>The warning is shown.</summary>
    public Visibility ProblemVisibility => row.Problem.Length > 0 ? Visibility.Visible : Visibility.Collapsed;

    /// <summary>The rule sends mail: the row shows the sending icon.</summary>
    public Visibility OutgoingVisibility => row.Outgoing ? Visibility.Visible : Visibility.Collapsed;

    /// <summary>The sending icon's tooltip and name.</summary>
    public string OutgoingLabel => RulesText.OutgoingTitle;

    /// <summary>What the rule's switch shows.</summary>
    public bool IsOn => row.Enabled;

    /// <summary>
    /// Set while the row applies a change from the page, so that the
    /// switch's Toggled of that change is not taken for a click.
    /// </summary>
    public bool Applying { get; private set; }

    /// <summary>Shows <paramref name="next"/>, raising only what changed.</summary>
    public void Update(RuleRow next)
    {
        var old = row;
        row = next;
        Applying = true;
        try
        {
            if (old.Title != next.Title)
            {
                Changed(nameof(Title));
            }
            if (old.Conditions != next.Conditions)
            {
                Changed(nameof(Conditions));
            }
            if (old.Actions != next.Actions)
            {
                Changed(nameof(Actions));
            }
            if (old.Scope != next.Scope)
            {
                Changed(nameof(Scope));
            }
            if (old.Status != next.Status)
            {
                Changed(nameof(Status));
            }
            if (old.Problem != next.Problem)
            {
                Changed(nameof(Problem));
                Changed(nameof(ProblemVisibility));
            }
            if (old.Outgoing != next.Outgoing)
            {
                Changed(nameof(OutgoingVisibility));
            }
            if (old.Enabled != next.Enabled)
            {
                Changed(nameof(IsOn));
            }
        }
        finally
        {
            Applying = false;
        }
    }

    private void Changed([CallerMemberName] string? name = null) => PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(name));
}
