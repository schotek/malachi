// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ui/internal/window/bulk.go (showBulk, bindBulk); macOS: the strip of
// MessageView/ (the Swift side is the other client's port of the same Go
// reference). The view of Core's
// BulkStrip (Malachi.Core.Bulk): the owner (the reading pane, a message
// window, a card of the conversation view) hands it the strip and whether a
// request waits, and acts on ActionClicked. The button takes no focus on a
// click (GTK SetFocusOnClick(false)): the strip changes when the request is
// done, and a click leaves the focus where it was; the keyboard still
// reaches it. Plain text only.

using System;
using Malachi.App.Localization;
using Malachi.App.Resources;
using Malachi.Core.Bulk;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Reader;

/// <summary>The strip above a bulk message: what it is, and the button that unsubscribes.</summary>
public sealed partial class BulkStripView : UserControl
{
    /// <summary>The space around the strip's content (the remote-image bars' 12 and 6; a card's 13 and 6).</summary>
    public static readonly DependencyProperty BarPaddingProperty = DependencyProperty.Register(
        nameof(BarPadding), typeof(Thickness), typeof(BulkStripView), new PropertyMetadata(new Thickness(12, 6, 12, 6), OnBarPaddingChanged));

    /// <summary>A hidden strip.</summary>
    public BulkStripView()
    {
        InitializeComponent();
    }

    /// <summary>The space around the strip's content.</summary>
    public Thickness BarPadding
    {
        get => (Thickness)GetValue(BarPaddingProperty);
        set => SetValue(BarPaddingProperty, value);
    }

    /// <summary>The button was clicked: the owner asks (<see cref="Malachi.Core.Controllers.BulkActionsController.Unsubscribe"/>).</summary>
    public event EventHandler? ActionClicked;

    /// <summary>Whether the keyboard focus is in the strip (its button).</summary>
    public bool OwnsFocus
    {
        get
        {
            if (XamlRoot is null || FocusManager.GetFocusedElement(XamlRoot) is not DependencyObject focused)
            {
                return false;
            }
            for (var e = focused; e is not null; e = VisualTreeHelper.GetParent(e))
            {
                if (ReferenceEquals(e, this))
                {
                    return true;
                }
            }
            return false;
        }
    }

    /// <summary>
    /// Whether <paramref name="strip"/> leaves the strip without its button
    /// (hidden, or no offer): the owner takes the focus out of it first, for
    /// the reason given in newMessageView, whatever it would hand it on to.
    /// </summary>
    public static bool LosesButton(BulkStrip strip)
    {
        ArgumentNullException.ThrowIfNull(strip);
        return !strip.Visible || strip.Action.Length == 0;
    }

    /// <summary>
    /// showBulk: shows <paramref name="strip"/> (hidden when it has none);
    /// <paramref name="busy"/>: a request is on its way and the button waits
    /// for it.
    /// </summary>
    public void Show(BulkStrip strip, bool busy)
    {
        ArgumentNullException.ThrowIfNull(strip);
        Visibility = strip.Visible ? Visibility.Visible : Visibility.Collapsed;
        if (!strip.Visible)
        {
            return;
        }
        Label.Text = strip.Text;
        KindIcon.Glyph = Icons.Glyph(BulkReading.IconName(strip.Kind));
        KindIcon.FontFamily = Icons.SymbolFont;
        VisualStateManager.GoToState(this, strip.Warning ? "Warning" : "Quiet", false);
        ActionButton.Visibility = strip.Action.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        MnemonicLabel.Apply(ActionButton, strip.Action);
        ActionButton.IsEnabled = !busy;
    }

    private static void OnBarPaddingChanged(DependencyObject d, DependencyPropertyChangedEventArgs e) =>
        ((BulkStripView)d).Bar.Padding = (Thickness)e.NewValue;

    private void OnActionClick(object sender, RoutedEventArgs e) => ActionClicked?.Invoke(this, EventArgs.Empty);
}
