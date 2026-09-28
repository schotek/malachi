// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the panel side of macos/Sources/MalachiMail/Compose/
// RecipientSuggestionsController.swift (show's frame under the field,
// hide, the field delegate's doCommandBy keys, controlTextDidEndEditing,
// accept's text and caret); GTK: ui/internal/compose/suggest.go
// (newSuggestions: a popover under the entry that never takes the focus,
// the capture-phase key controller, the focus-leave hide; accept's SetText
// and SetPosition). The logic is Core's SuggestionsController; this is the
// popup (docs/windows-port.md §11.3: not an AutoSuggestBox, which takes the
// focus, preselects nothing and does not accept on Tab).
//
// A Popup constrained to the window, placed under the row and as wide as
// it, holding a SuggestionList whose rows never take the focus: typing goes
// on in the row. The row's PreviewKeyDown (it sees the key before the
// TextBox and before the window's accelerators) hands Down, Up, Enter, Tab
// and Escape to the controller while the popup shows; Shift+Tab is GTK's
// ISO_Left_Tab and goes on. The popup is outside the window's tree, so it
// takes the row's theme when it opens.

using System;
using Malachi.Core.Presentation;
using Microsoft.UI.Input;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Input;
using Windows.UI.Core;
using WinUIKey = Windows.System.VirtualKey;

namespace Malachi.App.Compose;

/// <summary>The recipient popup of one To, Cc or Bcc row.</summary>
internal sealed class RecipientSuggestions : IDisposable
{
    private readonly TextBox field;
    private readonly SuggestionsController controller;
    private readonly SuggestionList list = new();
    private readonly Popup popup;
    private bool disposed;

    /// <summary>Attaches the popup of <paramref name="controller"/> to <paramref name="field"/>.</summary>
    public RecipientSuggestions(TextBox field, SuggestionsController controller)
    {
        ArgumentNullException.ThrowIfNull(field);
        ArgumentNullException.ThrowIfNull(controller);
        this.field = field;
        this.controller = controller;
        popup = new Popup
        {
            Child = list,
            IsLightDismissEnabled = false,
            ShouldConstrainToRootBounds = true,
            PlacementTarget = field,
            DesiredPlacement = PopupPlacementMode.BottomEdgeAlignedLeft,
        };
        field.PreviewKeyDown += OnPreviewKeyDown;
        field.LostFocus += OnLostFocus;
        field.SizeChanged += OnFieldSizeChanged;
        controller.Changed += OnChanged;
        controller.Accepted += OnAccepted;
        list.RowClicked += OnRowClicked;
    }

    /// <summary>The completion's logic.</summary>
    public SuggestionsController Controller => controller;

    /// <summary>Whether the popup shows.</summary>
    public bool IsVisible => controller.IsVisible;

    /// <summary>The window is closing: the popup goes, and nothing arrives late (cleanup).</summary>
    public void Dispose()
    {
        if (disposed)
        {
            return;
        }
        disposed = true;
        field.PreviewKeyDown -= OnPreviewKeyDown;
        field.LostFocus -= OnLostFocus;
        field.SizeChanged -= OnFieldSizeChanged;
        controller.Changed -= OnChanged;
        controller.Accepted -= OnAccepted;
        list.RowClicked -= OnRowClicked;
        popup.IsOpen = false;
        controller.Dispose();
    }

    private static bool IsDown(WinUIKey key) =>
        InputKeyboardSource.GetKeyStateForCurrentThread(key).HasFlag(CoreVirtualKeyStates.Down);

    // show and hide: the popup follows the controller.
    private void OnChanged(object? sender, EventArgs e)
    {
        if (!controller.IsVisible)
        {
            popup.IsOpen = false;
            return;
        }
        if (field.XamlRoot is null)
        {
            return;
        }
        popup.XamlRoot ??= field.XamlRoot;
        list.RequestedTheme = field.ActualTheme;
        list.Width = Math.Max(field.ActualWidth, 160);
        list.Show(controller.Rows, controller.SelectedIndex);
        popup.IsOpen = true;
    }

    // accept: the row gets the new text, the caret after the separator.
    private void OnAccepted(object? sender, SuggestionAcceptance a)
    {
        field.Text = a.Text;
        field.Select(Math.Clamp(a.Caret, 0, a.Text.Length), 0);
    }

    private void OnRowClicked(object? sender, int index) => controller.Accept(index);

    // onKey, before the row and the window's keys see it.
    private void OnPreviewKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (!controller.IsVisible)
        {
            return;
        }
        SuggestionKey? key = e.Key switch
        {
            WinUIKey.Down => SuggestionKey.Down,
            WinUIKey.Up => SuggestionKey.Up,
            WinUIKey.Enter => SuggestionKey.Enter,
            WinUIKey.Tab when !IsDown(WinUIKey.Shift) => SuggestionKey.Tab,
            WinUIKey.Escape => SuggestionKey.Escape,
            _ => null,
        };
        if (key is { } k && controller.OnKey(k, IsDown(WinUIKey.Control) || IsDown(WinUIKey.Menu)))
        {
            e.Handled = true;
        }
    }

    // The focus left the row (focus.ConnectLeave(s.hide)).
    private void OnLostFocus(object sender, RoutedEventArgs e) => controller.Hide();

    private void OnFieldSizeChanged(object sender, SizeChangedEventArgs e)
    {
        if (popup.IsOpen)
        {
            list.Width = Math.Max(field.ActualWidth, 160);
        }
    }
}
