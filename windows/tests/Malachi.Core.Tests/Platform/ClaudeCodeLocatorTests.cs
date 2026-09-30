// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of ClaudeCodeLocatorTests in macos/Tests/MalachiCoreTests/
// ClaudeCodeProcessTests.swift and of ui/internal/assistantpanel/
// locator_test.go (with AutomaticPath, which Swift does not have), against
// Malachi.FakeClaude where claude runs; a candidate that is only looked at
// is an empty file. Only paths inside a test's directory count, so a Claude
// Code installed on this computer never answers.
//
// Windows differences, as the locator's: only a file named *.exe is a
// claude (a file without it stands for Swift's file without an execute
// bit), the native install is %USERPROFILE%\.local\bin\claude.exe, the
// setting's path is taken when it is drive-absolute and replaces a
// candidate of any spelling. Swift's nvm link is a link in .local\bin to a
// claude.exe elsewhere; creating links needs Developer Mode or an
// administrator, so that test is skipped without them. The stand-in records
// its --version and auth runs (FakeClaudeScript.Calls), as Swift's script
// appends to "calls"; its auth state is a file the test rewrites. Go's
// TestLocatorFailedRunsAreUnknown (auth status without JSON) is a case of
// AnAnswerWithoutLoggedInIsUnknown, with the other answers that decode to
// no boolean. Added: the .exe rule, the setting's spelling, the private
// directory of the runs, and a PATH spelt Path.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Threading.Tasks;
using Malachi.Core.Platform;
using Malachi.Core.Settings;
using Malachi.Core.Tests.Assistants;
using Malachi.Core.Tests.Controllers;
using Malachi.FakeClaude;
using Xunit;

namespace Malachi.Core.Tests.Platform;

[Collection(typeof(McpRegistrationProcesses))]
public sealed class ClaudeCodeLocatorTests
{
    private static readonly byte[] Nothing = [];

    private static void RequireWindows() => Assert.SkipUnless(OperatingSystem.IsWindows(), "Windows paths and the stand-in claude.exe");

    private static SettingsStore Settings() => new(new InMemorySettingsBackend(), null);

    // Only paths inside dir count; a run may take long on a busy machine
    // (a .NET stand-in starting), which no test times.
    private static ClaudeCodeLocator Locator(
        string dir, SettingsStore settings, IReadOnlyDictionary<string, string>? env = null, IPrivateDirectoryFactory? directories = null, string? work = null,
        TimeSpan? signInTimeout = null)
    {
        var prefix = dir + @"\";
        return new ClaudeCodeLocator(
            settings,
            env ?? CannedStreamJson.Environment(("USERPROFILE", dir), ("PATH", "")),
            timeout: TimeSpan.FromSeconds(60),
            directory: work ?? dir,
            directories: directories,
            usable: p => p.StartsWith(prefix, StringComparison.OrdinalIgnoreCase) && ClaudeCodeLocator.IsExecutableFile(p),
            signInTimeout: signInTimeout ?? TimeSpan.FromSeconds(60));
    }

    private static string Touch(string path)
    {
        Directory.CreateDirectory(Path.GetDirectoryName(path)!);
        File.WriteAllBytes(path, Nothing);
        return path;
    }

    [Fact]
    public void TheSettingComesFirst()
    {
        RequireWindows();
        using var dir = new TemporaryDirectory();
        var home = Path.Combine(dir.Path, "home");
        var native = Touch(Path.Combine(home, ".local", "bin", "claude.exe"));
        var chosen = Touch(Path.Combine(dir.Path, "my-claude.exe"));
        var settings = Settings();
        var l = Locator(dir.Path, settings, CannedStreamJson.Environment(("USERPROFILE", home), ("PATH", "")));
        Assert.Equal(native, l.Locate());
        settings.AssistantClaudePath = chosen;
        Assert.Equal(chosen, l.Locate());
        Assert.Equal(chosen, l.Candidates()[0]);
        Assert.Equal(native, l.AutomaticPath());
        // A chosen path that is not a claude Windows runs falls back.
        var plain = Touch(Path.Combine(dir.Path, "not-executable"));
        foreach (var p in new[] { plain, dir.Path, @"relative\claude.exe" })
        {
            settings.AssistantClaudePath = p;
            Assert.Equal(native, l.Locate());
        }
    }

