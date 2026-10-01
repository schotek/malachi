// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of RecipientBadgeView of macos/Sources/MalachiMail/Compose/
// RecipientTokenField.swift; GTK: ui/internal/compose/recipient_field.go
// (the badge). The logic (what is shown, what is valid) is the token's,
// RecipientTokens.Token of Malachi.Core; this shows it and reports clicks.

using System;
using Malachi.Core.Compose;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;

namespace Malachi.App.Compose;

/// <summary>One finished recipient of a To, Cc or Bcc row.</summary>
public sealed partial class RecipientBadge : UserControl
{
    /// <summary>How many characters of a label the badge holds (the text is trimmed to its width anyway).</summary>
    public const int MaxLabelChars = 200;

    /// <summary>How many characters of the tooltip and the accessible name.</summary>
    public const int MaxTooltipChars = 1000;

    private bool selected;

    /// <summary>A badge for <paramref name="token"/>, the <paramref name="index"/>th of its row.</summary>
    public RecipientBadge(int index, RecipientTokens.Token token)
    {
        ArgumentNullException.ThrowIfNull(token);
        InitializeComponent();
        Index = index;
        IsValid = token.IsValid;
        // Plain strings only, however long: the text is data from the mail.
        LabelText.Text = Cap(token.Label, MaxLabelChars);
        var tip = Cap(token.Tooltip, MaxTooltipChars);
        ToolTipService.SetToolTip(this, tip);
        AutomationProperties.SetName(this, tip);
        AutomationProperties.SetHelpText(RemoveButton, tip);
        Loaded += (_, _) => UpdateLook();
        UpdateLook();
    }

    /// <summary>The badge was pressed (select it).</summary>
    public event EventHandler? Pressed;

    /// <summary>The badge was double-clicked (edit it).</summary>
    public event EventHandler? EditRequested;

    /// <summary>The Remove cross was clicked.</summary>
    public event EventHandler? RemoveRequested;

    /// <summary>The position of the token in its row.</summary>
    public int Index { get; }

    /// <summary>Whether the token is a mailbox.</summary>
    public bool IsValid { get; }

    /// <summary>Whether the badge is the selected one of the row.</summary>
    public bool IsSelected
    {
        get => selected;
        set
        {
            if (selected != value)
            {
                selected = value;
                UpdateLook();
            }
        }
    }

    /// <summary>
    /// <paramref name="text"/> cut to <paramref name="max"/> UTF-16 units
    /// (never between the halves of a pair) with an ellipsis.
    /// </summary>
    internal static string Cap(string text, int max)
    {
        if (text.Length <= max)
        {
            return text;
        }
        var cut = char.IsHighSurrogate(text[max - 1]) ? max - 1 : max;
        return text[..cut] + (char)0x2026;
    }

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new BadgePeer(this);

    private void UpdateLook()
    {
        var state = (IsValid, selected) switch
        {
            (true, false) => "Normal",
            (true, true) => "Selected",
            (false, false) => "Invalid",
            (false, true) => "InvalidSelected",
        };
        VisualStateManager.GoToState(this, state, useTransitions: false);
    }

    private void OnPointerPressed(object sender, PointerRoutedEventArgs e)
    {
        if (!e.GetCurrentPoint(this).Properties.IsLeftButtonPressed)
        {
            return;
        }
        e.Handled = true;
        Pressed?.Invoke(this, EventArgs.Empty);
    }

    private void OnDoubleTapped(object sender, DoubleTappedRoutedEventArgs e)
    {
        e.Handled = true;
        EditRequested?.Invoke(this, EventArgs.Empty);
    }

    private void OnRemoveClick(object sender, RoutedEventArgs e) => RemoveRequested?.Invoke(this, EventArgs.Empty);

    // A named group (as ChipGroup): Narrator says whose Remove the button
    // inside is.
    private sealed partial class BadgePeer(FrameworkElement owner) : FrameworkElementAutomationPeer(owner)
    {
        protected override AutomationControlType GetAutomationControlTypeCore() => AutomationControlType.Group;

        protected override string GetClassNameCore() => nameof(RecipientBadge);

        protected override bool IsControlElementCore() => true;
    }
}
