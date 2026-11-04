// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Shared/InvitationPill.swift (colours,
// show); GTK: none yet (ui/internal/calendar/invitation.go InvitationPill,
// PillTitle). The pill's kind as a dependency property, for x:Bind from a
// row, the invitation card or the Today strip; the kind picks the text
// (Cal.PillTitle) and the visual state (InvitationPill.xaml). A UserControl
// has no automation peer of its own: the pill is a text element with its
// AutomationId and its title (TextPeer).

using Malachi.Core.Calendars;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Controls;

/// <summary>The pill of a message with a calendar part: Invitation, Update, Cancelled or Reply.</summary>
public sealed partial class InvitationPill : UserControl
{
    /// <summary>Which pill; None hides it.</summary>
    public static readonly DependencyProperty KindProperty = DependencyProperty.Register(
        nameof(Kind), typeof(Cal.InvitationPill), typeof(InvitationPill), new PropertyMetadata(Cal.InvitationPill.None, OnChanged));

    /// <summary>An empty, hidden pill.</summary>
    public InvitationPill()
    {
        InitializeComponent();
        Apply();
    }

    /// <summary>Which pill; None hides it.</summary>
    public Cal.InvitationPill Kind
    {
        get => (Cal.InvitationPill)GetValue(KindProperty);
        set => SetValue(KindProperty, value);
    }

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new TextPeer(this);

    private static void OnChanged(DependencyObject d, DependencyPropertyChangedEventArgs e) => ((InvitationPill)d).Apply();

    // InvitationPill.show: the title, hidden for none, the kind's colours.
    private void Apply()
    {
        var text = Cal.PillTitle(Kind);
        Label.Text = text;
        AutomationProperties.SetName(this, text);
        Visibility = text.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        var state = Kind switch
        {
            Cal.InvitationPill.Update => "Update",
            Cal.InvitationPill.Cancelled => "Cancelled",
            Cal.InvitationPill.Reply => "Reply",
            _ => "Invitation",
        };
        VisualStateManager.GoToState(this, state, false);
    }

    // The pill as one text element of the automation tree.
    private sealed partial class TextPeer(FrameworkElement owner) : FrameworkElementAutomationPeer(owner)
    {
        protected override AutomationControlType GetAutomationControlTypeCore() => AutomationControlType.Text;

        protected override string GetClassNameCore() => nameof(InvitationPill);

        protected override bool IsControlElementCore() => true;
    }
}