    [Fact]
    public void LinksCountByTheirTargetAndKeepTheirPath()
    {
        RequireWindows();
        using var dir = new TemporaryDirectory();
        var home = Path.Combine(dir.Path, "home");
        var target = Touch(Path.Combine(dir.Path, "lib", "claude.exe"));
        var link = Path.Combine(home, ".local", "bin", "claude.exe");
        Directory.CreateDirectory(Path.GetDirectoryName(link)!);
        try
        {
            File.CreateSymbolicLink(link, target);
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
            Assert.Skip("creating a symbolic link needs Developer Mode or an administrator here");
        }
        var l = Locator(dir.Path, Settings(), CannedStreamJson.Environment(("USERPROFILE", home), ("PATH", "")));
        Assert.Equal(link, l.Locate());
        // A dangling link is nothing.
        File.Delete(target);
        Assert.Null(l.Locate());
    }

    [Fact]
    public async Task NothingFound()
    {
        RequireWindows();
        using var dir = new TemporaryDirectory();
        var l = Locator(dir.Path, Settings());
        Assert.Null(l.Locate());
        Assert.Null(l.AutomaticPath());
        Assert.Null(await l.VersionAsync());
        Assert.Null(await l.SignedInAsync());
    }

    /// <summary>--version and auth status --json run once each and are kept until Refresh.</summary>
    [Fact]
    public async Task VersionAndSignInAreAskedOnce()
    {
        RequireWindows();
        using var dir = new TemporaryDirectory();
        var state = Path.Combine(dir.Path, "auth.json");
        const string SignedIn = """{"loggedIn": true, "authMethod": "claude.ai", "email": "me@example.invalid"}""";
        File.WriteAllText(state, SignedIn + "\n");
        var claude = new FakeClaudeScript(
            [],
            version: [FakeClaudeStep.Lines(FakeClaudeScript.DefaultVersion, "more"), FakeClaudeStep.Exit(0)],
            auth: [FakeClaudeStep.PrintFile(state), FakeClaudeStep.Exit(0)]).CreateIn(dir.Path);
        var settings = Settings();
        settings.AssistantClaudePath = claude;
        var directories = new FakePrivateDirectories();
        var work = Path.Combine(dir.Path, "work");
        var l = Locator(dir.Path, settings, directories: directories, work: work);
        Assert.Equal("2.1.178 (Claude Code)", await l.VersionAsync());
        Assert.Equal("2.1.178 (Claude Code)", await l.VersionAsync());
        Assert.True(await l.SignedInAsync());
        Assert.True(await l.SignedInAsync());
        Assert.Equal(["--version", "auth status --json"], FakeClaudeScript.Calls(dir.Path));
        File.WriteAllText(state, SignedIn.Replace("true", "false", StringComparison.Ordinal) + "\n");
        Assert.True(await l.SignedInAsync()); // kept
        l.Refresh();
        Assert.False(await l.SignedInAsync());
        Assert.Equal(3, FakeClaudeScript.Calls(dir.Path).Count);
        // The runs happened in the private directory, made on demand.
        Assert.True(Directory.Exists(work));
        Assert.Equal([work, work, work], directories.Ensured);
    }

