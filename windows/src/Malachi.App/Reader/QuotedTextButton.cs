// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/QuotedTextButton.swift;
// GTK: ui/internal/conversation/quoted.go (QuotedTextLabel). The small
// "•••" button under a body whose quoted history the daemon cut
// (LoadedMessageText.QuotedTextOfferFor): Show Quoted Text, then Hide Quoted
// Text, in its tooltip and accessible name. Collapsed while there is nothing
// to offer. The conversation cards, the pane's message and the message
// window each have one; an attached message's window none. Windows: the
// three dots are Segoe Fluent's More glyph (GTK's view-more) on a small
// button, not text.

using Malachi.App.Resources;
using Malachi.Core.Model;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Reader;

/// <summary>The "•••" under a trimmed body; its owner acts on its Click.</summary>
public sealed partial class QuotedTextButton : Button
{
    /// <summary>A collapsed button, shown by <see cref="Show"/>.</summary>
    public QuotedTextButton()
    {
        Content = new FontIcon { Glyph = Icons.Glyph("view-more"), FontSize = 12 };
        HorizontalAlignment = HorizontalAlignment.Left;
        VerticalAlignment = VerticalAlignment.Top;
        MinWidth = 32;
        MinHeight = 20;
        Padding = new Thickness(8, 2, 8, 2);
        AutomationProperties.SetAutomationId(this, "QuotedText");
        Visibility = Visibility.Collapsed;
    }

    /// <summary>What the button offers now; null while it is collapsed.</summary>
    public QuotedTextOffer? Offer { get; private set; }

    /// <summary>Shows <paramref name="offer"/>, or collapses the button (null).</summary>
    public void Show(QuotedTextOffer? offer)
    {
        Offer = offer;
        Visibility = offer is null ? Visibility.Collapsed : Visibility.Visible;
        if (offer is not { } o)
        {
            return;
        }
        var label = o.Label;
        ToolTipService.SetToolTip(this, label);
        AutomationProperties.SetName(this, label);
    }
}
