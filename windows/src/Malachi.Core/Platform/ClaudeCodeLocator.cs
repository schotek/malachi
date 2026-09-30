// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Platform/ClaudeCodeLocator.swift
// (ClaudeCodeLocator: candidates, locate, isExecutableFile, refresh,
// version, signedIn, signIn, cancelSignIn, signingIn, run,
// ensureDirectory); GTK: ui/internal/assistantpanel/locator.go (Locator,
// Candidates, Locate, AutomaticPath, IsExecutableFile, Refresh, Version,
// SignedIn, SignIn, CancelSignIn, SigningIn, OnSignInChange, run).
//
// Windows differences (decided with the owner):
// - Only claude.exe runs (Assistant.CandidatePaths, IsExecutableFile):
//   npm's claude.cmd would go through cmd.exe, whose parsing of a command
//   line cannot carry the JSON arguments of Assistant.Args safely. A usable
//   candidate is an existing file (not a directory; a link counts by its
//   target, and its own path is kept) whose name ends with ".exe"; Windows
//   has no execute bit to ask for.
// - The setting's path is taken when Assistant.CleanWindowsPath accepts it
//   (drive-absolute, where Swift and Go ask path.IsAbs), cleaned so; a
//   candidate of the same path is dropped without regard to case, as the
//   file system compares it.
// - The home is USERPROFILE of the given environment (Go's os.UserHomeDir
//   on Windows), else the system's profile folder; PATH is looked up
//   without case (Windows spells it Path). There are no nvm versions to
//   list (CandidatePaths has none).
// - The private working directory of the runs is made by an
//   IPrivateDirectoryFactory (a protected DACL, as the open directory's)
//   instead of mode 0700; without a factory (the tests) by
//   Directory.CreateDirectory. It is made off the UI thread with the run.
// - AutomaticPath is GTK's (Swift's AIPaneViewController looks for the
//   automatic one itself).
// - A run goes through BridgeRunner off the UI thread (Swift's
//   Task.detached); only the kind of its failure is logged, and never
//   anything claude printed: the output of auth status holds the account's
//   e-mail address.
// - The sign-in (SignInAsync) is a run of the same runner with the sign-in's
//   timeout and a cancellation of its own: out of time or cancelled, the
//   process is killed with every process it started, at once (there is no
//   SIGTERM to send first). Its environment is ChildEnvironment (Windows
//   has no SignInEnv, Assistant.SignIn.cs). GTK's watchers are the event
//   SigningInChanged; its callback and cancel function are the task and
//   CancelSignIn. The task continues on the caller's (UI) thread, where the
//   locator's state lives.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Malachi.Core.Daemon;
using Malachi.Core.Settings;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Platform;

/// <summary>
/// Finds the user's Claude Code for the assistant panel (the In App target)
/// and asks it two things: its version and whether it is signed in;
/// <see cref="SignInAsync"/> runs Claude Code's own sign-in. Malachi Mail
/// never reads a credential and never shows the account: <c>claude auth
/// status --json</c> is read for its <c>loggedIn</c> only, and <c>claude
/// auth login</c> does the signing in.
/// </summary>
/// <remarks>
/// <para>
/// <see cref="Locate"/> looks every time (a few file lookups): the path of
/// the <c>assistant-claude-path</c> setting first, then
/// <see cref="Assistant.CandidatePaths"/>, and returns the first that is
/// usable (<see cref="IsExecutableFile"/>).
/// </para>
/// <para>
/// <see cref="VersionAsync"/> (<c>claude --version</c>, first line) and
/// <see cref="SignedInAsync"/> run the executable once each through
/// <see cref="BridgeRunner"/>, with <see cref="Assistant.ChildEnvironment"/>,
/// in the panel's private directory, bounded by the timeout; their answers
/// are kept per path until <see cref="Refresh"/>. A run that fails answers
/// null (not known), which the panel treats as "go ahead" and the settings
/// as nothing to show. Call it on the UI thread.
/// </para>
/// </remarks>
public sealed partial class ClaudeCodeLocator
{
    /// <summary>How long one run of <c>claude --version</c> or <c>claude auth status</c> may take.</summary>
    public static readonly TimeSpan DefaultTimeout = TimeSpan.FromSeconds(10);

