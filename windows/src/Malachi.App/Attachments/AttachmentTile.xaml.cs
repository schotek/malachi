// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Shared/AttachmentTileView.swift (the
// tile: drawing, mouse, keyboard, accessibility); GTK: attachments.go
// buildChip and compose.go addChip follow. A click on the card raises
// Clicked, a right-click (and Shift+F10 or the Menu key) shows the menu
// ContextMenu builds; the card takes the focus from the keyboard, and Space
// or Enter click it. The trailing button is the caller's (the arrow of the
// reader's menu, the ✕ of a compose window): its own clicks never reach
// the card. A tile that cannot be used is dimmed and ignores clicks, but
// keeps its tooltip (the reason), where a disabled control would show
// none. Names are server data and are shown as plain text: the name's
// middle is cut to the width (ChipText.MiddleEllipsisToFit, where macOS
// truncates the middle of its label), so the extension stays visible; the
// whole name is the tooltip unless the caller sets one.

using System;
using Malachi.App.Resources;
using Malachi.Core.Presentation;
using Microsoft.UI.Input;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Automation.Provider;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Input;
using Windows.Foundation;

namespace Malachi.App.Attachments;

/// <summary>One attachment as a tile.</summary>
public sealed partial class AttachmentTile : UserControl
{
    /// <summary>The size of the icon (AttachmentTileView.iconSize).</summary>
    public const double IconSize = 32;

    // A disabled tile's opacity (AttachmentTileView.isEnabled).
    private const double DimmedOpacity = 0.55;

    private readonly TextBlock measure = new();
    private string name = "";
    private Button? accessory;
    private bool available = true;
    private bool hovering;
    private bool pressed;

    /// <summary>An empty tile; <see cref="Set"/> fills it.</summary>
    public AttachmentTile()
    {
        InitializeComponent();
        Texts.SizeChanged += (_, _) => FitName();
        PointerEntered += (_, _) => SetPointer(hover: true, press: pressed);
        PointerExited += (_, _) => SetPointer(hover: false, press: false);
        PointerCaptureLost += (_, _) => SetPointer(hovering, press: false);
        PointerPressed += OnPointerPressed;
        PointerReleased += OnPointerReleased;
        ContextRequested += OnContextRequested;
        KeyDown += OnKeyDown;
    }

    /// <summary>A click on the card (or Space, Enter, Invoke); null leaves the card inert, without hover.</summary>
    public event EventHandler? Clicked;

    /// <summary>The menu of a right-click, built anew each time; null for none.</summary>
    public Func<MenuFlyout>? ContextMenu { get; set; }

    /// <summary>The trailing button, null for none.</summary>
    public Button? Accessory => accessory;

    /// <summary>
    /// Whether the tile can be used: a tile that cannot is dimmed, takes
    /// neither the focus nor clicks, and its trailing button is disabled.
    /// </summary>
    public bool Available
    {
        get => available;
        set
        {
            available = value;
            Opacity = value ? 1 : DimmedOpacity;
            IsTabStop = value;
            if (accessory is not null)
            {
                accessory.IsEnabled = value;
            }
            if (!value)
            {
                SetPointer(hover: false, press: false);
            }
        }
    }

    /// <summary>
    /// Fills the tile: <paramref name="icon"/> (32 px), the whole
    /// <paramref name="fileName"/>, <paramref name="size"/> ("" hides it)
    /// and the trailing <paramref name="trailing"/> button. The name is the
    /// tooltip and, with the size, what Narrator reads.
    /// </summary>
    public void Set(FrameworkElement icon, string fileName, string size, Button? trailing)
    {
        ArgumentNullException.ThrowIfNull(icon);
        ArgumentNullException.ThrowIfNull(fileName);
        ArgumentNullException.ThrowIfNull(size);
        IconSlot.Child = icon;
        name = fileName;
        NameText.Text = fileName;
        SizeText.Text = size;
        SizeText.Visibility = size.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        accessory = trailing;
        AccessorySlot.Child = trailing;
        AccessorySlot.Visibility = trailing is null ? Visibility.Collapsed : Visibility.Visible;
        Texts.Margin = new Thickness(8, 0, trailing is null ? 8 : 4, 0);
        if (trailing is not null)
        {
            trailing.IsEnabled = available;
        }
        Tooltip = fileName;
        AutomationProperties.SetName(this, size.Length > 0 ? fileName + ", " + size : fileName);
        FitName();
    }

