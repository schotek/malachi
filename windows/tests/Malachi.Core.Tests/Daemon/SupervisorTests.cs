// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/SupervisorTests.swift. The #!/bin/sh
// and python3 stand-ins are Malachi.Core.TestDaemon (exit 3 after 200 ms,
// listen); FakeDaemon, which only has to listen here, is a bare listener.
// Windows differences, each in its test: the daemon is malachid.exe and the
// helper malachi-credentials.exe (Windows keeps no execute bits, so the
// "not executable" case runs where there are some), PATH is split on the
// system's separator, the socket limit is 107 bytes, the home is
// USERPROFILE, and the daemon's environment disables D-Bus.

using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Threading.Tasks;
using Malachi.Core.Daemon;
using Malachi.Core.TestDaemon;
using Xunit;

namespace Malachi.Core.Tests.Daemon;

public sealed class SupervisorTests
{
    private static Dictionary<string, string?> Env(params (string Name, string? Value)[] entries)
    {
        var env = new Dictionary<string, string?>();
        foreach (var (name, value) in entries)
        {
            env[name] = value;
        }
        return env;
    }

    [Fact]
    public void LocateHonoursTheEnvironment()
    {
        Assert.Null(DaemonSupervisor.Locate(null, Env(("MALACHI_DAEMON", "none"))));
        Assert.Null(DaemonSupervisor.Locate(null, Env(("MALACHI_DAEMON", ""))));
        Assert.Equal(@"C:\opt\x\malachid.exe", DaemonSupervisor.Locate(null, Env(("MALACHI_DAEMON", @"C:\opt\x\malachid.exe"))));
    }

    [Fact]
    public async Task LocateFindsTheDaemonBesideTheExecutableThenOnPath()
    {
        await using var f = new DaemonFixture();
        var daemon = f.Program("app", DaemonSupervisor.ExecutableName);
        var dir = Path.GetDirectoryName(daemon)!;
        var nonexistent = Path.Combine(f.Path, "nonexistent");
        Assert.Equal(daemon, DaemonSupervisor.Locate(dir, Env()));
        Assert.Equal(daemon, DaemonSupervisor.Locate(null, Env(("PATH", nonexistent + Path.PathSeparator + dir))));
        Assert.Throws<DaemonSupervisorException>(() => DaemonSupervisor.Locate(null, Env(("PATH", nonexistent))));
        // A directory named malachid.exe does not count.
        var trap = Path.Combine(f.Path, "trap");
        Directory.CreateDirectory(Path.Combine(trap, DaemonSupervisor.ExecutableName));
        var e = Assert.Throws<DaemonSupervisorException>(() => DaemonSupervisor.Locate(trap, Env(("PATH", ""))));
        Assert.Equal(DaemonSupervisorFailure.NoDaemon, e.Failure);
    }

    [Fact]
    public async Task AdoptsARunningDaemonWithoutSpawning()
    {
        await using var f = new DaemonFixture();
        f.Listen(f.Socket);
        using var sup = new DaemonSupervisor(null, f.Socket);
        await sup.EnsureAsync(TestContext.Current.CancellationToken);
        Assert.Equal(0, sup.Spawns);
        await sup.StopAsync(); // nothing of ours to stop; must not touch the listener
        Assert.True(DaemonFixture.Answers(f.Socket));
    }

    [Fact]
    public async Task NoDaemonAndNothingListening()
    {
        await using var f = new DaemonFixture();
        using var sup = new DaemonSupervisor(null, f.Socket);
        var e = await Assert.ThrowsAsync<DaemonSupervisorException>(() => sup.EnsureAsync(TestContext.Current.CancellationToken));
        Assert.Equal(DaemonSupervisorFailure.NoDaemon, e.Failure);
    }

    [Fact]
    public async Task EarlyExitIsReportedAndBackedOff()
    {
        await using var f = new DaemonFixture();
        var sup = f.Supervisor(TestDaemonSettings.Exit, extra: [(TestDaemonSettings.DelayEnv, "200"), (TestDaemonSettings.ExitCodeEnv, "3")]);
        var cancellationToken = TestContext.Current.CancellationToken;

        var first = await Assert.ThrowsAsync<DaemonSupervisorException>(() => sup.EnsureAsync(cancellationToken));
        Assert.Equal(DaemonSupervisorFailure.ExitedEarly, first.Failure);
        Assert.Equal("malachid exited with status 3 before opening its socket", first.Message);
        Assert.Equal(1, sup.Spawns);
        // One exit is retried at once; the second consecutive exit backs off.
        await Assert.ThrowsAsync<DaemonSupervisorException>(() => sup.EnsureAsync(cancellationToken));
        Assert.Equal(2, sup.Spawns);
        var third = await Assert.ThrowsAsync<DaemonSupervisorException>(() => sup.EnsureAsync(cancellationToken));
        Assert.Equal(DaemonSupervisorFailure.Backoff, third.Failure);
        Assert.Equal(2, third.Failures);
        Assert.Equal(2, sup.Spawns);
    }

