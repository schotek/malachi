// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first view of ui/internal/signature (BadgeFor, Badge); the macOS
// port is macos/Sources/MalachiMail/MessageView/SignatureBadgeView.swift
// (written at the same time). The badge in a message's header
// (docs/plans/signatures.md §6): the verdict's shield in its colour and the
// sentence ("Signed by Jan Novák <jan@example.org>"), a flat button that opens
// the details flyout (SignatureDetailsPresenter). Shown in the reading pane,
// a message window and an open conversation card, never in a chat bubble or an
// attached message's window. Hidden without a verdict. The sentence is built
// from a certificate or key and set as TextBlock.Text only; a long one is cut
// (the whole sentence is the button's accessible name, the details show the rest).

using System;
using Malachi.App.Controls;
using Malachi.App.Reader;
using Malachi.Core.Api;
using Malachi.Core.Signatures;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Signatures;

/// <summary>The badge of a signed message in its header.</summary>
public sealed partial class SignatureBadgeButton : UserControl
{
    private readonly Button button;
    private readonly SignatureGlyph glyph;
    private readonly TextBlock label;
    private ReaderServices? services;
    private Func<Window?>? host;
    private SignatureDetailsPresenter? presenter;
    private SignatureSummary? summary;
    private MessageId id;
    private string from = "";

    /// <summary>A hidden badge; <see cref="Attach"/> gives it the application.</summary>
    public SignatureBadgeButton()
    {
        IsTabStop = false;
        Visibility = Visibility.Collapsed;
        HorizontalAlignment = HorizontalAlignment.Left;
        glyph = new SignatureGlyph();
        label = new TextBlock
        {
            VerticalAlignment = VerticalAlignment.Center,
            MaxLines = 1,
            TextTrimming = TextTrimming.CharacterEllipsis,
            TextWrapping = TextWrapping.NoWrap,
        };
        var row = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 6 };
        row.Children.Add(glyph);
        row.Children.Add(label);
        button = new Button
        {
            Padding = new Thickness(8, 2, 8, 2),
            Content = row,
            Style = (Style)Application.Current.Resources["SubtleButtonStyle"],
        };
        AutomationProperties.SetAutomationId(button, "SignatureBadge");
        button.Click += (_, _) => Open();
        Content = button;
    }

    /// <summary>Gives the badge the application and the window its flyout and dialogs go on.</summary>
    public void Attach(ReaderServices readerServices, Func<Window?> hostWindow)
    {
        ArgumentNullException.ThrowIfNull(readerServices);
        ArgumentNullException.ThrowIfNull(hostWindow);
        services = readerServices;
        host = hostWindow;
        presenter = new SignatureDetailsPresenter(readerServices, hostWindow);
    }

    /// <summary>
    /// Shows the badge of <paramref name="verdict"/>, <c>message.get</c>'s
    /// signature of message <paramref name="messageId"/> whose From address is
    /// <paramref name="fromAddress"/>; null hides it.
    /// </summary>
    public void Show(SignatureSummary? verdict, MessageId messageId, string fromAddress)
    {
        summary = verdict;
        id = messageId;
        from = fromAddress ?? "";
        var badge = SignatureView.BadgeFor(verdict);
        Visibility = badge.Visible ? Visibility.Visible : Visibility.Collapsed;
        glyph.IconName = badge.Icon;
        glyph.Tone = badge.Style;
        label.Text = badge.Text;
        label.Style = (Style)Application.Current.Resources[badge.Severity switch
        {
            SignatureSeverity.Bad => "CriticalCaptionLabelStyle",
            SignatureSeverity.Warning => "CautionCaptionLabelStyle",
            SignatureSeverity.Neutral => "DimCaptionLabelStyle",
            _ => "CaptionLabelStyle",
        }];
        label.MaxLines = 1;
        label.TextTrimming = TextTrimming.CharacterEllipsis;
        // The tooltip says what a click does; a screen reader reads the whole sentence the label may cut.
        ToolTipService.SetToolTip(button, badge.Visible ? badge.Tooltip : null);
        AutomationProperties.SetName(button, badge.Visible ? badge.Text : "");
        AutomationProperties.SetHelpText(button, badge.Visible ? badge.Tooltip : "");
    }

    /// <summary>Opens the details of the shown verdict (the badge's click, also for a test).</summary>
    public void Open()
    {
        // A message without an id (the board's invented samples) shows its
        // badge and has no details to ask for.
        if (summary is null || presenter is null || services is null || host is null || string.IsNullOrEmpty(id.Value))
        {
            return;
        }
        presenter.Show(button, id, summary, from);
    }
}
