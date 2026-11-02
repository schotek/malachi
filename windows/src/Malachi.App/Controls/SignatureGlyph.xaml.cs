// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only view of ui/internal/signature (Icon*, Style*; the table is
// Core's IconGlyphs and SignatureView.StyleOf). The shield of a verdict as
// two dependency properties, for x:Bind from a row or code: the GTK icon name
// and the style class of the verdict's colour. The class picks the visual
// state (SignatureGlyph.xaml), so a change of theme repaints it; hidden while
// the icon name is empty.

using Malachi.App.Resources;
using Malachi.Core.Signatures;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Controls;

/// <summary>The shield of a signed message's verdict.</summary>
public sealed partial class SignatureGlyph : UserControl
{
    /// <summary>The GTK icon name (<see cref="SignatureView.IconOf"/>); empty hides the glyph.</summary>
    public static readonly DependencyProperty IconNameProperty = DependencyProperty.Register(
        nameof(IconName), typeof(string), typeof(SignatureGlyph), new PropertyMetadata("", OnChanged));

    /// <summary>The style class of the colour (<see cref="SignatureView.StyleOf"/>: success, warning, error or dim-label).</summary>
    public static readonly DependencyProperty ToneProperty = DependencyProperty.Register(
        nameof(Tone), typeof(string), typeof(SignatureGlyph), new PropertyMetadata("", OnChanged));

    /// <summary>The size of the glyph, 14 as the star of a list row.</summary>
    public static readonly DependencyProperty GlyphSizeProperty = DependencyProperty.Register(
        nameof(GlyphSize), typeof(double), typeof(SignatureGlyph), new PropertyMetadata(14.0, OnChanged));

    /// <summary>A hidden glyph.</summary>
    public SignatureGlyph()
    {
        InitializeComponent();
        Apply();
    }

    /// <summary>The GTK icon name; empty hides the glyph.</summary>
    public string IconName
    {
        get => (string)GetValue(IconNameProperty);
        set => SetValue(IconNameProperty, value);
    }

    /// <summary>The style class of the colour.</summary>
    public string Tone
    {
        get => (string)GetValue(ToneProperty);
        set => SetValue(ToneProperty, value);
    }

    /// <summary>The size of the glyph.</summary>
    public double GlyphSize
    {
        get => (double)GetValue(GlyphSizeProperty);
        set => SetValue(GlyphSizeProperty, value);
    }

    private static void OnChanged(DependencyObject d, DependencyPropertyChangedEventArgs e) => ((SignatureGlyph)d).Apply();

    private void Apply()
    {
        var name = IconName ?? "";
        Visibility = name.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        Shield.Glyph = name.Length > 0 ? Icons.Glyph(name) : "";
        Shield.FontSize = GlyphSize;
        // The half shield of a key not confirmed: the same glyph, fainter.
        Shield.Opacity = name == SignatureView.IconGoodSeen ? 0.6 : 1;
        VisualStateManager.GoToState(this, (Tone ?? "") switch
        {
            SignatureView.StyleGood => "Success",
            SignatureView.StyleWarning => "Warning",
            SignatureView.StyleBad => "Critical",
            _ => "Neutral",
        }, false);
    }
}