    [Fact]
    public async Task SpawnsPollsAndStops()
    {
        // The stand-in daemon binds the socket it is given as --socket,
        // accepts (and drops) every connection like malachid would, and
        // exits on a stop request.
        await using var f = new DaemonFixture();
        var sup = f.Supervisor(TestDaemonSettings.Listen);
        await sup.EnsureAsync(TestContext.Current.CancellationToken);
        Assert.Equal(1, sup.Spawns);
        Assert.True(DaemonFixture.Answers(f.Socket));
        await sup.EnsureAsync(TestContext.Current.CancellationToken); // already up: no second spawn
        Assert.Equal(1, sup.Spawns);
        var clock = Stopwatch.StartNew();
        await sup.StopAsync();
        Assert.True(clock.Elapsed < TimeSpan.FromSeconds(5), "the stop request must end the stand-in promptly");
        Assert.False(DaemonFixture.Answers(f.Socket));
    }

    [Fact]
    public void SocketPathLengthIsChecked()
    {
        var root = OperatingSystem.IsWindows() ? @"C:\t\" : "/tmp/";
        var longPath = root + new string('x', 110);
        var e = Assert.Throws<SocketPathTooLongException>(() => Paths.CheckSocket(longPath));
        Assert.Contains("MALACHI_SOCKET", e.Message, StringComparison.Ordinal);
        Assert.False(DaemonFixture.Answers(longPath));
        Paths.CheckSocket(root + "short.sock");
    }

    [Fact]
    public void PathsFollowTheDaemonsResolution()
    {
        var home = OperatingSystem.IsWindows() ? @"C:\Users\u" : "/home/u";
        var local = Path.Combine(home, "AppData", "Local");
        var env = Env(("USERPROFILE", home), ("LOCALAPPDATA", local));
        Assert.Equal(Path.Combine(home, ".cache", "malachi", "run", "rpc.sock"), Paths.Resolve(env, null).Socket);
        var cache = Path.Combine(home, "c");
        Assert.Equal(
            Path.Combine(cache, "malachi", "run", "rpc.sock"),
            Paths.Resolve(Env(("USERPROFILE", home), ("XDG_CACHE_HOME", cache)), null).Socket);
        var runtime = Path.Combine(home, "run", "user", "1");
        Assert.Equal(
            Path.Combine(runtime, "malachi", "rpc.sock"),
            Paths.Resolve(Env(("USERPROFILE", home), ("XDG_RUNTIME_DIR", runtime)), null).Socket);
        Assert.Equal("s.sock", Paths.Resolve(Env(("USERPROFILE", home), ("MALACHI_SOCKET", "s.sock")), null).Socket);
        var p = Paths.Resolve(env, null);
        Assert.Equal("Malachi Mail", Path.GetFileName(p.DataDir));
        Assert.EndsWith(Path.Combine("Malachi Mail", "config.toml"), p.Config, StringComparison.Ordinal);
        Assert.EndsWith(Path.Combine("Malachi Mail", "store.db"), p.Store, StringComparison.Ordinal);
        Assert.Null(p.McpBridge);
        Assert.Null(p.KeyringHelper);
    }

    [Fact]
    public async Task PathsFindTheKeyringHelperBesideTheExecutable()
    {
        await using var f = new DaemonFixture();
        var helper = f.Program("app", Paths.KeyringHelperName);
        var dir = Path.GetDirectoryName(helper)!;
        var env = Env(("USERPROFILE", f.Path));
        Assert.Equal(helper, Paths.Resolve(env, dir).KeyringHelper);

        if (!OperatingSystem.IsWindows())
        {
            // Not executable: not a helper. (Windows keeps no execute bits.)
            File.SetUnixFileMode(helper, UnixFileMode.UserRead | UnixFileMode.UserWrite);
            Assert.Null(Paths.Resolve(env, dir).KeyringHelper);
        }

        // A directory of that name does not count either.
        var trap = Path.Combine(f.Path, "trap");
        Directory.CreateDirectory(Path.Combine(trap, Paths.KeyringHelperName));
        Assert.Null(Paths.Resolve(env, trap).KeyringHelper);
    }