    /// <summary>How long the browser is waited for in a sign-in.</summary>
    public static readonly TimeSpan DefaultSignInTimeout = TimeSpan.FromMinutes(10);

    /// <summary>The most of the version line that is kept, in bytes.</summary>
    public const int VersionLimit = 100;

    private static readonly string[] VersionArguments = ["--version"];
    private static readonly string[] AuthArguments = ["auth", "status", "--json"];

    private readonly IReadOnlyDictionary<string, string> environment;
    private readonly BridgeRunner runner;
    private readonly TimeSpan timeout;
    private readonly TimeSpan signInTimeout;
    private readonly string? directory;
    private readonly IPrivateDirectoryFactory? directories;
    private readonly Func<string, bool> usable;
    private readonly ILogger logger;

    private Dictionary<string, Task<string?>> versions = new(StringComparer.OrdinalIgnoreCase);
    private Dictionary<string, Task<bool?>> signIns = new(StringComparer.OrdinalIgnoreCase);

    // The sign-in under way: what ends it, and its task.
    private CancellationTokenSource? signIn;
    private Task<ClaudeCodeSignIn>? signInTask;

    /// <summary>A locator that reads the setting from <paramref name="settings"/>.</summary>
    /// <param name="settings">Where <c>assistant-claude-path</c> is read.</param>
    /// <param name="environment">
    /// The application's environment (USERPROFILE, PATH, and what
    /// <see cref="Assistant.ChildEnvironment"/> keeps for the child); this
    /// process's when null.
    /// </param>
    /// <param name="runner">How claude is run; the default runs the real thing.</param>
    /// <param name="timeout">How long one run may take (<see cref="DefaultTimeout"/>).</param>
    /// <param name="directory">
    /// The working directory of the runs, fully qualified (the panel's
    /// private directory, <see cref="Paths.AssistantDir"/>), created on
    /// demand; null leaves the application's.
    /// </param>
    /// <param name="directories">Makes <paramref name="directory"/> private; Directory.CreateDirectory when null.</param>
    /// <param name="usable">Whether a candidate is the one (<see cref="IsExecutableFile"/>; the tests keep it to their own directory).</param>
    /// <param name="logger">Receives the kind of a failed run, never its output.</param>
    /// <param name="signInTimeout">How long a sign-in waits for the browser (<see cref="DefaultSignInTimeout"/>).</param>
    public ClaudeCodeLocator(
        SettingsStore settings,
        IReadOnlyDictionary<string, string>? environment = null,
        BridgeRunner? runner = null,
        TimeSpan? timeout = null,
        string? directory = null,
        IPrivateDirectoryFactory? directories = null,
        Func<string, bool>? usable = null,
        ILogger<ClaudeCodeLocator>? logger = null,
        TimeSpan? signInTimeout = null)
    {
        ArgumentNullException.ThrowIfNull(settings);
        Settings = settings;
        this.environment = environment ?? ProcessEnvironment.Copy(ProcessEnvironment.Current());
        this.runner = runner ?? new BridgeRunner();
        this.timeout = timeout ?? DefaultTimeout;
        this.signInTimeout = signInTimeout ?? DefaultSignInTimeout;
        this.directory = directory;
        this.directories = directories;
        this.usable = usable ?? IsExecutableFile;
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
    }

    /// <summary>A sign-in started or ended (<see cref="SigningIn"/>); raised on the thread of <see cref="SignInAsync"/>'s caller.</summary>
    public event EventHandler? SigningInChanged;

