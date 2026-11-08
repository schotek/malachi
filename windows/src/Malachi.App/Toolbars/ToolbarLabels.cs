// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/plans/toolbar-customization.md W.4, O4, T9): the
// display mode of the toolbars, toolbar-display-mode, one for every bar, as
// a shared presentation of the icon buttons' content. Icons Only is the
// buttons as they are (their FontIcon); Icons and Text puts the label
// beside the icon (the bars keep their height; Q-W9);
// Text Only shows the label without the glyph. A button with a label of its
// own already (Today, the Board's text buttons, the views) is not
// registered and stays as it is in every mode, as do the title bar's
// switch and box. The icon element stays the button's own, so code that
// sets its glyph (Reply or Comment, the star) still reaches it; the labels
// are read again on Refresh, after such a change.

using System;
using System.Collections.Generic;
using Malachi.Core.Toolbars;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Toolbars;

/// <summary>The icon buttons of one bar and the display mode they show.</summary>
internal sealed class ToolbarLabels
{
    private readonly List<Entry> entries = [];

    /// <summary>The mode shown.</summary>
    public Toolbar.DisplayMode Mode { get; private set; } = Toolbar.DisplayMode.Icon;

    /// <summary>
    /// Adds <paramref name="button"/>, whose content is its icon, with the
    /// label <paramref name="label"/> gives (read again on every Refresh).
    /// </summary>
    public void Register(ContentControl button, Func<string> label)
    {
        ArgumentNullException.ThrowIfNull(button);
        ArgumentNullException.ThrowIfNull(label);
        if (button.Content is not UIElement icon)
        {
            return;
        }
        var e = new Entry(button, label, icon, button.Width, button.Padding, new TextBlock { VerticalAlignment = VerticalAlignment.Center });
        entries.Add(e);
        Show(e);
    }

    /// <summary>Shows <paramref name="mode"/> on every button.</summary>
    public void Apply(Toolbar.DisplayMode mode)
    {
        Mode = mode;
        foreach (var e in entries)
        {
            Show(e);
        }
    }

    /// <summary>Reads the labels again (Reply became Comment, Star became Unstar).</summary>
    public void Refresh()
    {
        foreach (var e in entries)
        {
            e.Text.Text = e.Label();
        }
    }

    private void Show(Entry e)
    {
        e.Text.Text = e.Label();
        // The icon and the text leave whatever held them before.
        e.Button.Content = null;
        e.Panel.Children.Clear();
        switch (Mode)
        {
            case Toolbar.DisplayMode.IconAndLabel:
                e.Panel.Children.Add(e.Icon);
                e.Panel.Children.Add(e.Text);
                e.Button.Content = e.Panel;
                break;
            case Toolbar.DisplayMode.Label:
                e.Button.Content = e.Text;
                break;
            default:
                e.Button.Content = e.Icon;
                break;
        }
        var labelled = Mode != Toolbar.DisplayMode.Icon;
        e.Button.Width = labelled ? double.NaN : e.Width;
        e.Button.Padding = labelled ? new Thickness(8, 0, 8, 0) : e.Padding;
    }

    private sealed record Entry(ContentControl Button, Func<string> Label, UIElement Icon, double Width, Thickness Padding, TextBlock Text)
    {
        public StackPanel Panel { get; } = new() { Orientation = Orientation.Horizontal, Spacing = 6 };
    }
}
