// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The main window. Scaffold (phase B): the title bar only. GTK:
// ui/data/ui/window.blp (title "Malachi Mail", not translated).

using Malachi.Core;
using Microsoft.UI.Xaml;

namespace Malachi.App;

/// <summary>The main window of the application.</summary>
public sealed partial class MainWindow : Window
{
    /// <summary>Builds the window with its title bar.</summary>
    public MainWindow()
    {
        InitializeComponent();
        // The caption the taskbar and Alt+Tab show, and the one drawn in the
        // title bar that the content extends into.
        Title = AppIdentity.DisplayName;
        AppTitleBar.Title = AppIdentity.DisplayName;
        ExtendsContentIntoTitleBar = true;
        SetTitleBar(AppTitleBar);
    }
}
