// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The canary host's application: reads the configuration named on the
// command line, plays it (CanaryRunner), writes the results beside it and
// exits. A watchdog, started first, ends a run that hangs, so a test never
// waits for ever: exit code 0 when the run completed, 1 when it failed, 2
// without a readable configuration, 3 when the watchdog fired. What the run
// recorded is also appended, as it happens, to <results>.progress.

using System;
using System.IO;
using System.Text.Json;
using System.Threading;
using Microsoft.UI.Xaml;

namespace Malachi.App.Canary.Host;

/// <summary>The canary host application.</summary>
public partial class App : Application
{
    // The longest run, the recovery run, took up to 75 s on a busy machine,
    // and the browser may take CanaryRunner.BrowserExitTimeout (120 s) to
    // end after it.
    private static readonly TimeSpan Watchdog = TimeSpan.FromSeconds(210);

    // Lives as long as the process, whatever happens to OnLaunched.
    private static Timer? watchdog;

    /// <summary>Loads App.xaml.</summary>
    public App()
    {
        InitializeComponent();
    }

    /// <inheritdoc/>
    protected override async void OnLaunched(LaunchActivatedEventArgs args)
    {
        watchdog = new Timer(_ => Environment.Exit(3), null, Watchdog, Timeout.InfiniteTimeSpan);
        var arguments = Environment.GetCommandLineArgs();
        HostConfig? config = null;
        try
        {
            if (arguments.Length >= 2 && File.Exists(arguments[1]))
            {
                config = JsonSerializer.Deserialize(File.ReadAllText(arguments[1]), CanaryJson.Default.HostConfig);
            }
        }
        catch (JsonException)
        {
            config = null;
        }
        if (config is null)
        {
            Environment.Exit(2);
            return;
        }
        try
        {
            var results = await new CanaryRunner(config).RunAsync();
            File.WriteAllText(config.Results, JsonSerializer.Serialize(results, CanaryJson.Default.HostResults));
            Environment.Exit(results.Completed ? 0 : 1);
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            // The runner records its own failures; this is one of the host
            // itself (the results could not be written), for the progress
            // file beside them.
            File.AppendAllText(config.Results + ".progress", "host failed: " + e + "\n");
            Environment.Exit(1);
        }
    }
}
