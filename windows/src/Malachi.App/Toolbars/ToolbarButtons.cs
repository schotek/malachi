// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/plans/toolbar-customization.md W.5): the icon
// buttons a bar makes in code for the catalog's items it has no control of
// its own for (Contacts, the sidebar toggle, and an item of Mail's sidebar
// and list sections in the other of the two), in the bars' look: a subtle
// 40×32 button with a 16 DIP glyph, named by its label, which is also its
// tooltip.

using System;
using Malachi.App.Resources;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Toolbars;

/// <summary>Makes the bars' icon buttons.</summary>
internal static class ToolbarButtons
{
    /// <summary>A subtle icon button with <paramref name="glyph"/> (a GTK icon name), named and tooltipped <paramref name="label"/>.</summary>
    public static Button Make(string glyph, string label, string automationId, double width = 40)
    {
        var button = new Button
        {
            Width = width,
            Height = 32,
            Padding = new Thickness(0),
            VerticalAlignment = VerticalAlignment.Center,
            Content = Icons.Create(glyph, 16),
        };
        if (Application.Current.Resources.TryGetValue("SubtleButtonStyle", out var style) && style is Style s)
        {
            button.Style = s;
        }
        AutomationProperties.SetAutomationId(button, automationId);
        Name(button, label);
        return button;
    }

    /// <summary>Gives <paramref name="control"/> <paramref name="label"/> as its name and tooltip.</summary>
    public static void Name(FrameworkElement control, string label)
    {
        ArgumentNullException.ThrowIfNull(control);
        AutomationProperties.SetName(control, label);
        ToolTipService.SetToolTip(control, label);
    }
}
