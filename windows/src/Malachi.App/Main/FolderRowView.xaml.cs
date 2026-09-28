// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the sidebar rows of ui/internal/window/folders.go and
// favourites.go (the twisty's and the star's clicks: toggleFolder,
// toggleAccount, toggleFavourite) and of the star's CSS in
// ui/internal/style (opacity 0 until the row is hovered or has the keyboard
// focus, a filled star in the tree always); macOS:
// Sidebar/FolderCellView.swift (the star also on the selected row, a
// 150 ms fade). The view of Core's SidebarRow (FolderRowView.xaml); its
// buttons take the click themselves, so pressing them does not select the
// row, and hand it to the sidebar. The star's opacity is always the local
// value it ends at, kept beside it (the property reads the fade's current
// value while one runs); the fade is one storyboard per row that runs over
// it and lets go when it ends (FillBehavior.Stop), so no finished fade holds
// an old value over a later change.

using System.ComponentModel;
using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Media.Animation;

namespace Malachi.App.Main;

/// <summary>One row of the folder sidebar.</summary>
public sealed partial class FolderRowView : UserControl
{
    /// <summary>The row it shows.</summary>
    public static readonly DependencyProperty RowProperty = DependencyProperty.Register(
        nameof(Row), typeof(SidebarRow), typeof(FolderRowView), new PropertyMetadata(null, OnRowChanged));

    private readonly Storyboard fade;
    private readonly DoubleAnimation fadeAnimation;
    private SelectorItem? container;
    private double starTarget;
    private long selectedToken;
    private bool hovered;
    private bool focusWithin;

    /// <summary>An empty row.</summary>
    public FolderRowView()
    {
        InitializeComponent();
        Star.Opacity = 0;
        fadeAnimation = new DoubleAnimation { Duration = new Duration(System.TimeSpan.FromMilliseconds(150)) };
        Storyboard.SetTarget(fadeAnimation, Star);
        Storyboard.SetTargetProperty(fadeAnimation, nameof(Opacity));
        fade = new Storyboard { FillBehavior = FillBehavior.Stop };
        fade.Children.Add(fadeAnimation);
        RowRoot.PointerEntered += (_, _) => SetHovered(true);
        RowRoot.PointerExited += (_, _) => SetHovered(false);
        RowRoot.PointerCanceled += (_, _) => SetHovered(false);
        Loaded += OnLoaded;
        Unloaded += OnUnloaded;
    }

    /// <summary>The row it shows.</summary>
    public SidebarRow? Row
    {
        get => (SidebarRow?)GetValue(RowProperty);
        set => SetValue(RowProperty, value);
    }

    /// <summary>A folder's indent; a heading 3 lower (folderHeadingGap).</summary>
    public static Thickness RowMargin(double indent, bool heading) => new(indent, heading ? SidebarRow.HeadingGap : 0, 0, 0);

    /// <summary>dim-label on a container that cannot be opened.</summary>
    public static double DimOpacity(bool dimmed) => dimmed ? 0.55 : 1;

    /// <summary>A heading's caption, or a folder's name.</summary>
    public Style TitleStyle(bool heading) => (Style)Resources[heading ? "FolderHeadingStyle" : "FolderTitleStyle"];

    private static void OnRowChanged(DependencyObject d, DependencyPropertyChangedEventArgs e)
    {
        var view = (FolderRowView)d;
        if (e.OldValue is SidebarRow old)
        {
            old.PropertyChanged -= view.OnRowPropertyChanged;
        }
        if (e.NewValue is SidebarRow row)
        {
            row.PropertyChanged += view.OnRowPropertyChanged;
        }
        view.UpdateStar(animated: false);
    }

    private void OnRowPropertyChanged(object? sender, PropertyChangedEventArgs e)
    {
        if (e.PropertyName == nameof(SidebarRow.StarPinned))
        {
            UpdateStar(animated: false);
        }
    }

    // The row's container: its selection shows the star too.
    private void OnLoaded(object sender, RoutedEventArgs e)
    {
        DependencyObject? d = this;
        while (d is not null and not SelectorItem)
        {
            d = VisualTreeHelper.GetParent(d);
        }
        if (d is SelectorItem item && !ReferenceEquals(item, container))
        {
            Detach();
            container = item;
            selectedToken = item.RegisterPropertyChangedCallback(SelectorItem.IsSelectedProperty, (_, _) => UpdateStar(animated: true));
            item.GotFocus += OnContainerFocus;
            item.LostFocus += OnContainerFocus;
        }
        UpdateStar(animated: false);
    }

    private void OnUnloaded(object sender, RoutedEventArgs e) => Detach();

    private void Detach()
    {
        if (container is { } item)
        {
            item.UnregisterPropertyChangedCallback(SelectorItem.IsSelectedProperty, selectedToken);
            item.GotFocus -= OnContainerFocus;
            item.LostFocus -= OnContainerFocus;
        }
        container = null;
        focusWithin = false;
        hovered = false;
    }

    private void OnContainerFocus(object sender, RoutedEventArgs e)
    {
        focusWithin = container?.FocusState is FocusState.Keyboard || Star.FocusState != FocusState.Unfocused;
        UpdateStar(animated: true);
    }

    private void OnStarFocus(object sender, RoutedEventArgs e)
    {
        focusWithin = Star.FocusState != FocusState.Unfocused || container?.FocusState is FocusState.Keyboard;
        UpdateStar(animated: true);
    }

    private void SetHovered(bool on)
    {
        hovered = on;
        UpdateStar(animated: true);
    }

    // style.go: opacity rather than visibility, so nothing moves under the
    // pointer; a 150 ms fade. A change while a fade runs stops it: the new
    // value shows at once, or fades from where the running fade got to.
    private void UpdateStar(bool animated)
    {
        var show = Row is { } row && (row.StarPinned || hovered || focusWithin || container?.IsSelected == true);
        var target = show ? 1.0 : 0.0;
        if (target == starTarget)
        {
            // There already, or fading there.
            return;
        }
        var from = Star.Opacity;
        starTarget = target;
        fade.Stop();
        Star.Opacity = target;
        if (animated && from != target)
        {
            fadeAnimation.From = from;
            fadeAnimation.To = target;
            fade.Begin();
        }
    }

    private void OnTwistyClick(object sender, RoutedEventArgs e)
    {
        if (Row is { } row && FindPane() is { } pane)
        {
            pane.ToggleFold(row);
        }
    }

    private void OnStarClick(object sender, RoutedEventArgs e)
    {
        if (Row is { } row && FindPane() is { } pane)
        {
            pane.ToggleFavourite(row);
        }
    }

    private SidebarPane? FindPane()
    {
        DependencyObject? d = this;
        while (d is not null)
        {
            if (d is SidebarPane pane)
            {
                return pane;
            }
            d = VisualTreeHelper.GetParent(d);
        }
        return null;
    }
}