    [Fact]
    public async Task EnvironmentSelectsTheBundledHelper()
    {
        await using var f = new DaemonFixture();
        var helper = f.Program("app", Paths.KeyringHelperName);
        var daemon = Path.Combine(Path.GetDirectoryName(helper)!, DaemonSupervisor.ExecutableName);
        var home = OperatingSystem.IsWindows() ? @"C:\Users\u" : "/home/u";
        var systemPath = OperatingSystem.IsWindows() ? @"C:\Windows\system32" : "/usr/bin";
        var @base = Env(("USERPROFILE", home), ("PATH", systemPath));

        var withHelper = f.Launch(daemon, helper);
        var env = DaemonSupervisor.Environment(@base, withHelper);
        Assert.Equal("helper", env["MALACHI_KEYRING"]);
        Assert.Equal(helper, env["MALACHI_KEYRING_HELPER"]);
        Assert.True(env["USERPROFILE"] == home && env["PATH"] == systemPath, "the app's environment is kept");
        Assert.Equal("disabled:", env["DBUS_SESSION_BUS_ADDRESS"]);

        var without = f.Launch(daemon);
        env = DaemonSupervisor.Environment(@base, without);
        Assert.Equal("none", env["MALACHI_KEYRING"]);
        Assert.False(env.ContainsKey("MALACHI_KEYRING_HELPER"));

        // A helper that is not there (a broken app folder) falls back to
        // none rather than a daemon that refuses to start.
        var gone = f.Launch(daemon, Path.Combine(Path.GetDirectoryName(helper)!, "missing"));
        env = DaemonSupervisor.Environment(@base, gone);
        Assert.Equal("none", env["MALACHI_KEYRING"]);
        Assert.False(env.ContainsKey("MALACHI_KEYRING_HELPER"));

        // A preset MALACHI_KEYRING wins, helper or not.
        foreach (var preset in new[] { "none", "secretservice", "helper" })
        {
            var presetBase = new Dictionary<string, string?>(@base) { ["MALACHI_KEYRING"] = preset };
            env = DaemonSupervisor.Environment(presetBase, withHelper);
            Assert.Equal(preset, env["MALACHI_KEYRING"]);
            Assert.False(env.ContainsKey("MALACHI_KEYRING_HELPER"));
        }

        // So does a preset D-Bus address.
        var dbus = new Dictionary<string, string?>(@base) { ["DBUS_SESSION_BUS_ADDRESS"] = "tcp:host=localhost,port=1" };
        Assert.Equal("tcp:host=localhost,port=1", DaemonSupervisor.Environment(dbus, withHelper)["DBUS_SESSION_BUS_ADDRESS"]);
    }

    /// <summary>
    /// The counterpart of Swift's environmentSetsStorageDefaults, decided the
    /// other way: the macOS app gives its daemon
    /// MALACHI_DEFAULT_COMPRESS_STORE=1 and
    /// MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS=30, the Windows app sets
    /// neither, as the GTK UI, so the daemon's own defaults apply (off and
    /// 0; docs/windows-port.md §5). Values already in the environment go
    /// through untouched, whatever the keyring.
    /// </summary>
    [Fact]
    public async Task EnvironmentLeavesTheStorageDefaultsToTheDaemon()
    {
        await using var f = new DaemonFixture();
        var helper = f.Program("app", Paths.KeyringHelperName);
        var daemon = Path.Combine(Path.GetDirectoryName(helper)!, DaemonSupervisor.ExecutableName);
        var @base = Env(("USERPROFILE", OperatingSystem.IsWindows() ? @"C:\Users\u" : "/home/u"));

        foreach (var launch in new[] { f.Launch(daemon, helper), f.Launch(daemon) })
        {
            var env = DaemonSupervisor.Environment(@base, launch);
            Assert.False(env.ContainsKey("MALACHI_DEFAULT_COMPRESS_STORE"));
            Assert.False(env.ContainsKey("MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS"));
        }

        var preset = new Dictionary<string, string?>(@base)
        {
            ["MALACHI_DEFAULT_COMPRESS_STORE"] = "1",
            ["MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS"] = "-1",
            ["MALACHI_KEYRING"] = "none",
        };
        var kept = DaemonSupervisor.Environment(preset, f.Launch(daemon, helper));
        Assert.Equal("1", kept["MALACHI_DEFAULT_COMPRESS_STORE"]);
        Assert.Equal("-1", kept["MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS"]);
        preset.Remove("MALACHI_KEYRING");
        preset["MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS"] = "";
        var withEmpty = DaemonSupervisor.Environment(preset, f.Launch(daemon, helper));
        Assert.Equal("helper", withEmpty["MALACHI_KEYRING"]);
        Assert.Equal("1", withEmpty["MALACHI_DEFAULT_COMPRESS_STORE"]);
        // An empty one stays empty: the daemon reads it as no default.
        Assert.Equal("", withEmpty["MALACHI_DEFAULT_ATTACHMENT_OFFLINE_DAYS"]);
    }
}