    /// <summary>The tooltip of the card (the whole name, or why it cannot be used); null for none.</summary>
    public string? Tooltip
    {
        set
        {
            ToolTipService.SetToolTip(this, string.IsNullOrEmpty(value) ? null : value);
            AutomationProperties.SetHelpText(this, value ?? "");
        }
    }

    /// <summary>Adds <paramref name="e"/> after the size (the server symbol or a spinner).</summary>
    public void AddDetail(FrameworkElement e)
    {
        ArgumentNullException.ThrowIfNull(e);
        DetailRow.Children.Add(e);
    }

    /// <summary>
    /// The trailing button of a tile: borderless, a glyph of 12 px in the
    /// secondary colour, <paramref name="label"/> its tooltip and name.
    /// </summary>
    public static Button AccessoryButton(string iconName, string label)
    {
        var button = new Button
        {
            Width = 24,
            Height = 24,
            Padding = new Thickness(0),
            Content = Icons.Create(iconName, 12),
            Style = (Style)Application.Current.Resources["SubtleButtonStyle"],
        };
        ToolTipService.SetToolTip(button, label);
        AutomationProperties.SetName(button, label);
        return button;
    }

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new TilePeer(this);

    private bool Clickable => available && Clicked is not null;

    private void Click()
    {
        if (Clickable)
        {
            Clicked?.Invoke(this, EventArgs.Empty);
        }
    }

    // The name's middle cut to the width the texts have.
    private void FitName()
    {
        var width = Texts.ActualWidth;
        if (width <= 0 || name.Length == 0)
        {
            return;
        }
        measure.FontFamily = NameText.FontFamily;
        measure.FontSize = NameText.FontSize;
        measure.FontWeight = NameText.FontWeight;
        NameText.Text = ChipText.MiddleEllipsisToFit(name, width, Measure);
    }

    private double Measure(string text)
    {
        measure.Text = text;
        measure.Measure(new Size(double.PositiveInfinity, double.PositiveInfinity));
        return measure.DesiredSize.Width;
    }

    private void SetPointer(bool hover, bool press)
    {
        hovering = hover;
        pressed = press;
        var tint = Clickable;
        HoverTint.Opacity = tint && hover && !press ? 1 : 0;
        PressTint.Opacity = tint && press ? 1 : 0;
    }

    private void OnPointerPressed(object sender, PointerRoutedEventArgs e)
    {
        if (!Clickable || !e.GetCurrentPoint(this).Properties.IsLeftButtonPressed)
        {
            return;
        }
        CapturePointer(e.Pointer);
        SetPointer(hover: true, press: true);
        e.Handled = true;
    }

    private void OnPointerReleased(object sender, PointerRoutedEventArgs e)
    {
        if (!pressed)
        {
            return;
        }
        var at = e.GetCurrentPoint(this).Position;
        var inside = at.X >= 0 && at.Y >= 0 && at.X <= ActualWidth && at.Y <= ActualHeight;
        ReleasePointerCapture(e.Pointer);
        SetPointer(hover: inside && e.Pointer.PointerDeviceType == PointerDeviceType.Mouse, press: false);
        e.Handled = true;
        if (inside)
        {
            Click();
        }
    }

    private void OnContextRequested(UIElement sender, ContextRequestedEventArgs e)
    {
        if (!available || ContextMenu?.Invoke() is not { } menu)
        {
            return;
        }
        e.Handled = true;
        if (e.TryGetPosition(this, out var at))
        {
            menu.ShowAt(this, new FlyoutShowOptions { Position = at });
        }
        else
        {
            menu.ShowAt(this);
        }
    }

    private void OnKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key is Windows.System.VirtualKey.Space or Windows.System.VirtualKey.Enter && ReferenceEquals(e.OriginalSource, this) && Clickable)
        {
            e.Handled = true;
            Click();
        }
    }

    // Narrator and UI Automation see a button that can be invoked.
    private sealed partial class TilePeer(AttachmentTile owner) : FrameworkElementAutomationPeer(owner), IInvokeProvider
    {
        public void Invoke() => owner.Click();

        protected override object? GetPatternCore(PatternInterface patternInterface) =>
            patternInterface == PatternInterface.Invoke && owner.Clicked is not null ? this : base.GetPatternCore(patternInterface);

        protected override AutomationControlType GetAutomationControlTypeCore() => AutomationControlType.Button;

        protected override string GetClassNameCore() => nameof(AttachmentTile);

        protected override bool IsEnabledCore() => owner.available;

        protected override bool IsControlElementCore() => true;
    }
}