    /// <summary>Where <c>assistant-claude-path</c> is read.</summary>
    public SettingsStore Settings { get; }

    /// <summary>Whether a sign-in is under way.</summary>
    public bool SigningIn => signIn is not null;

    /// <summary>
    /// Whether <paramref name="path"/> is a claude that Windows runs: an
    /// existing file (not a directory; a link counts by its final target,
    /// which must be a file) whose name ends with ".exe", compared without
    /// case.
    /// </summary>
    public static bool IsExecutableFile(string path)
    {
        ArgumentNullException.ThrowIfNull(path);
        if (!path.EndsWith(".exe", StringComparison.OrdinalIgnoreCase))
        {
            return false;
        }
        try
        {
            var info = new FileInfo(path);
            if (!info.Exists)
            {
                return false; // missing, or a directory
            }
            if (info.LinkTarget is null)
            {
                return true;
            }
            return info.ResolveLinkTarget(returnFinalTarget: true) is FileInfo { Exists: true };
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException or ArgumentException or NotSupportedException)
        {
            // A path Windows refuses, or a loop of links.
            return false;
        }
    }

    /// <summary>Every path looked at, in order: the setting's, then the usual places.</summary>
    public IReadOnlyList<string> Candidates()
    {
        var list = Automatic().ToList();
        if (Assistant.CleanWindowsPath(Settings.AssistantClaudePath) is { } chosen)
        {
            list.RemoveAll(p => string.Equals(p, chosen, StringComparison.OrdinalIgnoreCase));
            list.Insert(0, chosen);
        }
        return list;
    }

    /// <summary>The claude executable to run; null when there is none.</summary>
    public string? Locate() => Candidates().FirstOrDefault(usable);

    /// <summary>
    /// The claude executable found without the setting: the usual places,
    /// then the PATH (null for none). The settings store nothing when the
    /// user chooses this one (GTK <c>AutomaticPath</c>).
    /// </summary>
    public string? AutomaticPath() => Automatic().FirstOrDefault(usable);

    /// <summary>Forgets the versions and sign-in states asked so far.</summary>
    public void Refresh()
    {
        versions = new(StringComparer.OrdinalIgnoreCase);
        signIns = new(StringComparer.OrdinalIgnoreCase);
    }

    /// <summary>
    /// <c>claude --version</c>'s first line for the located executable
    /// ("2.1.178 (Claude Code)", at most <see cref="VersionLimit"/> bytes);
    /// null when there is none or it failed.
    /// </summary>
    public Task<string?> VersionAsync()
    {
        if (Locate() is not { } path)
        {
            return Task.FromResult<string?>(null);
        }
        if (versions.TryGetValue(path, out var known))
        {
            return known;
        }
        var task = Run<string?>(path, VersionArguments, static (stdout, status) =>
        {
            if (status != 0)
            {
                return null;
            }
            var line = McpRegistrationController.FirstLine(stdout.Span, VersionLimit);
            return line.Length == 0 ? null : line;
        });
        versions[path] = task;
        return task;
    }

    /// <summary>
    /// Whether the located Claude Code is signed in (<c>claude auth status
    /// --json</c>, its <c>loggedIn</c>); null when there is none, the run
    /// failed or the output said nothing.
    /// </summary>
    public Task<bool?> SignedInAsync()
    {
        if (Locate() is not { } path)
        {
            return Task.FromResult<bool?>(null);
        }
        if (signIns.TryGetValue(path, out var known))
        {
            return known;
        }
        // The status may be non-zero when signed out; the JSON counts.
        var task = Run(path, AuthArguments, static (stdout, _) => LoggedIn(stdout));
        signIns[path] = task;
        return task;
    }

