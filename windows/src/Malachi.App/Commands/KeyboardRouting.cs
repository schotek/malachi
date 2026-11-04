// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §11.5): what the CommandRouter
// needs to know about the focused element. A text input types single keys
// (GTK setTypingAccels lifts Delete, a, j, u and s while the search entry
// has the keyboard; macOS refuses bareKeyActions while a text view is
// first responder), and the compose editor, a WebView2 the router cannot
// look into, is one: its window marks it with IsEditor, so that the single
// keys type there and Escape stays with the editor's bridge.
//
//     <WebView2 cmd:KeyboardRouting.IsEditor="True" />

using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Commands;

/// <summary>Marks elements for the command router.</summary>
public static class KeyboardRouting
{
    /// <summary>The element is an editor the router cannot see into (the compose WebView2).</summary>
    public static readonly DependencyProperty IsEditorProperty = DependencyProperty.RegisterAttached(
        "IsEditor", typeof(bool), typeof(KeyboardRouting), new PropertyMetadata(false));

    /// <summary>Whether <paramref name="element"/> is marked as an editor.</summary>
    public static bool GetIsEditor(DependencyObject element) => element is not null && (bool)element.GetValue(IsEditorProperty);

    /// <summary>Marks <paramref name="element"/> as an editor, or not.</summary>
    public static void SetIsEditor(DependencyObject element, bool value) => element?.SetValue(IsEditorProperty, value);

    /// <summary>Whether <paramref name="focused"/> takes typed text (single keys type there).</summary>
    public static bool IsTextInput(object? focused) =>
        focused is TextBox or PasswordBox or RichEditBox or AutoSuggestBox or NumberBox
        || (focused is DependencyObject d && GetIsEditor(d));

    /// <summary>Whether <paramref name="focused"/> is a marked editor.</summary>
    public static bool IsEditor(object? focused) => focused is DependencyObject d && GetIsEditor(d);
}
