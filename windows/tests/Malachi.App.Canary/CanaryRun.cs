// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// One run of the canary host: its listeners, its configuration, the process,
// and what it left behind (results, NetLog).

using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Text.Json;
using System.Threading.Tasks;
using Malachi.App.Canary.Host;

namespace Malachi.App.Canary;

/// <summary>A canary host run and its evidence.</summary>
internal sealed class CanaryRun : IDisposable
{
    // Beyond the host's own watchdog (App.Watchdog, 150 s).
    private static readonly TimeSpan Timeout = TimeSpan.FromSeconds(160);

    private readonly Dictionary<string, CanaryListener> canaries = new(StringComparer.Ordinal);

    /// <summary>A run named <paramref name="name"/> in <paramref name="directory"/>, with a listener per vector.</summary>
    public CanaryRun(string name, string directory, IEnumerable<string> vectors)
    {
        Name = name;
        Directory = directory;
        System.IO.Directory.CreateDirectory(directory);
        foreach (var vector in vectors)
        {
            canaries[vector] = new CanaryListener(vector);
        }
    }

    /// <summary>protected, control or recovery.</summary>
    public string Name { get; }

    /// <summary>The run's own directory (user data, NetLog, results, downloads).</summary>
    public string Directory { get; }

    /// <summary>The listeners by vector.</summary>
    public IReadOnlyDictionary<string, CanaryListener> Canaries => canaries;

    /// <summary>The host's exit code, once it ran.</summary>
    public int ExitCode { get; private set; }

    /// <summary>What the host recorded; null when it wrote nothing.</summary>
    public HostResults? Results { get; private set; }

    /// <summary>The NetLog; null when there is none.</summary>
    public NetLog? NetLog { get; private set; }

    /// <summary>Why the NetLog could not be read (a runtime without an event the reader relies on); null otherwise.</summary>
    public string? NetLogError { get; private set; }

    /// <summary>The listener of <paramref name="vector"/>.</summary>
    public CanaryListener Canary(string vector) => canaries[vector];

    /// <summary>The configuration of a run with these steps.</summary>
    public HostConfig Config(string mode, IReadOnlyList<HostStep> steps) => new()
    {
        Mode = mode,
        UserDataFolder = Path.Combine(Directory, "udf"),
        NetLog = Path.Combine(Directory, "netlog.json"),
        Results = Path.Combine(Directory, "results.json"),
        Downloads = Path.Combine(Directory, "downloads"),
        Steps = steps,
    };

    /// <summary>Runs <paramref name="host"/> over <paramref name="config"/> and reads what it left.</summary>
    public async Task RunAsync(string host, HostConfig config)
    {
        var configPath = Path.Combine(Directory, "config.json");
        await File.WriteAllTextAsync(configPath, JsonSerializer.Serialize(config, CanaryJson.Default.HostConfig));
        using var process = Process.Start(new ProcessStartInfo(host, "\"" + configPath + "\"") { UseShellExecute = false })
            ?? throw new InvalidOperationException("the canary host did not start");
        var exited = process.WaitForExitAsync();
        if (await Task.WhenAny(exited, Task.Delay(Timeout)) != exited)
        {
            process.Kill(entireProcessTree: true);
            await process.WaitForExitAsync();
            ExitCode = -1;
        }
        else
        {
            ExitCode = process.ExitCode;
        }
        if (File.Exists(config.Results))
        {
            Results = JsonSerializer.Deserialize(await File.ReadAllTextAsync(config.Results), CanaryJson.Default.HostResults);
        }
        if (File.Exists(config.NetLog))
        {
            try
            {
                NetLog = NetLog.Read(config.NetLog);
            }
            catch (Exception e) when (e is InvalidDataException or JsonException or IOException)
            {
                // The tests that need the log say why there is none. A
                // browser still ending after a host killed at the timeout
                // holds the file (IOException); the fixture must not fail
                // every test for that, the run's own tests say what went wrong.
                NetLogError = e.Message;
            }
        }
    }

    /// <summary>The vectors whose listener was reached, with what reached it.</summary>
    public IReadOnlyList<string> Reached() =>
        [.. canaries.Values.Where(c => c.Hits.Count > 0).Select(c => c.Vector + ": " + string.Join(" | ", c.Hits))];

    public void Dispose()
    {
        foreach (var canary in canaries.Values)
        {
            canary.Dispose();
        }
    }
}
