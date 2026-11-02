// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of AttachmentSummaryView of
// macos/Sources/MalachiMail/MessageView/AttachmentChipView.swift; GTK:
// attachments.go buildSaveAll follows. The line over the tiles of two or
// more attachments that can all be saved: their number and total size in a
// dim caption (Format.AttachmentSummary), and at its end the quiet Save All
// button, in the caption's size and colour. It takes a line of its own in
// the WrapBox (IWrapBoxFullLine) and comes first.

using Malachi.App.Localization;
using Malachi.App.Resources;
using Malachi.Core.I18n;
using Malachi.Core.Presentation;
using Malachi.Core.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Reader;

/// <summary>The summary line over the attachment tiles, with Save All.</summary>
internal sealed partial class AttachmentSummaryLine : Grid, IWrapBoxFullLine
{
    /// <summary>
    /// The line for <paramref name="count"/> attachments of
    /// <paramref name="total"/> bytes; <paramref name="buttonStyle"/> is the
    /// owner's SummaryActionStyle (ChipStyles.xaml).
    /// </summary>
    public AttachmentSummaryLine(int count, long total, Style buttonStyle)
    {
        ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        ColumnSpacing = 12;

        var label = new TextBlock
        {
            Text = Format.AttachmentSummary(count, total),
            Style = (Style)Application.Current.Resources["DimCaptionLabelStyle"],
            VerticalAlignment = VerticalAlignment.Center,
            TextTrimming = TextTrimming.CharacterEllipsis,
            TextWrapping = TextWrapping.NoWrap,
        };
        Children.Add(label);

        var mnemonic = Mnemonic.Parse(L10n.T("Save _All"));
        var content = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 4 };
        var icon = Icons.Create("document-save", 12);
        content.Children.Add(icon);
        content.Children.Add(new TextBlock
        {
            Text = mnemonic.Label,
            Style = (Style)Application.Current.Resources["DimCaptionLabelStyle"],
            VerticalAlignment = VerticalAlignment.Center,
        });
        SaveAll = new Button
        {
            Content = content,
            Style = buttonStyle,
            AccessKey = mnemonic.AccessKey ?? "",
        };
        AutomationProperties.SetName(SaveAll, mnemonic.Label);
        AutomationProperties.SetAutomationId(SaveAll, "SaveAllButton");
        SetColumn(SaveAll, 1);
        Children.Add(SaveAll);
    }

    /// <summary>Save All, disabled while one of the message runs.</summary>
    public Button SaveAll { get; }
}
