// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/ComposeHeaderView.swift
// (setCcBccVisible, setAccounts, selectedAccountIndex, the From pop-up's
// action); GTK: ui/internal/compose/compose.go (setCcBccVisible,
// showCcBcc, setAccounts' drop-down, the From row's "selected"
// notification). The rules are Core's (ComposeHeaderRules); the compose
// window reads the fields and decides. The recipient rows are
// RecipientTokenBoxes: an unparsable entry shows as a red badge there, which
// replaces validateRow's red row. ShowsFrom takes the From line away for
// the board's inline reply (ComposeHeaderView(showsFrom:), pane.go).

using System;
using System.Collections.Generic;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Compose;

/// <summary>The card of header fields of a compose window.</summary>
public sealed partial class ComposeHeader : UserControl
{
    // The window's own changes of the From row, which are no choice.
    private bool settingFrom;

    /// <summary>An empty card: Cc and Bcc hidden, nothing to choose from.</summary>
    public ComposeHeader()
    {
        InitializeComponent();
    }

    /// <summary>The user picked another identity in From.</summary>
    public event EventHandler? FromChanged;

    /// <summary>The To row.</summary>
    public RecipientTokenBox To => ToBox;

    /// <summary>The Cc row.</summary>
    public RecipientTokenBox Cc => CcBox;

    /// <summary>The Bcc row.</summary>
    public RecipientTokenBox Bcc => BccBox;

    /// <summary>
    /// The window is closing: the recipient rows change nothing and say
    /// nothing from now on (the commit of a focus lost on the way out must
    /// not mark the cleaned-up draft dirty).
    /// </summary>
    public void FreezeRecipients()
    {
        foreach (var field in RecipientFields)
        {
            field.Freeze();
        }
    }

    /// <summary>The Subject row.</summary>
    public TextBox Subject => SubjectBox;

    /// <summary>The recipient rows, in order (compose.go <c>suggest</c>'s rows).</summary>
    public IReadOnlyList<RecipientTokenBox> RecipientFields => [ToBox, CcBox, BccBox];

    /// <summary>
    /// Whether the card has its From line (compose.go's from_box and
    /// from_separator; ComposeHeaderView showsFrom). The board's inline
    /// reply has none: it is from-locked and the detail shows the account.
    /// </summary>
    public bool ShowsFrom
    {
        get => FromBox.Visibility == Visibility.Visible;
        set
        {
            var v = value ? Visibility.Visible : Visibility.Collapsed;
            FromLabel.Visibility = FromBox.Visibility = FromSeparator.Visibility = v;
        }
    }

    /// <summary>The selected identity's index (<c>from.Selected()</c>), 0 when none is.</summary>
    public int SelectedAccountIndex => Math.Max(FromBox.SelectedIndex, 0);

    /// <summary>Whether the From drop-down is open (the window's Escape must not close the window then).</summary>
    public bool IsFromOpen => FromBox.IsDropDownOpen;

    /// <summary>
    /// setAccounts' row: the identities' labels (plain text), the selected
    /// one, and whether there is a choice at all.
    /// </summary>
    public void SetAccounts(IReadOnlyList<string> labels, int selected, bool enabled)
    {
        ArgumentNullException.ThrowIfNull(labels);
        settingFrom = true;
        try
        {
            FromBox.ItemsSource = labels;
            FromBox.SelectedIndex = selected >= 0 && selected < labels.Count ? selected : -1;
            FromBox.IsEnabled = enabled;
        }
        finally
        {
            settingFrom = false;
        }
    }

    /// <summary>
    /// setCcBccVisible: reveals the lines asked for, each with the separator
    /// above it, and keeps the Cc/Bcc button only while one of them is still
    /// hidden. A reply carrying only a Cc therefore opens no empty Bcc line.
    /// </summary>
    public void SetCcBccVisible(bool cc, bool bcc)
    {
        if (cc)
        {
            CcLabel.Visibility = CcBox.Visibility = CcSeparator.Visibility = Visibility.Visible;
        }
        if (bcc)
        {
            BccLabel.Visibility = BccBox.Visibility = BccSeparator.Visibility = Visibility.Visible;
        }
        var shown = ComposeHeaderRules.CcBccButtonVisible(CcBox.Visibility == Visibility.Visible, BccBox.Visibility == Visibility.Visible);
        if (!shown && CcBccButton.FocusState != FocusState.Unfocused)
        {
            // The button goes away under the keyboard: Cc takes it.
            CcBox.FocusInput();
        }
        CcBccButton.Visibility = shown ? Visibility.Visible : Visibility.Collapsed;
    }

    /// <summary>compose.blp's focus-widget: the To row.</summary>
    public void FocusTo() => ToBox.FocusInput();

    private void OnFromSelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (settingFrom || FromBox.SelectedIndex < 0)
        {
            return;
        }
        FromChanged?.Invoke(this, EventArgs.Empty);
    }

    // showCcBcc: both lines at once.
    private void OnCcBccClick(object sender, RoutedEventArgs e) => SetCcBccVisible(cc: true, bcc: true);
}
