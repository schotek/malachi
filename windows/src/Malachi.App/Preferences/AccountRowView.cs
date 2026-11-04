// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/AccountRowCell.swift
// (apply, setRowEnabled, setGroupEnabled); GTK:
// ui/internal/window/accounts_page.go (accountRow and its apply). The view
// model of one row of the Accounts or the Integrations page's list: Core's AccountRow applied
// in place (KeyedListSync's view overload, docs/windows-port.md §7.5), so
// that the list keeps the row's container, its focus and its selection
// across a change of state. Everything shown is plain text.

using System.ComponentModel;
using System.Runtime.CompilerServices;
using Malachi.App.Resources;
using Malachi.Core.Api;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Integrations = Malachi.Core.Model.Integrations;

namespace Malachi.App.Preferences;

/// <summary>One account's row in the Accounts or the Integrations page.</summary>
public sealed partial class AccountRowView : INotifyPropertyChanged
{
    private AccountRow row;
    private bool groupEnabled;

    /// <summary>The row of <paramref name="row"/>.</summary>
    public AccountRowView(AccountRow row, bool groupEnabled)
    {
        this.row = row;
        this.groupEnabled = groupEnabled;
    }

    /// <inheritdoc/>
    public event PropertyChangedEventHandler? PropertyChanged;

    /// <summary>The row's account.</summary>
    public AccountId Id => row.Id;

    /// <summary>The name, or the address when unnamed.</summary>
    public string Title => row.Title;

    /// <summary>The address, or the host of a Jira site or a CalDAV server.</summary>
    public string Subtitle => row.Subtitle;

    /// <summary>The status beside the switch; its tooltip shows it whole.</summary>
    public string Status => row.Status;

    /// <summary>The status is shown (not for the idle state).</summary>
    public Visibility StatusVisibility => row.Status.Length > 0 ? Visibility.Visible : Visibility.Collapsed;

    /// <summary>"Sign In…" is offered.</summary>
    public Visibility SignInVisibility => row.OffersSignIn ? Visibility.Visible : Visibility.Collapsed;

    /// <summary>What the Enabled switch shows.</summary>
    public bool IsOn => row.Enabled;

    /// <summary>The Enabled switch is shown (not on a CalDAV row, which has no pause).</summary>
    public Visibility SwitchVisibility => row.OffersSwitch ? Visibility.Visible : Visibility.Collapsed;

    /// <summary>What Edit opens.</summary>
    public Integrations.Editor Editor => row.Editor;

    /// <summary>Edit is shown (not for a kind this client does not know).</summary>
    public Visibility EditVisibility => row.Editor == Integrations.Editor.None ? Visibility.Collapsed : Visibility.Visible;

    /// <summary>Edit's tooltip and name: Edit Account, a CalDAV row's Calendar Settings.</summary>
    public string EditLabel => row.EditLabel;

    /// <summary>The row's buttons and switch take input: no call in flight, the group sensitive.</summary>
    public bool IsInteractive => !row.Busy && groupEnabled;

    /// <summary>The provider's glyph (the generic mail icon, U6).</summary>
    public string IconGlyph => Icons.Glyph(row.Icon);

    /// <summary>
    /// Set while the row applies a change from the page, so that the
    /// switch's Toggled of that change is not taken for a click.
    /// </summary>
    public bool Applying { get; private set; }

    /// <summary>Shows <paramref name="next"/> and the group's sensitivity, raising only what changed.</summary>
    public void Update(AccountRow next, bool group)
    {
        var old = row;
        var oldGroup = groupEnabled;
        row = next;
        groupEnabled = group;
        Applying = true;
        try
        {
            if (old.Title != next.Title)
            {
                Changed(nameof(Title));
            }
            if (old.Subtitle != next.Subtitle)
            {
                Changed(nameof(Subtitle));
            }
            if (old.Status != next.Status)
            {
                Changed(nameof(Status));
                Changed(nameof(StatusVisibility));
            }
            if (old.OffersSignIn != next.OffersSignIn)
            {
                Changed(nameof(SignInVisibility));
            }
            if (old.Enabled != next.Enabled)
            {
                Changed(nameof(IsOn));
            }
            if (old.Busy != next.Busy || oldGroup != group)
            {
                Changed(nameof(IsInteractive));
            }
            if (old.Icon != next.Icon)
            {
                Changed(nameof(IconGlyph));
            }
            if (old.OffersSwitch != next.OffersSwitch)
            {
                Changed(nameof(SwitchVisibility));
            }
            if (old.Editor != next.Editor)
            {
                Changed(nameof(Editor));
                Changed(nameof(EditVisibility));
            }
            if (old.EditLabel != next.EditLabel)
            {
                Changed(nameof(EditLabel));
            }
        }
        finally
        {
            Applying = false;
        }
    }

    private void Changed([CallerMemberName] string? name = null) => PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(name));
}
