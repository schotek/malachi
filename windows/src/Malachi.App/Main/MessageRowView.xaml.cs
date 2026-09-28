// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/widget/message_row.go (the preview's attributes from
// widget/highlight.go: the matched words in bold, never markup;
// ConnectExpander); macOS: MessageList/MessageCellView.swift. The view of
// Core's MessageRow (MessageRowView.xaml); a ListView's recycled container
// gets another row through Row, and the preview's runs follow it.

using System.ComponentModel;
using Malachi.Core.Presentation;
using Microsoft.UI.Text;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Documents;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Main;

/// <summary>One row of the message list.</summary>
public sealed partial class MessageRowView : UserControl
{
    /// <summary>The row it shows.</summary>
    public static readonly DependencyProperty RowProperty = DependencyProperty.Register(
        nameof(Row), typeof(MessageRow), typeof(MessageRowView), new PropertyMetadata(null, OnRowChanged));

    /// <summary>An empty row.</summary>
    public MessageRowView()
    {
        InitializeComponent();
    }

    /// <summary>The row it shows.</summary>
    public MessageRow? Row
    {
        get => (MessageRow?)GetValue(RowProperty);
        set => SetValue(RowProperty, value);
    }

    /// <summary>content_box's margins: the start of applyLead, 6 at the end, the density's above and below.</summary>
    public static Thickness ContentMargin(double start, double top, double bottom) => new(start, top, MessageRow.MarginEnd, bottom);

    /// <summary>Bold for unread (the heading class), as Typography.xaml's heading style.</summary>
    public static Windows.UI.Text.FontWeight Weight(bool unread) => unread ? FontWeights.SemiBold : FontWeights.Normal;

    /// <summary>The live fold arrow is visible; the kept place is not.</summary>
    public static double Opaque(bool live) => live ? 1 : 0;

    /// <summary>An inert, invisible control (the kept place of a fold arrow) is no element for a screen reader.</summary>
    public static AccessibilityView View(bool live) => live ? AccessibilityView.Content : AccessibilityView.Raw;

    /// <summary>No tooltip for an empty text.</summary>
    public static object? Tip(string? text) => string.IsNullOrEmpty(text) ? null : text;

    /// <summary>The hairline under every row but the last.</summary>
    public static Visibility NotLast(bool last) => last ? Visibility.Collapsed : Visibility.Visible;

    private static void OnRowChanged(DependencyObject d, DependencyPropertyChangedEventArgs e)
    {
        var view = (MessageRowView)d;
        if (e.OldValue is MessageRow old)
        {
            old.PropertyChanged -= view.OnRowPropertyChanged;
        }
        if (e.NewValue is MessageRow row)
        {
            row.PropertyChanged += view.OnRowPropertyChanged;
        }
        view.FillPreview();
    }

    private void OnRowPropertyChanged(object? sender, PropertyChangedEventArgs e)
    {
        if (e.PropertyName is nameof(MessageRow.Preview) or nameof(MessageRow.Highlights))
        {
            FillPreview();
        }
    }

    // widget.highlightAttrs: the excerpt as plain runs, the matched words
    // (Core's UTF-16 ranges, sorted and apart) in bold.
    private void FillPreview()
    {
        var inlines = PreviewLabel.Inlines;
        inlines.Clear();
        if (Row is not { } row)
        {
            return;
        }
        var text = row.Preview;
        var at = 0;
        foreach (var r in row.Highlights)
        {
            if (r.Start < at || r.Start + r.Length > text.Length)
            {
                continue;
            }
            if (r.Start > at)
            {
                inlines.Add(new Run { Text = text[at..r.Start] });
            }
            inlines.Add(new Run { Text = text.Substring(r.Start, r.Length), FontWeight = FontWeights.Bold });
            at = r.Start + r.Length;
        }
        if (at < text.Length)
        {
            inlines.Add(new Run { Text = text[at..] });
        }
    }

    private void OnExpanderClick(object sender, RoutedEventArgs e)
    {
        if (Row is { } row && FindPane() is { } pane)
        {
            pane.ToggleThread(row);
        }
    }

    private MessageListPane? FindPane()
    {
        DependencyObject? d = this;
        while (d is not null)
        {
            if (d is MessageListPane pane)
            {
                return pane;
            }
            d = VisualTreeHelper.GetParent(d);
        }
        return null;
    }
}
