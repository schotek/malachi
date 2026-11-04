// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the Toolbar group of Preferences → General of the GTK port
// (docs/plans/toolbar-customization.md T13; ui/internal/toolbar text.go
// GroupTitle, DisplayModeTitle, DisplayModes, DisplayModeLabel,
// CustomizeTitle). The display mode is toolbar-display-mode, one for every
// bar (O4), bound two-way like the other choices; Customize Toolbar…
// opens the editor's dialog over this window, on the Mail toolbar (a right
// click on a bar of the main window opens it on that bar, T15). macOS has
// no such group: its toolbars are customized with AppKit's palette.

using Malachi.App.Toolbars;
using Malachi.Core.Settings;
using Malachi.Core.Toolbars;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Preferences;

public sealed partial class GeneralPage
{
    /// <summary>The Preferences window the page is in (the editor's dialog goes over it).</summary>
    internal Window? HostWindow { get; set; }

    private void InitializeToolbarGroup(SettingBindings bindings)
    {
        var s = state.Settings;
        ToolbarHeading.Text = Toolbar.GroupTitle();
        ToolbarDisplayRow.Header = Toolbar.DisplayModeTitle();
        AutomationProperties.SetName(ToolbarDisplayBox, Toolbar.DisplayModeTitle());
        foreach (var m in Toolbar.DisplayModes)
        {
            ToolbarDisplayBox.Items.Add(new ComboBoxItem { Content = Toolbar.DisplayModeLabel(m) });
        }
        bindings.Choice(ToolbarDisplayBox, SettingsKey.ToolbarDisplayMode, Toolbar.DisplayModes, () => s.ToolbarDisplayMode, v => s.ToolbarDisplayMode = v);
        ToolbarCustomizeRow.Header = Toolbar.DialogTitle();
        ToolbarCustomizeButton.Content = Toolbar.CustomizeTitle();
    }

    private void OnCustomizeToolbarClick(object sender, RoutedEventArgs e)
    {
        if (HostWindow is { } window)
        {
            ToolbarEditorView.Show(state, window, Toolbar.Bar.Mail);
        }
    }
}
