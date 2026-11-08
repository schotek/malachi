// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/AccountRowCell.swift
// (apply, setRowEnabled, setGroupEnabled); GTK:
// ui/internal/window/accounts_page.go (accountRow and its apply). The view
// model of one row of the Accounts or the Integrations page's list: Core's AccountRow applied
// in place (KeyedListSync's view overload, docs/windows-port.md §7.5), so
// that the list keeps the row's container, its focus and its selection
// across a change of state. Everything shown is plain text. An AI provider
// account's row (kind ai) also has its "API key" capsule after the name,
// the tokens the daemon's assistant used through it today after its
// subtitle (ai_editors.go showUsage: "<subtitle> · %s tokens today", given
// by the Integrations page), and Sign In while the provider refused its key
// (its dialog on Replace Key…).

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
    private string usage;

    /// <summary>The row of <paramref name="row"/>; <paramref name="usage"/> is its usage line ("" for none).</summary>
    public AccountRowView(AccountRow row, bool groupEnabled, string usage = "")
    {
        this.row = row;
        this.groupEnabled = groupEnabled;
        this.usage = usage;
    }

    /// <inheritdoc/>
    public event PropertyChangedEventHandler? PropertyChanged;

    /// <summary>The row's account.</summary>
    public AccountId Id => row.Id;

    /// <summary>The name, or the address when unnamed.</summary>
    public string Title => row.Title;

    /// <summary>
    /// The address, or the host of a Jira site or a CalDAV server, or an AI
    /// provider account's provider and model, with its usage line after it.
    /// </summary>
    public string Subtitle => usage.Length == 0 ? row.Subtitle : row.Subtitle.Length == 0 ? usage : row.Subtitle + " · " + usage;

    /// <summary>The capsule after the name (an AI provider account's "API key"); "" for none.</summary>
    public string Badge => row.Badge;

    /// <summary>The capsule is shown.</summary>
    public Visibility BadgeVisibility => row.Badge.Length > 0 ? Visibility.Visible : Visibility.Collapsed;

    /// <summary>The status beside the switch; its tooltip shows it whole.</summary>
    public string Status => row.Status;

    /// <summary>The status is shown (not for the idle state).</summary>
    public Visibility StatusVisibility => row.Status.Length > 0 ? Visibility.Visible : Visibility.Collapsed;

    /// <summary>
    /// "Sign In…" is offered: an account of the browser sign-in that needs
    /// it, or an AI provider account whose key the provider refused.
    /// </summary>
    public Visibility SignInVisibility => OffersSignIn(row) ? Visibility.Visible : Visibility.Collapsed;

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

    /// <summary>
    /// Shows <paramref name="next"/>, the group's sensitivity and the usage
    /// line <paramref name="nextUsage"/>, raising only what changed.
    /// </summary>
    public void Update(AccountRow next, bool group, string nextUsage = "")
    {
        var old = row;
        var oldGroup = groupEnabled;
        var oldUsage = usage;
        row = next;
        groupEnabled = group;
        usage = nextUsage;
        Applying = true;
        try
        {
            if (old.Title != next.Title)
            {
                Changed(nameof(Title));
            }
            if (old.Subtitle != next.Subtitle || oldUsage != nextUsage)
            {
                Changed(nameof(Subtitle));
            }
            if (old.Badge != next.Badge)
            {
                Changed(nameof(Badge));
                Changed(nameof(BadgeVisibility));
            }
            if (old.Status != next.Status)
            {
                Changed(nameof(Status));
                Changed(nameof(StatusVisibility));
            }
            if (OffersSignIn(old) != OffersSignIn(next))
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

    private static bool OffersSignIn(AccountRow r) =>
        r.OffersSignIn || (r.Editor == Integrations.Editor.Ai && r.Account.State.Status == SyncStatus.AuthRequired);

    private void Changed([CallerMemberName] string? name = null) => PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(name));
}
