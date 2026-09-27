// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The application object. Scaffold (phase B): it opens the main window; the
// lifecycle of docs/windows-port.md §10 (activation kinds, running in the
// background, the daemon) arrives in phase E1.

using Microsoft.UI.Xaml;

namespace Malachi.App;

/// <summary>The WinUI application.</summary>
public partial class App : Application
{
    private MainWindow? window;

    /// <summary>Loads App.xaml.</summary>
    public App()
    {
        InitializeComponent();
    }

    /// <inheritdoc />
    protected override void OnLaunched(LaunchActivatedEventArgs args)
    {
        window = new MainWindow();
        window.Activate();
    }
}
