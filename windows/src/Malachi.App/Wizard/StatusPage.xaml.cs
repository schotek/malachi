// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/AccountWizard/WizardWidgets.swift
// (WizardStatusPageView: title, descriptionText, symbolName, setSpinning);
// GTK: Adw.StatusPage (icon-name, paintable with an Adw.SpinnerPaintable,
// title, description, child). The icon is the GTK name, drawn as its
// Segoe Fluent glyph (Resources/Icons). The pages turn the spinner off
// where it is not shown (a hidden page need not animate); it does not
// follow Loaded and Unloaded, whose order WinUI does not keep when the
// wizard's Frame moves a page to its next host.

using Malachi.App.Resources;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Markup;

namespace Malachi.App.Wizard;

/// <summary>Adw.StatusPage: an icon or a spinner, a title, a description and a child.</summary>
[ContentProperty(Name = nameof(Child))]
public sealed partial class StatusPage : UserControl
{
    /// <summary>The GTK icon name of the illustration; empty for none.</summary>
    public static readonly DependencyProperty IconNameProperty = DependencyProperty.Register(
        nameof(IconName), typeof(string), typeof(StatusPage), new PropertyMetadata("", (d, _) => ((StatusPage)d).ShowIllustration()));

    /// <summary>A spinner in the icon's place (Adw.SpinnerPaintable).</summary>
    public static readonly DependencyProperty SpinningProperty = DependencyProperty.Register(
        nameof(Spinning), typeof(bool), typeof(StatusPage), new PropertyMetadata(false, (d, _) => ((StatusPage)d).ShowIllustration()));

    /// <summary>The title.</summary>
    public static readonly DependencyProperty TitleProperty = DependencyProperty.Register(
        nameof(Title), typeof(string), typeof(StatusPage), new PropertyMetadata("", (d, e) => ((StatusPage)d).TitleView.Text = e.NewValue as string ?? ""));

    /// <summary>The description; empty hides it.</summary>
    public static readonly DependencyProperty DescriptionProperty = DependencyProperty.Register(
        nameof(Description), typeof(string), typeof(StatusPage), new PropertyMetadata("", (d, _) => ((StatusPage)d).ShowDescription()));

    /// <summary>What goes under the texts (the buttons of a page, the result rows).</summary>
    public static readonly DependencyProperty ChildProperty = DependencyProperty.Register(
        nameof(Child), typeof(object), typeof(StatusPage), new PropertyMetadata(null, (d, e) => ((StatusPage)d).ShowChild(e.NewValue)));

    /// <summary>An empty status page.</summary>
    public StatusPage()
    {
        InitializeComponent();
    }

    /// <summary>The GTK icon name of the illustration; empty for none.</summary>
    public string IconName
    {
        get => (string)GetValue(IconNameProperty);
        set => SetValue(IconNameProperty, value);
    }

    /// <summary>A spinner in the icon's place.</summary>
    public bool Spinning
    {
        get => (bool)GetValue(SpinningProperty);
        set => SetValue(SpinningProperty, value);
    }

    /// <summary>The title.</summary>
    public string Title
    {
        get => (string)GetValue(TitleProperty);
        set => SetValue(TitleProperty, value);
    }

    /// <summary>The description; empty hides it.</summary>
    public string Description
    {
        get => (string)GetValue(DescriptionProperty);
        set => SetValue(DescriptionProperty, value);
    }

    /// <summary>What goes under the texts.</summary>
    public object? Child
    {
        get => GetValue(ChildProperty);
        set => SetValue(ChildProperty, value);
    }

    private void ShowIllustration()
    {
        var spinning = Spinning;
        Spinner.Visibility = spinning ? Visibility.Visible : Visibility.Collapsed;
        Spinner.IsActive = spinning;
        var icon = !spinning && !string.IsNullOrEmpty(IconName);
        IconView.Visibility = icon ? Visibility.Visible : Visibility.Collapsed;
        if (icon)
        {
            IconView.FontFamily = Icons.SymbolFont;
            IconView.Glyph = Icons.Glyph(IconName);
        }
    }

    private void ShowDescription()
    {
        var text = Description ?? "";
        DescriptionView.Text = text;
        DescriptionView.Visibility = text.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        UpdateSpacing();
    }

    private void ShowChild(object? child)
    {
        ChildView.Content = child;
        UpdateSpacing();
    }

    // statuspage .title:not(:last-child) 12 px, .description:not(:last-child) 36 px.
    private void UpdateSpacing()
    {
        var child = ChildView.Content is not null;
        DescriptionView.Margin = new Thickness(0, 0, 0, child ? 36 : 0);
        TitleView.Margin = new Thickness(0, 0, 0, DescriptionView.Visibility == Visibility.Visible || child ? 12 : 0);
    }
}