    /// <summary>
    /// Runs Claude Code's own sign-in for the located executable (<c>claude
    /// auth login</c>, <see cref="Assistant.SignInArguments"/>, with
    /// <see cref="Assistant.ChildEnvironment"/> in the panel's private
    /// directory).
    /// </summary>
    /// <remarks>
    /// <para>
    /// Claude Code opens the browser, the user signs in to Claude there, and
    /// Claude Code stores the sign-in itself. Malachi Mail only waits for the
    /// process to end: it sees no credential, and what the process prints is
    /// neither shown nor logged (the address it names belongs to the
    /// sign-in; only stderr's first line is the reason of a failure). The
    /// answers kept for <see cref="SignedInAsync"/> are forgotten when it
    /// ends, so the next question asks afresh.
    /// </para>
    /// <para>
    /// One sign-in at a time, for the panel and the settings alike: a new one
    /// takes the place of the one under way, which ends as
    /// <see cref="ClaudeCodeSignIn.Cancelled"/>, as does one that
    /// <see cref="CancelSignIn()"/> ends. Call it on the UI thread; it
    /// continues there.
    /// </para>
    /// </remarks>
    public Task<ClaudeCodeSignIn> SignInAsync()
    {
        CancelSignIn();
        if (Locate() is not { } path)
        {
            return Task.FromResult<ClaudeCodeSignIn>(new ClaudeCodeSignIn.NotFound());
        }
        var mine = new CancellationTokenSource();
        signIn = mine;
        var task = RunSignInAsync(path, mine);
        if (signIn == mine)
        {
            signInTask = task;
            SigningInChanged?.Invoke(this, EventArgs.Empty);
        }
        return task;
    }

    // The sign-in's run: claude auth login off the UI thread, its end back
    // on it.
    private async Task<ClaudeCodeSignIn> RunSignInAsync(string path, CancellationTokenSource mine)
    {
        using var disposal = mine;
        var env = Assistant.ChildEnvironment(environment, path);
        var token = mine.Token;
        ClaudeCodeSignIn result;
        try
        {
            var output = await Task.Run(async () =>
            {
                var dir = EnsureDirectory() ? directory : null;
                return await runner.RunAsync(path, Assistant.SignInArguments, signInTimeout, env, dir, token).ConfigureAwait(false);
            });
            result = output.Status == 0
                ? new ClaudeCodeSignIn.Done()
                : new ClaudeCodeSignIn.Failed(
                    new ClaudeCodeExit(output.Status, McpRegistrationController.FirstLine(output.Stderr.Span, ClaudeCodeProcess.ReasonLimit)).Description);
        }
        catch (OperationCanceledException)
        {
            result = new ClaudeCodeSignIn.Cancelled();
        }
        catch (BridgeRunnerException e) when (e.Failure == BridgeRunnerFailure.Timeout)
        {
            result = new ClaudeCodeSignIn.TimedOut();
        }
        catch (BridgeRunnerException e)
        {
            // The system's reason, without the runner's own name for what it ran.
            result = new ClaudeCodeSignIn.Failed("claude could not be started: " + (e.InnerException?.Message ?? e.Message));
        }
        if (result is not ClaudeCodeSignIn.Done)
        {
            // The outcome alone: stderr may name the account.
            LogSignIn(logger, result.GetType().Name);
        }
        if (signIn != mine)
        {
            // Cancelled, or another sign-in took its place: reported then.
            return new ClaudeCodeSignIn.Cancelled();
        }
        signIn = null;
        signInTask = null;
        Refresh();
        SigningInChanged?.Invoke(this, EventArgs.Empty);
        return result;
    }

    /// <summary>
    /// Ends the sign-in <paramref name="run"/> (a task of
    /// <see cref="SignInAsync"/>) when it is the one under way; a sign-in
    /// that is over, or that another took the place of, ends no other.
    /// </summary>
    public void CancelSignIn(Task<ClaudeCodeSignIn> run)
    {
        if (ReferenceEquals(signInTask, run))
        {
            CancelSignIn();
        }
    }

