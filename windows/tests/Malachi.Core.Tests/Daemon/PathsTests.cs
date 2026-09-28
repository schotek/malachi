// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only cases of Paths beyond SupervisorTests.swift
// (pathsFollowTheDaemonsResolution): the home is USERPROFILE as Go's
// os.UserHomeDir reads it, api.SocketBase's Flatpak directory, empty
// variables, the data directory and MALACHI_DATA_DIR, the preferences' key
// and the names MALACHI_SETTINGS_KEY may give it (every other value falls
// back to the app's key), the key and log files, the directories made
// before the daemon starts; and the MCP
// bridge beside the app, the cases of ui/internal/mcpsetup/mcpsetup_test.go
// (TestLocate) that apply: the Windows app, as the macOS one, looks for the
// bridge beside itself only, never on PATH.

using System;
using System.Collections.Generic;
using System.IO;
using System.Threading.Tasks;
using Malachi.Core.Daemon;
using Malachi.Core.Tests.Platform;
using Xunit;

namespace Malachi.Core.Tests.Daemon;

public sealed class PathsTests
{
    private static readonly string Home = OperatingSystem.IsWindows() ? @"C:\Users\u" : "/home/u";

    private static Dictionary<string, string?> Env(params (string Name, string? Value)[] entries)
    {
        var env = new Dictionary<string, string?> { ["USERPROFILE"] = Home };
        foreach (var (name, value) in entries)
        {
            env[name] = value;
        }
        return env;
    }

    [Fact]
    public void TheHomeIsUserProfileNotHome()
    {
        var gitBash = OperatingSystem.IsWindows() ? "/c/Users/u" : "/elsewhere";
        var p = Paths.Resolve(Env(("HOME", gitBash)), null);
        Assert.Equal(Path.Combine(Home, ".cache", "malachi", "run", "rpc.sock"), p.Socket);
        Assert.False(p.SocketFromEnvironment);
    }

    [Fact]
    public void FlatpakKeepsTheSocketInTheApplicationsRuntimeDirectory()
    {
        var runtime = Path.Combine(Home, "run");
        var p = Paths.Resolve(Env(("XDG_RUNTIME_DIR", runtime), ("FLATPAK_ID", "io.github.schotek.Malachi")), null);
        Assert.Equal(Path.Combine(runtime, "app", "io.github.schotek.Malachi", "malachi", "rpc.sock"), p.Socket);
    }

    [Fact]
    public void EmptyVariablesAreUnset()
    {
        var p = Paths.Resolve(
            Env(("MALACHI_SOCKET", ""), ("XDG_RUNTIME_DIR", ""), ("XDG_CACHE_HOME", ""), ("MALACHI_DATA_DIR", ""), ("LOCALAPPDATA", Path.Combine(Home, "L"))),
            null);
        Assert.Equal(Path.Combine(Home, ".cache", "malachi", "run", "rpc.sock"), p.Socket);
        Assert.False(p.SocketFromEnvironment);
        Assert.Equal(Path.Combine(Home, "L", "Malachi Mail"), p.DataDir);
    }

    [Fact]
    public void MalachiSocketIsTakenAsItIs()
    {
        var socket = Path.Combine(Home, "x", "..", "d.sock");
        var p = Paths.Resolve(Env(("MALACHI_SOCKET", socket), ("XDG_RUNTIME_DIR", Path.Combine(Home, "run"))), null);
        Assert.Equal(socket, p.Socket);
        Assert.True(p.SocketFromEnvironment);
        Assert.Equal(socket + ".key", p.KeyFile);
    }

    [Fact]
    public void TheDataDirectoryIsLocalAppDataUnlessMalachiDataDirSaysOtherwise()
    {
        var local = Path.Combine(Home, "AppData", "Local");
        var p = Paths.Resolve(Env(("LOCALAPPDATA", local)), null);
        Assert.Equal(Path.Combine(local, "Malachi Mail"), p.DataDir);
        Assert.Equal(Path.Combine(local, "Malachi Mail", "config.toml"), p.Config);
        Assert.Equal(Path.Combine(local, "Malachi Mail", "store.db"), p.Store);
        Assert.Equal(Path.Combine(local, "Malachi Mail", "logs"), p.LogDir);
        Assert.Equal(Path.Combine(local, "Malachi Mail", "logs", "malachid.log"), p.DaemonLog);
        Assert.Equal(p.Socket + ".key", p.KeyFile);

        var elsewhere = Path.Combine(Home, "agent-data");
        var q = Paths.Resolve(Env(("LOCALAPPDATA", local), ("MALACHI_DATA_DIR", elsewhere)), null);
        Assert.Equal(elsewhere, q.DataDir);
        Assert.Equal(Path.Combine(elsewhere, "store.db"), q.Store);
    }

    [Fact]
    public void ThePreferencesAreTheAppsKeyWithoutMalachiSettingsKey()
    {
        Assert.Equal(@"Software\io.github.schotek.Malachi", Paths.DefaultSettingsKey);
        Assert.Equal("MALACHI_SETTINGS_KEY", Paths.SettingsKeyVariable);
        foreach (var p in new[] { Paths.Resolve(Env(), null), Paths.Resolve(Env(("MALACHI_SETTINGS_KEY", "")), null) })
        {
            Assert.Equal(Paths.DefaultSettingsKey, p.SettingsKey);
            Assert.Null(p.IgnoredSettingsKey);
        }
    }

