// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/conversation_rows.go (convRow, newConvRow,
// show); macOS: MessageView/ConversationRow.swift. One item of the
// conversation's stack with its piece of the timeline
// (ConversationLayout.Rails): in the gutter the marker (the sender's avatar
// at the top of a card, a dot beside the first line of an event or of the
// row of older messages) with the line above and below it, and beside the
// gutter the item itself, the item gap above it. The rows touch, so the
// pieces of the line join: each runs through its own row from edge to edge,
// the gap included, and stops short of the marker. The gutter is
// decoration: the card and the event row name their sender themselves.

using Malachi.App.Main;
using Malachi.Core.Model;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Shapes;
using L = Malachi.Core.Model.ConversationLayout;

namespace Malachi.App.Reader.Conversation;

/// <summary>A row of the conversation view: its piece of the timeline and its item.</summary>
internal sealed partial class ConversationRow : Grid
{
    // conversation_rows.go convCaptionMiddle: how far below the top of a
    // caption's first line its middle is, where the dot of an event sits.
    private const double CaptionMiddle = 8;

    private readonly Rectangle above;
    private readonly Rectangle below;

    /// <summary>newConvRow: a row of <paramref name="item"/> marked by <paramref name="marker"/>; the styles come from <paramref name="look"/>.</summary>
    public ConversationRow(FrameworkElement item, RailMarker marker, System.Func<string, Style> look)
    {
        Item = item;
        ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(L.Avatar) });
        ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });

        var gutter = new Grid();
        gutter.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        gutter.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        gutter.RowDefinitions.Add(new RowDefinition { Height = new GridLength(1, GridUnitType.Star) });
        AutomationProperties.SetAccessibilityView(gutter, AccessibilityView.Raw);
        var markerTop = L.ItemGap;
        FrameworkElement mark;
        if (marker == RailMarker.Avatar)
        {
            Avatar = new Avatar { Size = L.Avatar, HorizontalAlignment = HorizontalAlignment.Center };
            mark = Avatar;
        }
        else
        {
            mark = new Ellipse { Style = look("ConversationDotStyle") };
            markerTop = L.ItemGap + CaptionMiddle - (L.Dot / 2);
        }
        // The line from the row's top to a break above the marker, whose top
        // is then markerTop (the avatar's at the card's top), and from a
        // break under the marker to the row's bottom.
        above = new Rectangle
        {
            Style = look("ConversationLineStyle"),
            Height = markerTop - L.LineBreak,
            Margin = new Thickness(0, 0, 0, L.LineBreak),
        };
        below = new Rectangle
        {
            Style = look("ConversationLineStyle"),
            Margin = new Thickness(0, L.LineBreak, 0, 0),
            VerticalAlignment = VerticalAlignment.Stretch,
        };
        gutter.Children.Add(above);
        SetRow(mark, 1);
        gutter.Children.Add(mark);
        SetRow(below, 2);
        gutter.Children.Add(below);

        item.Margin = new Thickness(L.GutterGap, L.ItemGap, 0, 0);
        SetColumn(item, 1);
        Children.Add(gutter);
        Children.Add(item);
    }

    /// <summary>The item the row holds (a card, an event row, the row of older messages).</summary>
    public FrameworkElement Item { get; }

    /// <summary>The card the row holds; null for an event and the row of the older members.</summary>
    public ConversationCard? Card { get; init; }

    /// <summary>The avatar of a card's row; null for a dot.</summary>
    public Avatar? Avatar { get; }

    /// <summary>
    /// show: the row's piece of the timeline; <paramref name="name"/> is the
    /// sender the avatar stands for (hostile text, only ever drawn as
    /// initials). A piece that is not drawn keeps its place, so the marker
    /// stays put.
    /// </summary>
    public void Show(L.Rail rail, string name, bool monochrome)
    {
        above.Opacity = rail.Above ? 1 : 0;
        below.Opacity = rail.Below ? 1 : 0;
        if (Avatar is null)
        {
            return;
        }
        Avatar.Text = name;
        Avatar.Monochrome = monochrome;
        Avatar.Accent = rail.Accent;
    }
}
