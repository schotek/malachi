// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/Actions.swift (mn, the stripping of
// the GTK mnemonic marker); GTK: use-underline on buttons and menu items.
// Windows keeps the marked letter as the control's AccessKey (Alt shows
// the key tips, Alt+letter invokes; docs/windows-port.md §9): the attached
// property takes the translated label with its "_" and sets the text
// without it plus the key, on whatever the control shows its text in.
//
//     <Button l:MnemonicLabel.Text="{l:T Msgid='_Add Account…'}" />
//     MnemonicLabel.SetText(item, L10n.T("_Preferences"));

using Malachi.Core.Presentation;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Localization;

/// <summary>Sets a GTK label with its mnemonic on a control: its text, and its access key.</summary>
public static class MnemonicLabel
{
    /// <summary>The label, with the GTK mnemonic marker.</summary>
    public static readonly DependencyProperty TextProperty = DependencyProperty.RegisterAttached(
        "Text", typeof(string), typeof(MnemonicLabel), new PropertyMetadata(null, OnTextChanged));

    /// <summary>The label set on <paramref name="element"/>.</summary>
    public static string? GetText(DependencyObject element) => (string?)element?.GetValue(TextProperty);

    /// <summary>Sets <paramref name="label"/> (with its "_") on <paramref name="element"/>.</summary>
    public static void SetText(DependencyObject element, string? label) => element?.SetValue(TextProperty, label);

    /// <summary>Applies <paramref name="label"/> to <paramref name="element"/> now.</summary>
    public static void Apply(DependencyObject element, string? label)
    {
        var m = Mnemonic.Parse(label ?? "");
        switch (element)
        {
            case MenuFlyoutItem item:
                item.Text = m.Label;
                break;
            case MenuFlyoutSubItem sub:
                sub.Text = m.Label;
                break;
            case AppBarButton appBar:
                appBar.Label = m.Label;
                break;
            case AppBarToggleButton appBarToggle:
                appBarToggle.Label = m.Label;
                break;
            case SelectorBarItem selector:
                selector.Text = m.Label;
                break;
            case ContentControl content:
                // Button, ToggleButton, CheckBox, RadioButton, HyperlinkButton:
                // a string is shown by a TextBlock's Text, never parsed.
                content.Content = m.Label;
                break;
            case TextBlock text:
                text.Text = m.Label;
                return;
            default:
                return;
        }
        if (element is UIElement ui)
        {
            ui.AccessKey = m.AccessKey ?? "";
        }
    }

    private static void OnTextChanged(DependencyObject element, DependencyPropertyChangedEventArgs e) =>
        Apply(element, e.NewValue as string);
}