    [Fact]
    public async Task FailedRunsAreUnknown()
    {
        RequireWindows();
        using var dir = new TemporaryDirectory();
        var claude = new FakeClaudeScript(
            [],
            version: [FakeClaudeStep.Stderr("broken\n"), FakeClaudeStep.Exit(1)],
            auth: [FakeClaudeStep.Stdout("Not logged in\n"), FakeClaudeStep.Exit(1)]).CreateIn(dir.Path);
        var settings = Settings();
        settings.AssistantClaudePath = claude;
        var l = Locator(dir.Path, settings);
        Assert.Null(await l.VersionAsync());
        Assert.Null(await l.SignedInAsync());

        // Signed out may exit non-zero: the JSON counts.
        var outDir = Path.Combine(dir.Path, "out");
        Directory.CreateDirectory(outDir);
        settings.AssistantClaudePath = new FakeClaudeScript(
            [],
            auth: [FakeClaudeStep.Stdout("{\"loggedIn\": false}\n"), FakeClaudeStep.Exit(1)]).CreateIn(outDir);
        Assert.False(await l.SignedInAsync());
    }

    /// <summary>
    /// locator_test.go TestLocatorSignIn. Claude Code's own sign-in: claude
    /// auth login in the private directory, its end reported once, and the
    /// kept sign-in state asked afresh after it.
    /// </summary>
    [Fact]
    public async Task SignIn()
    {
        RequireWindows();
        using var dir = new TemporaryDirectory();
        var state = Path.Combine(dir.Path, "logged-in");
        File.WriteAllText(state, "{\"loggedIn\": false}\n");
        var settings = Settings();
        settings.AssistantClaudePath = new FakeClaudeScript([], auth: [FakeClaudeStep.PrintFile(state)]).CreateIn(dir.Path);
        var work = Path.Combine(dir.Path, "work");
        var l = Locator(dir.Path, settings, work: work);
        var changes = 0;
        void Count(object? sender, EventArgs e) => changes++;
        l.SigningInChanged += Count;
        Assert.False(await l.SignedInAsync());

        FakeClaudeScript.SetLogin(
            dir.Path,
            FakeClaudeStep.Stdout("Opening browser to sign in…\nIf the browser didn't open, visit: https://claude.example/authorize?state=secret\n"),
            FakeClaudeStep.WriteFile(state, "{\"loggedIn\": true}\n"));
        var run = l.SignInAsync();
        Assert.True(l.SigningIn);
        Assert.Equal(1, changes);
        Assert.Equal(new ClaudeCodeSignIn.Done(), await run);
        Assert.False(l.SigningIn);
        Assert.Equal(2, changes);
        // No Refresh by the caller: the answer kept from before is gone.
        Assert.True(await l.SignedInAsync());
        Assert.Equal(1, FakeClaudeScript.Logins(dir.Path));
        Assert.Equal(work, FakeClaudeScript.LoginCwd(dir.Path));

        // A bad end: stderr's first line, else the status.
        FakeClaudeScript.SetLogin(dir.Path, FakeClaudeStep.Stderr("Login failed: no\nmore\n"), FakeClaudeStep.Exit(3));
        Assert.Equal(new ClaudeCodeSignIn.Failed("Login failed: no"), await l.SignInAsync());
        FakeClaudeScript.SetLogin(dir.Path, FakeClaudeStep.Exit(4));
        Assert.Equal(new ClaudeCodeSignIn.Failed("claude exited with status 4"), await l.SignInAsync());

        // Cancelled; then one that another takes the place of.
        FakeClaudeScript.SetLogin(dir.Path, FakeClaudeStep.Hang());
        run = l.SignInAsync();
        l.CancelSignIn(run);
        l.CancelSignIn(run);
        Assert.False(l.SigningIn);
        Assert.Equal(new ClaudeCodeSignIn.Cancelled(), await run);
        var stale = l.SignInAsync();
        FakeClaudeScript.SetLogin(dir.Path);
        var next = l.SignInAsync();
        Assert.Equal(new ClaudeCodeSignIn.Cancelled(), await stale);
        Assert.Equal(new ClaudeCodeSignIn.Done(), await next);
        // The cancel of a sign-in that is over ends no other.
        FakeClaudeScript.SetLogin(dir.Path, FakeClaudeStep.Hang());
        run = l.SignInAsync();
        l.CancelSignIn(stale);
        Assert.True(l.SigningIn);
        l.CancelSignIn();
        Assert.Equal(new ClaudeCodeSignIn.Cancelled(), await run);

        // Out of time.
        var brief = Locator(dir.Path, settings, work: work, signInTimeout: TimeSpan.FromMilliseconds(500));
        Assert.Equal(new ClaudeCodeSignIn.TimedOut(), await brief.SignInAsync());
        Assert.False(brief.SigningIn);

        // A removed handler hears nothing more; no Claude Code at all.
        l.SigningInChanged -= Count;
        var before = changes;
        File.Delete(settings.AssistantClaudePath);
        Assert.Equal(new ClaudeCodeSignIn.NotFound(), await l.SignInAsync());
        Assert.False(l.SigningIn);
        Assert.Equal(before, changes);
    }

