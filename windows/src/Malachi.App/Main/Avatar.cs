// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageList/AvatarView.swift
// (AvatarView); GTK: Adw.Avatar of ui/data/ui/message_row.blp (size 40,
// show-initials) and ui/internal/style's monochrome-avatars rule. A disc
// with the gradient of the sender's colour class and the initials of the
// name, or the person symbol at half the size without a name; the
// monochrome variant is the text colour at 12 % with the initials at 80 %.
// The palette and the initials are Core's (AvatarPalette). Nothing is
// fetched; the name is hostile input, only ever set as a TextBlock's text.
// Decoration: the row's name says who wrote, so a screen reader skips it.

using Malachi.App.Resources;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Shapes;
using Windows.Foundation;
using Windows.UI;

namespace Malachi.App.Main;

/// <summary>A sender's avatar (Adw.Avatar).</summary>
public sealed partial class Avatar : UserControl
{
    /// <summary>The name the colour and the initials come from.</summary>
    public static readonly DependencyProperty TextProperty = DependencyProperty.Register(
        nameof(Text), typeof(string), typeof(Avatar), new PropertyMetadata("", (d, _) => ((Avatar)d).Redraw()));

    /// <summary>The diameter.</summary>
    public static readonly DependencyProperty SizeProperty = DependencyProperty.Register(
        nameof(Size), typeof(double), typeof(Avatar), new PropertyMetadata(40.0, (d, _) => ((Avatar)d).Redraw()));

    /// <summary>The neutral variant.</summary>
    public static readonly DependencyProperty MonochromeProperty = DependencyProperty.Register(
        nameof(Monochrome), typeof(bool), typeof(Avatar), new PropertyMetadata(false, (d, _) => ((Avatar)d).Redraw()));

    private readonly Ellipse disc = new();
    private readonly TextBlock initials = new()
    {
        HorizontalAlignment = HorizontalAlignment.Center,
        VerticalAlignment = VerticalAlignment.Center,
        FontWeight = Microsoft.UI.Text.FontWeights.Bold,
        TextLineBounds = TextLineBounds.Tight,
        IsTextScaleFactorEnabled = false,
    };

    private readonly FontIcon person = new()
    {
        Glyph = Icons.Glyph("avatar-default-symbolic"),
        HorizontalAlignment = HorizontalAlignment.Center,
        VerticalAlignment = VerticalAlignment.Center,
    };

    /// <summary>An avatar of 40 without a name.</summary>
    public Avatar()
    {
        IsTabStop = false;
        AutomationProperties.SetAccessibilityView(this, AccessibilityView.Raw);
        person.FontFamily = Icons.SymbolFont;
        AutomationProperties.SetAccessibilityView(initials, AccessibilityView.Raw);
        AutomationProperties.SetAccessibilityView(person, AccessibilityView.Raw);
        var root = new Grid();
        root.Children.Add(disc);
        root.Children.Add(initials);
        root.Children.Add(person);
        Content = root;
        ActualThemeChanged += (_, _) => Redraw();
        Redraw();
    }

    /// <summary>The name the colour and the initials come from.</summary>
    public string Text
    {
        get => (string)GetValue(TextProperty);
        set => SetValue(TextProperty, value ?? "");
    }

    /// <summary>The diameter.</summary>
    public double Size
    {
        get => (double)GetValue(SizeProperty);
        set => SetValue(SizeProperty, value);
    }

    /// <summary>The neutral variant.</summary>
    public bool Monochrome
    {
        get => (bool)GetValue(MonochromeProperty);
        set => SetValue(MonochromeProperty, value);
    }

    private static Color Rgb(uint v) => Color.FromArgb(0xff, (byte)(v >> 16), (byte)(v >> 8), (byte)v);

    private void Redraw()
    {
        var size = Size;
        Width = size;
        Height = size;
        disc.Width = size;
        disc.Height = size;
        var text = Text ?? "";
        Brush foreground;
        if (Monochrome)
        {
            // alpha(@window_fg_color, …): the text colour of the theme.
            var fg = ActualTheme == ElementTheme.Dark ? Color.FromArgb(0xff, 0xff, 0xff, 0xff) : Color.FromArgb(0xff, 0, 0, 0);
            disc.Fill = new SolidColorBrush(fg) { Opacity = AvatarPalette.MonochromeFillOpacity };
            foreground = new SolidColorBrush(fg) { Opacity = AvatarPalette.MonochromeTextOpacity };
        }
        else
        {
            var colours = AvatarPalette.ColoursOf(text);
            var gradient = new LinearGradientBrush { StartPoint = new Point(0.5, 0), EndPoint = new Point(0.5, 1) };
            gradient.GradientStops.Add(new GradientStop { Color = Rgb(colours.Top), Offset = 0 });
            gradient.GradientStops.Add(new GradientStop { Color = Rgb(colours.Bottom), Offset = 1 });
            disc.Fill = gradient;
            foreground = new SolidColorBrush(Rgb(colours.Foreground));
        }
        var letters = AvatarPalette.Initials(text);
        initials.Text = letters;
        initials.FontSize = AvatarPalette.FontSize(size);
        initials.Foreground = foreground;
        initials.Visibility = letters.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        person.FontSize = AvatarPalette.SymbolSize(size);
        person.Foreground = foreground;
        person.Visibility = letters.Length > 0 ? Visibility.Collapsed : Visibility.Visible;
    }
}