    [Theory]
    [InlineData("io.github.schotek.Malachi")]
    [InlineData("io.github.schotek.Malachi.UiTests.0f8e2a6c4b1d4e7f9a3c5b2d1e0f6a7b")]
    [InlineData("io.github.schotek.Malachi.Tests")]
    [InlineData("io.github.schotek.Malachi.a")]
    [InlineData("io.github.schotek.Malachi.A-b_c.9")]
    [InlineData("io.github.schotek.Malachi..")]
    public void MalachiSettingsKeyNamesAKeyOfTheAppsFamily(string name)
    {
        var p = Paths.Resolve(Env(("MALACHI_SETTINGS_KEY", name)), null);
        Assert.Equal(@"Software\" + name, p.SettingsKey);
        Assert.Null(p.IgnoredSettingsKey);
    }

    [Theory]
    // The prefix without anything after its dot, or without the dot.
    [InlineData("io.github.schotek.Malachi.")]
    [InlineData("io.github.schotek.MalachiTests")]
    [InlineData("io.github.schotek.Malach")]
    // Re-cased: the registry would take it as the app's key, but it is a mistake.
    [InlineData("io.github.schotek.malachi")]
    [InlineData("IO.GITHUB.SCHOTEK.MALACHI.Tests")]
    // Another key, a subkey, a parent, a path.
    [InlineData("Microsoft")]
    [InlineData(@"Software\io.github.schotek.Malachi")]
    [InlineData(@"io.github.schotek.Malachi\Tests")]
    [InlineData(@"io.github.schotek.Malachi.Tests\..\..\Microsoft\Windows\CurrentVersion\Run")]
    [InlineData("io.github.schotek.Malachi.Tests/x")]
    [InlineData(@"..\Microsoft")]
    // Spaces, controls and letters beyond ASCII.
    [InlineData(" io.github.schotek.Malachi")]
    [InlineData("io.github.schotek.Malachi ")]
    [InlineData("io.github.schotek.Malachi.Tests x")]
    [InlineData("io.github.schotek.Malachi.Tests\t")]
    [InlineData("io.github.schotek.Malachi.Tests\n")]
    [InlineData("io.github.schotek.Malachi.Testš")]
    [InlineData("io.github.schotek.Malachi.Ｔests")]
    public void AnyOtherMalachiSettingsKeyIsIgnored(string name)
    {
        var p = Paths.Resolve(Env(("MALACHI_SETTINGS_KEY", name)), null);
        Assert.Equal(Paths.DefaultSettingsKey, p.SettingsKey);
        Assert.Equal(name, p.IgnoredSettingsKey);
    }

    [Fact]
    public async Task TheBridgeIsFoundBesideTheApp()
    {
        // beside the executable
        await using var f = new DaemonFixture();
        var bridge = f.Program("app", Paths.McpBridgeName);
        var app = Path.GetDirectoryName(bridge)!;
        Assert.Equal(bridge, Paths.Resolve(Env(), app).McpBridge);
        Assert.Null(Paths.Resolve(Env(), app).KeyringHelper);

        // not executable beside (where the system has execute bits)
        if (!OperatingSystem.IsWindows())
        {
            File.SetUnixFileMode(bridge, UnixFileMode.UserRead | UnixFileMode.UserWrite);
            Assert.Null(Paths.Resolve(Env(), app).McpBridge);
        }

        // missing: nothing beside the app, whatever is on PATH
        var onPath = f.Program("bin", Paths.McpBridgeName);
        Directory.CreateDirectory(Path.Combine(f.Path, "empty"));
        Assert.Null(Paths.Resolve(Env(("PATH", Path.GetDirectoryName(onPath))), Path.Combine(f.Path, "empty")).McpBridge);
    }

    [Fact]
    public async Task EnsureDirectoriesMakesTheDataAndRunDirectories()
    {
        await using var f = new DaemonFixture();
        var local = Path.Combine(f.Path, "Local");
        var profile = Path.Combine(f.Path, "profile");
        var p = Paths.Resolve(new Dictionary<string, string?> { ["USERPROFILE"] = profile, ["LOCALAPPDATA"] = local }, null);
        var directories = new FakePrivateDirectories();

        p.EnsureDirectories(directories);

        Assert.Equal([Path.Combine(local, "Malachi Mail")], directories.Created);
        Assert.True(Directory.Exists(p.LogDir));
        // The default socket directory is the app's: made private, whatever was there.
        Assert.Equal([Path.Combine(profile, ".cache", "malachi", "run")], directories.Ensured);

        // Again: an existing data directory is left as it is.
        var again = new FakePrivateDirectories();
        p.EnsureDirectories(again);
        Assert.Empty(again.Created);
        Assert.Equal([Path.Combine(profile, ".cache", "malachi", "run")], again.Ensured);
    }

    [Fact]
    public async Task ASocketDirectoryOfMalachiSocketIsOnlyCreated()
    {
        await using var f = new DaemonFixture();
        var missing = Path.Combine(f.Path, "sockets");
        var p = Paths.Resolve(Env(("MALACHI_SOCKET", Path.Combine(missing, "rpc.sock"))), null);
        var directories = new FakePrivateDirectories();
        p.EnsureSocketDirectory(directories);
        Assert.Equal([missing], directories.Created);
        Assert.Empty(directories.Ensured);

        // One that exists is the user's: never changed.
        var existing = new FakePrivateDirectories();
        p.EnsureSocketDirectory(existing);
        Assert.Empty(existing.Created);
        Assert.Empty(existing.Ensured);
    }

    [Fact]
    public async Task ADataDirectoryCreatedMeanwhileIsTaken()
    {
        await using var f = new DaemonFixture();
        var p = Paths.Resolve(Env(("MALACHI_DATA_DIR", Path.Combine(f.Path, "data")), ("MALACHI_SOCKET", f.Socket)), null);
        var directories = new FakePrivateDirectories { Collisions = 1 };
        p.EnsureDirectories(directories);
        Assert.True(Directory.Exists(p.DataDir));
        Assert.Empty(directories.Created);
    }
}
