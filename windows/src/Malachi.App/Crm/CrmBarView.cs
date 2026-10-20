// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/CRM/CRMBarView.swift; GTK:
// window/crm_bar.go of docs/eway.md §2.6 (written in T7). The bar under the
// header of a message saved in eWay-CRM (Core's CrmText.BarFor): the
// eWay-CRM symbol, "Saved in eWay-CRM" over the names of the linked items
// (and the private hint when the saved item is private), and Open in
// eWay-CRM, in the look of the bulk-mail strip. Every name is from the CRM
// and set as TextBlock.Text only. Shown in the reading pane, a message
// window and the conversation's cards, never for an attached message.
//
// Open in eWay-CRM opens the saved item in the browser at once, without
// the "Open This Link?" question of the mail's links (a listed deviation,
// windows/README.md): the URL is built by the daemon from the account's
// stored service address and the item's GUID of fixed shape (docs/eway.md
// D15), never taken from the mail or the CRM, and CrmRules.OpenUrl lets
// only https through.

using System;
using Malachi.App.Resources;
using Malachi.Core.Api;
using Malachi.Core.Crm;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Crm;

/// <summary>The bar of a message saved in eWay-CRM.</summary>
public sealed partial class CrmBarView : UserControl
{
    private readonly TextBlock title;
    private readonly TextBlock names;
    private readonly TextBlock note;
    private readonly Button open;
    private CrmBar? bar;

    /// <summary>A hidden bar; <paramref name="padding"/> is the space around its content.</summary>
    public CrmBarView(Thickness padding)
    {
        IsTabStop = false;
        Visibility = Visibility.Collapsed;
        AutomationProperties.SetAutomationId(this, "CrmBar");
        var icon = Icons.Create(CrmRules.Icon);
        icon.VerticalAlignment = VerticalAlignment.Center;
        AutomationProperties.SetAccessibilityView(icon, Microsoft.UI.Xaml.Automation.Peers.AccessibilityView.Raw);
        title = new TextBlock { Style = (Style)Application.Current.Resources["BodyLabelStyle"], TextWrapping = TextWrapping.Wrap };
        AutomationProperties.SetAutomationId(title, "CrmBarTitle");
        names = new TextBlock
        {
            Style = (Style)Application.Current.Resources["DimCaptionLabelStyle"],
            TextWrapping = TextWrapping.Wrap,
            MaxLines = 3,
            TextTrimming = TextTrimming.CharacterEllipsis,
        };
        AutomationProperties.SetAutomationId(names, "CrmBarNames");
        note = new TextBlock { Style = (Style)Application.Current.Resources["DimCaptionLabelStyle"], TextWrapping = TextWrapping.Wrap };
        AutomationProperties.SetAutomationId(note, "CrmBarNote");
        open = new Button { VerticalAlignment = VerticalAlignment.Center, AllowFocusOnInteraction = false };
        AutomationProperties.SetAutomationId(open, "CrmOpenButton");
        open.Click += (_, _) =>
        {
            if (bar is { Url.Length: > 0 } b)
            {
                OpenRequested?.Invoke(this, b.Url);
            }
        };
        var text = new StackPanel { Spacing = 1, VerticalAlignment = VerticalAlignment.Center };
        text.Children.Add(title);
        text.Children.Add(names);
        text.Children.Add(note);
        var grid = new Grid { ColumnSpacing = 12 };
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
        grid.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
        grid.Children.Add(icon);
        Grid.SetColumn(text, 1);
        grid.Children.Add(text);
        Grid.SetColumn(open, 2);
        grid.Children.Add(open);
        Content = new Border
        {
            Padding = padding,
            CornerRadius = new CornerRadius(4),
            Background = (Microsoft.UI.Xaml.Media.Brush)Application.Current.Resources["SubtleFillColorSecondaryBrush"],
            Child = grid,
        };
    }

    /// <summary>Open in eWay-CRM: the saved item's URL (the daemon's); the owner opens it without a question.</summary>
    public event EventHandler<string>? OpenRequested;

    /// <summary>Shows the bar of <paramref name="record"/> (<see cref="CrmText.BarFor"/>), or hides it for none.</summary>
    public void Show(CrmRecord? record)
    {
        var b = CrmText.BarFor(record);
        bar = b;
        if (b is null)
        {
            Visibility = Visibility.Collapsed;
            return;
        }
        title.Text = b.Title;
        names.Text = b.Names;
        names.Visibility = b.Names.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        if (b.Names.Length > 0)
        {
            ToolTipService.SetToolTip(names, b.Names);
        }
        note.Text = b.Note;
        note.Visibility = b.Note.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        open.Content = b.OpenLabel;
        open.Visibility = b.Url.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        Visibility = Visibility.Visible;
    }
}