    /// <summary>locator_test.go TestLocatorFailedRunsAreUnknown: auth status that prints no JSON, or no boolean, is not known.</summary>
    [Theory]
    [InlineData("not json\n")]
    [InlineData("{\"loggedIn\": \"yes\"}\n")]
    [InlineData("{\"loggedIn\": null}\n")]
    [InlineData("[true]\n")]
    [InlineData("{\"loggedIn\": true} trailing\n")]
    [InlineData("")]
    public async Task AnAnswerWithoutLoggedInIsUnknown(string output)
    {
        RequireWindows();
        using var dir = new TemporaryDirectory();
        var settings = Settings();
        settings.AssistantClaudePath = new FakeClaudeScript([], auth: [FakeClaudeStep.Stdout(output), FakeClaudeStep.Exit(0)]).CreateIn(dir.Path);
        Assert.Null(await Locator(dir.Path, settings).SignedInAsync());
    }

    /// <summary>Windows: only claude.exe runs (npm's claude.cmd would go through cmd.exe), and a directory of that name is none.</summary>
    [Theory]
    [InlineData("claude.exe", true)]
    [InlineData("claude.EXE", true)]
    [InlineData("claude.cmd", false)]
    [InlineData("claude", false)]
    [InlineData("claude.exe.txt", false)]
    public void OnlyAnExeFileIsAClaude(string name, bool want)
    {
        using var dir = new TemporaryDirectory();
        Assert.Equal(want, ClaudeCodeLocator.IsExecutableFile(Touch(Path.Combine(dir.Path, name))));
        var folder = Path.Combine(dir.Path, "folder", name);
        Directory.CreateDirectory(folder);
        Assert.False(ClaudeCodeLocator.IsExecutableFile(folder));
        Assert.False(ClaudeCodeLocator.IsExecutableFile(Path.Combine(dir.Path, "missing", name)));
    }

    /// <summary>
    /// Windows: the setting's path is cleaned (separators, dots, the drive
    /// letter) and takes the place of the same candidate in any spelling;
    /// the PATH is read whatever the case of its name.
    /// </summary>
    [Fact]
    public void TheSettingsSpellingAndThePath()
    {
        RequireWindows();
        using var dir = new TemporaryDirectory();
        var home = Path.Combine(dir.Path, "home");
        var native = Touch(Path.Combine(home, ".local", "bin", "claude.exe"));
        var onPath = Touch(Path.Combine(dir.Path, "bin", "claude.exe"));
        var settings = Settings();
        // Keys compared exactly, so the lookup has to find "Path" itself.
        var env = new Dictionary<string, string>(StringComparer.Ordinal) { ["USERPROFILE"] = home, ["Path"] = Path.Combine(dir.Path, "bin") };
        var l = Locator(dir.Path, settings, env);
        Assert.Equal([native, onPath], l.Candidates());
        settings.AssistantClaudePath = char.ToLowerInvariant(onPath[0]) + onPath[1..].Replace('\\', '/').Replace("/bin/", "/./x/../BIN/", StringComparison.Ordinal);
        var candidates = l.Candidates();
        Assert.Equal(2, candidates.Count);
        Assert.Equal(onPath, candidates[0], ignoreCase: true);
        Assert.Equal(native, candidates[1]);
        Assert.Equal(onPath, l.Locate(), ignoreCase: true);
        Assert.Equal(native, l.AutomaticPath());
    }
}