    /// <summary>
    /// Ends the sign-in under way, which is reported as
    /// <see cref="ClaudeCodeSignIn.Cancelled"/>; nothing without one.
    /// </summary>
    public void CancelSignIn()
    {
        if (signIn is not { } run)
        {
            return;
        }
        signIn = null;
        signInTask = null;
        try
        {
            run.Cancel();
        }
        catch (ObjectDisposedException)
        {
            // It ended in this very moment.
        }
        Refresh();
        SigningInChanged?.Invoke(this, EventArgs.Empty);
    }

    // The usual places, in order.
    private IReadOnlyList<string> Automatic() => Assistant.CandidatePaths(Home(), Variable("PATH") ?? "");

    private string Home() =>
        Variable("USERPROFILE") is { Length: > 0 } home ? home : Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);

    // A variable of the environment, its name compared without case as
    // Windows compares it.
    private string? Variable(string name)
    {
        if (environment.TryGetValue(name, out var value))
        {
            return value;
        }
        foreach (var (key, v) in environment)
        {
            if (string.Equals(key, name, StringComparison.OrdinalIgnoreCase))
            {
                return v;
            }
        }
        return null;
    }

    // The loggedIn of auth status's JSON object: null when it is not an
    // object, has no such boolean (a null is none) or is not JSON at all.
    private static bool? LoggedIn(ReadOnlyMemory<byte> json)
    {
        try
        {
            using var document = JsonDocument.Parse(json);
            if (document.RootElement.ValueKind != JsonValueKind.Object)
            {
                return null;
            }
            bool? loggedIn = null;
            foreach (var property in document.RootElement.EnumerateObject())
            {
                if (!property.NameEquals("loggedIn"u8))
                {
                    continue;
                }
                switch (property.Value.ValueKind)
                {
                    case JsonValueKind.True:
                        loggedIn = true;
                        break;
                    case JsonValueKind.False:
                        loggedIn = false;
                        break;
                    case JsonValueKind.Null:
                        loggedIn = null;
                        break;
                    default:
                        return null; // not a boolean: the decoding fails
                }
            }
            return loggedIn;
        }
        catch (JsonException)
        {
            return null;
        }
    }

    // Runs claude with arguments off the UI thread and reads its stdout and
    // status with read; the default when it could not run or timed out.
    private Task<T> Run<T>(string path, string[] arguments, Func<ReadOnlyMemory<byte>, int, T> read)
    {
        var env = Assistant.ChildEnvironment(environment, path);
        return Task.Run(async () =>
        {
            var dir = EnsureDirectory() ? directory : null;
            try
            {
                var output = await runner.RunAsync(path, arguments, timeout, env, dir).ConfigureAwait(false);
                return read(output.Stdout, output.Status);
            }
            catch (BridgeRunnerException e)
            {
                LogRunFailed(logger, arguments[0], e.Failure);
                return default!;
            }
        });
    }

    // Creates the private directory when it is missing; false when it
    // cannot be had (the run then takes the application's).
    private bool EnsureDirectory()
    {
        if (directory is null)
        {
            return false;
        }
        try
        {
            if (directories is null)
            {
                Directory.CreateDirectory(directory);
            }
            else
            {
                directories.Ensure(directory);
            }
            return true;
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException or ArgumentException or NotSupportedException or InvalidOperationException)
        {
            LogDirectoryFailed(logger, e.GetType().Name, e.HResult);
            return false;
        }
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "claude auth login: {Outcome}")]
    private static partial void LogSignIn(ILogger logger, string outcome);

    [LoggerMessage(Level = LogLevel.Warning, Message = "claude {Argument} failed: {Failure}")]
    private static partial void LogRunFailed(ILogger logger, string argument, BridgeRunnerFailure failure);

    [LoggerMessage(Level = LogLevel.Warning, Message = "the assistant's directory could not be made: {Kind} 0x{HResult:X8}")]
    private static partial void LogDirectoryFailed(ILogger logger, string kind, int hResult);
}
