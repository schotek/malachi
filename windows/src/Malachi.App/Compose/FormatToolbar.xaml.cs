// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Compose/FormatToolbar.swift (the
// controls' actions, applyState, setBlock, setAlign, showLinkPopover,
// insertLink, colorChanged, clearFormatting); GTK:
// ui/internal/compose/compose.go (wireActions' block and align, wireToolbar,
// applyState). What each control sends and what the bar shows for the
// formatting at the caret are Core's (FormatBarState); the link is accepted
// by ComposeLinkUrl as GTK's url.Parse check accepts it. The bar never echoes
// the page's state back as commands: the page's state sets IsChecked, and
// only Click (the user's) sends a command. The text colour is sent when its
// flyout closes with another colour than the last one sent (GTK's
// ColorDialog sends it when the dialog is confirmed).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Resources;
using Malachi.Core.Controllers;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Presentation;
using Microsoft.UI;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Shapes;
using Windows.UI;

namespace Malachi.App.Compose;

/// <summary>The formatting bar of a compose window.</summary>
public sealed partial class FormatToolbar : UserControl
{
    private Color sentColor = Colors.Black;

    /// <summary>A bar for a left-aligned paragraph, nothing on.</summary>
    public FormatToolbar()
    {
        InitializeComponent();
        foreach (var block in FormatBarState.Blocks)
        {
            var item = new RadioMenuFlyoutItem { Text = FormatBarState.BlockTitle(block), GroupName = "block", Tag = block };
            item.Click += OnBlockClick;
            BlockMenu.Items.Add(item);
        }
        foreach (var align in FormatBarState.Aligns)
        {
            var item = new RadioMenuFlyoutItem
            {
                Text = FormatBarState.AlignTitle(align),
                GroupName = "align",
                Tag = align,
                Icon = Icons.Create(FormatBarState.AlignIconOf(align)),
            };
            item.Click += OnAlignClick;
            AlignMenu.Items.Add(item);
        }
        ColorChooser.Color = sentColor;
        ApplyState(FormatBarState.Initial);
    }

    /// <summary>editor.Exec: runs an editing command on the page's selection.</summary>
    public Action<string, string?>? Exec { get; set; }

    /// <summary>editor.GrabFocus: the keyboard back to the page (after a menu).</summary>
    public Action? FocusEditor { get; set; }

    /// <summary>The image button (compose.insert-image).</summary>
    public Action? InsertImageRequested { get; set; }

    /// <summary>Whether one of the bar's menus or popovers is open (the window's Escape must not close the window then).</summary>
    public bool IsPopupOpen => LinkFlyout.IsOpen || ColorFlyout.IsOpen || BlockMenu.IsOpen || AlignMenu.IsOpen;

    /// <summary>applyState: mirrors the formatting at the caret.</summary>
    public void ApplyState(FormatBarState s)
    {
        ArgumentNullException.ThrowIfNull(s);
        BoldButton.IsChecked = s.Bold;
        ItalicButton.IsChecked = s.Italic;
        UnderlineButton.IsChecked = s.Underline;
        BulletedListButton.IsChecked = s.BulletedList;
        NumberedListButton.IsChecked = s.NumberedList;
        QuoteButton.IsChecked = s.Quote;
        BlockButton.Content = s.BlockLabel;
        foreach (var item in BlockMenu.Items)
        {
            if (item is RadioMenuFlyoutItem radio)
            {
                radio.IsChecked = string.Equals(radio.Tag as string, s.Block, StringComparison.Ordinal);
            }
        }
        AlignIcon.Glyph = Icons.Glyph(s.AlignIcon);
        foreach (var item in AlignMenu.Items)
        {
            if (item is RadioMenuFlyoutItem radio)
            {
                radio.IsChecked = string.Equals(radio.Tag as string, s.Align, StringComparison.Ordinal);
            }
        }
    }

    /// <summary>Insert Link (the editor's Ctrl+K): opens the link popover under its button.</summary>
    public void ShowLinkFlyout()
    {
        if (!LinkFlyout.IsOpen)
        {
            LinkFlyout.ShowAt(LinkButton);
        }
    }

