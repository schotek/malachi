// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Shared/StatusPageView.swift and
// Sidebar/SidebarStatusView.swift; GTK: Adw.StatusPage and folders.go
// setStatusPage (the description escaped: nothing shown is markup). See
// PaneStatusPage.xaml.

using Malachi.App.Resources;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Main;

/// <summary>A pane's status page (Adw.StatusPage).</summary>
public sealed partial class PaneStatusPage : UserControl
{
    /// <summary>An empty page.</summary>
    public PaneStatusPage()
    {
        InitializeComponent();
        Show("", "", "");
    }

    /// <summary>The sidebar's smaller page.</summary>
    public bool Compact { get; set; }

    /// <summary>The child under the texts (the list's Try Again), or null.</summary>
    public UIElement? Child
    {
        get => ChildHost.Content as UIElement;
        set => ChildHost.Content = value;
    }

    /// <summary>Fills the page: a GTK icon name ("" for none), the title and the description, plain text.</summary>
    public void Show(string icon, string title, string description)
    {
        Illustration.Glyph = Icons.Glyph(icon);
        Illustration.FontFamily = Icons.SymbolFont;
        Illustration.FontSize = Compact ? 48 : Icons.Status;
        Illustration.Margin = new Thickness(0, 0, 0, Compact ? 12 : 24);
        Illustration.Visibility = string.IsNullOrEmpty(icon) ? Visibility.Collapsed : Visibility.Visible;
        TitleText.Text = title ?? "";
        TitleText.FontSize = Compact ? 16 : 20;
        TitleText.Visibility = string.IsNullOrEmpty(title) ? Visibility.Collapsed : Visibility.Visible;
        DescriptionText.Text = description ?? "";
        DescriptionText.Visibility = string.IsNullOrEmpty(description) ? Visibility.Collapsed : Visibility.Visible;
        Column.Margin = Compact ? new Thickness(12, 24, 12, 24) : new Thickness(12, 36, 12, 36);
    }
}
