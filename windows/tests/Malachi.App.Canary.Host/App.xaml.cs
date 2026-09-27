// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The canary host's application: reads the configuration named on the
// command line, plays it (CanaryRunner), writes the results beside it and
// exits. A watchdog ends a run that hangs, so a test never waits for ever:
// exit code 0 when the run completed, 1 when it failed, 2 without a
// configuration, 3 when the watchdog fired.

using System;
using System.IO;
using System.Text.Json;
using System.Threading;
using Microsoft.UI.Xaml;

namespace Malachi.App.Canary.Host;

/// <summary>The canary host application.</summary>
public partial class App : Application
{
    private static readonly TimeSpan Watchdog = TimeSpan.FromSeconds(100);

    /// <summary>Loads App.xaml.</summary>
    public App()
    {
        InitializeComponent();
    }

    /// <inheritdoc/>
    protected override async void OnLaunched(LaunchActivatedEventArgs args)
    {
        var arguments = Environment.GetCommandLineArgs();
        if (arguments.Length < 2 || !File.Exists(arguments[1]))
        {
            Environment.Exit(2);
            return;
        }
        var config = JsonSerializer.Deserialize(File.ReadAllText(arguments[1]), CanaryJson.Default.HostConfig)!;
        using var watchdog = new Timer(_ => Environment.Exit(3), null, Watchdog, Timeout.InfiniteTimeSpan);
        var results = await new CanaryRunner(config).RunAsync();
        File.WriteAllText(config.Results, JsonSerializer.Serialize(results, CanaryJson.Default.HostResults));
        Environment.Exit(results.Completed ? 0 : 1);
    }
}