    /// <summary>
    /// comment.go <c>restrictToolbar</c> (macOS <c>FormatToolbar.restrict</c>):
    /// keeps only the controls of a comment's formats
    /// (<see cref="CommentMode.RestrictedToolbar"/>); the others leave the
    /// bar, and so does a separator left with nothing to separate.
    /// </summary>
    public void RestrictToComment()
    {
        // The controls by the format each applies (comment.go toolbarFormats).
        var formats = new Dictionary<UIElement, JiraFormat>
        {
            [BoldButton] = JiraFormat.Bold,
            [ItalicButton] = JiraFormat.Italic,
            [UnderlineButton] = JiraFormat.Underline,
            [BlockButton] = JiraFormat.Heading,
            [AlignButton] = JiraFormat.Alignment,
            [BulletedListButton] = JiraFormat.BulletList,
            [NumberedListButton] = JiraFormat.NumberedList,
            [QuoteButton] = JiraFormat.Quote,
            [LinkButton] = JiraFormat.Link,
            [ColorButton] = JiraFormat.Colour,
            [ImageButton] = JiraFormat.Image,
            [ClearButton] = JiraFormat.Clear,
        };
        var children = Bar.Children.ToList();
        var items = children
            .Select(c => c is Rectangle ? CommentToolItem.Divider : CommentToolItem.Control(formats.TryGetValue(c, out var f) ? f : null))
            .ToList();
        var shown = CommentMode.RestrictedToolbar(items);
        for (var i = 0; i < children.Count; i++)
        {
            children[i].Visibility = shown[i] ? Visibility.Visible : Visibility.Collapsed;
        }
    }

    /// <summary>Closes whatever of the bar is open (the window is closing).</summary>
    public void HidePopups()
    {
        LinkFlyout.Hide();
        ColorFlyout.Hide();
        BlockMenu.Hide();
        AlignMenu.Hide();
    }

    private void Send((string Command, string? Argument) command) => Exec?.Invoke(command.Command, command.Argument);

    private void OnBoldClick(object sender, RoutedEventArgs e) => Exec?.Invoke("bold", null);

    private void OnItalicClick(object sender, RoutedEventArgs e) => Exec?.Invoke("italic", null);

    private void OnUnderlineClick(object sender, RoutedEventArgs e) => Exec?.Invoke("underline", null);

    private void OnBulletedListClick(object sender, RoutedEventArgs e) => Exec?.Invoke("insertUnorderedList", null);

    private void OnNumberedListClick(object sender, RoutedEventArgs e) => Exec?.Invoke("insertOrderedList", null);

    // The quote toggle, as the user left it: into a blockquote, or out.
    private void OnQuoteClick(object sender, RoutedEventArgs e) => Send(FormatBarState.QuoteCommand(QuoteButton.IsChecked == true));

    // compose.block, then the keyboard back to the page.
    private void OnBlockClick(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.Tag is string block)
        {
            Send(FormatBarState.BlockCommand(block));
            FocusEditor?.Invoke();
        }
    }

    // compose.align, then the keyboard back to the page.
    private void OnAlignClick(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.Tag is string align)
        {
            Send(FormatBarState.AlignCommand(align));
            FocusEditor?.Invoke();
        }
    }

    private void OnImageClick(object sender, RoutedEventArgs e) => InsertImageRequested?.Invoke();

    // Clear Formatting: the formatting, then the links.
    private void OnClearClick(object sender, RoutedEventArgs e)
    {
        foreach (var command in FormatBarState.ClearCommands)
        {
            Send(command);
        }
    }

    private void OnLinkFlyoutOpened(object sender, object e)
    {
        SetLinkInvalid(false);
        LinkEntry.Focus(FocusState.Programmatic);
    }

    private void OnLinkFlyoutClosed(object sender, object e) => FocusEditor?.Invoke();

    private void OnLinkApplyClick(object sender, RoutedEventArgs e) => InsertLink();

    // link_entry's activate. The TextBox takes Enter before its KeyDown is
    // raised (measured), so the key is read on its way down.
    private void OnLinkEntryKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == Windows.System.VirtualKey.Enter)
        {
            e.Handled = true;
            InsertLink();
        }
    }

    // insertLink: an http, https or mailto URL goes to the page; anything
    // else turns the entry red and keeps the popover open. The red stays
    // until a link is accepted (GTK removes the error class only then) or
    // the popover opens again.
    private void InsertLink()
    {
        if (ComposeLinkUrl.Accept(LinkEntry.Text) is not { } url)
        {
            SetLinkInvalid(true);
            return;
        }
        SetLinkInvalid(false);
        Exec?.Invoke("createLink", url);
        LinkEntry.Text = "";
        LinkFlyout.Hide();
    }

    // link_entry's error class: its invalid style (the same template).
    private void SetLinkInvalid(bool invalid)
    {
        var style = (Style)Resources[invalid ? "LinkEntryInvalidStyle" : "LinkEntryStyle"];
        if (!ReferenceEquals(LinkEntry.Style, style))
        {
            LinkEntry.Style = style;
        }
    }

    // ColorDialogButton's rgba: a new colour, sent once the flyout closes.
    private void OnColorFlyoutClosed(object sender, object e)
    {
        var color = ColorChooser.Color;
        color.A = 0xFF;
        if (color == sentColor)
        {
            FocusEditor?.Invoke();
            return;
        }
        sentColor = color;
        ColorSwatch.Background = new SolidColorBrush(color);
        Exec?.Invoke("foreColor", FormatBarState.CssColor(color.R, color.G, color.B));
        FocusEditor?.Invoke();
    }
}
