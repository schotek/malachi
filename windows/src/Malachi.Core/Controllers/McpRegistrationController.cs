// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Controllers/MCPRegistrationController.swift
// (MCPRegistrationController, invoke, firstLine, exitDescription); GTK:
// ui/internal/window/preferences.go (bindMCP) and
// ui/internal/mcpsetup/mcpsetup.go (Query, Install, Uninstall, run, reason).
//
// Windows differences. The group description reports what GTK reports
// there and macOS only toasts or logs (docs/windows-port.md §3.1, U2 and
// U3): a bridge missing beside the app, and a status the bridge did not
// give; a later status that succeeds puts the page's own text back. Every
// call passes --command with the bridge's canonical path, so the entry
// the bridge writes and compares is spelt the same whichever way the app
// was started, and --claude-desktop-config when the Microsoft Store's
// Claude Desktop is installed (ClaudeDesktopPackage; docs/mcp.md). A
// status is a read and ends with the page (the run is killed on Close); an
// install or uninstall runs to its end, as Swift's does. A process that
// was killed exits with -1 and a crash with its NTSTATUS (ExitStatus),
// where Swift reports a signal. The reason is cut as Go's reason cuts it
// (FirstLine). The Swift callbacks are events of the same
// words (onRegistered is RegisteredChanged, onEnabled EnabledChanged,
// onToast ToastRequested), and DescriptionChanged is Windows' own.

using System;
using System.Collections.Generic;
using System.IO;
using System.Security;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using CommunityToolkit.Mvvm.ComponentModel;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Daemon;
using Malachi.Core.I18n;
using Malachi.Core.Platform;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Logging.Abstractions;

namespace Malachi.Core.Controllers;

/// <summary>
/// The MCP group of the AI page of the preferences (preferences.blp
/// <c>ai_page</c>, preferences.go <c>bindMCP</c>) without the widgets: one
/// switch, "Register with Claude", that adds the bundled
/// <c>malachi-mcp.exe</c> to the MCP configuration of Claude Desktop and
/// Claude Code, or takes it out, through the bridge's own setup subcommands
/// (<c>malachi-mcp status | install | uninstall --json</c>; docs/mcp.md).
/// The application never touches those files itself.
/// </summary>
/// <remarks>
/// The switch is on when at least one client reports the bridge as
/// registered. The row is insensitive before the first status and while a
/// call runs; a failed install or uninstall shows a toast (the bridge's
/// one-line reason, or that no Claude app is installed) and the switch goes
/// back to the last state the bridge confirmed. A failed status check puts
/// its reason into the group's description and the row stays insensitive
/// until the page comes up again and asks once more. Without a bridge
/// beside the application (<see cref="Paths.McpBridge"/> null) the row stays
/// insensitive and the description says so, once. A status asked while a
/// call runs is skipped (that call's answer is the newer status); the reply
/// of a call a newer one overtook, and every reply after
/// <see cref="Close"/>, is dropped. Create it, and call it, on the UI
/// thread.
/// </remarks>
public sealed partial class McpRegistrationController : ObservableObject, IDisposable
{
    /// <summary>The most of the bridge's stderr that is kept: its first line, cut at this many bytes.</summary>
    public const int ReasonLimit = 200;

    /// <summary>How long one bridge call may take (mcpsetup.Timeout).</summary>
    public static readonly TimeSpan DefaultTimeout = BridgeRunner.DefaultTimeout;

    // How install says that neither Claude app is installed: the start of
    // its one-line reason on stderr, compared case-insensitively.
    private const string NoClaudeAppPrefix = "no claude app found";

    private readonly string? bridge;
    private readonly BridgeRunner runner;
    private readonly TimeSpan timeout;
    private readonly ClaudeDesktopPackage claudeDesktop;
    private readonly ControllerScope scope;
    private readonly ILogger logger;

    // Bumped by every call; the reply of an older one is dropped.
    private int op;
    private bool inFlight;
    private bool reportedMissing;

    /// <summary>A controller for <paramref name="bridge"/>, on the calling (UI) thread.</summary>
    /// <param name="bridge">
    /// <c>malachi-mcp.exe</c> beside the application (<see cref="Paths.McpBridge"/>),
    /// or null when there is none. It is registered, and run, by its
    /// <see cref="CanonicalPath"/>.
    /// </param>
    /// <param name="runner">How the bridge is run; the default runs the real thing.</param>
    /// <param name="timeout">How long one call may take (<see cref="DefaultTimeout"/>).</param>
    /// <param name="claudeDesktop">The Microsoft Store's Claude Desktop (<see cref="ClaudeDesktopPackage.ForCurrentUser"/>).</param>
    /// <param name="logger">Receives the subcommand and the kind of a failure, never a path or a reason.</param>
    /// <param name="pending">Counts the controller's background work; one of its own when null.</param>
    public McpRegistrationController(
        string? bridge,
        BridgeRunner? runner = null,
        TimeSpan? timeout = null,
        ClaudeDesktopPackage? claudeDesktop = null,
        ILogger<McpRegistrationController>? logger = null,
        PendingWork? pending = null)
    {
        this.bridge = bridge is null ? null : CanonicalPath(bridge);
        this.runner = runner ?? new BridgeRunner();
        this.timeout = timeout ?? DefaultTimeout;
        this.claudeDesktop = claudeDesktop ?? ClaudeDesktopPackage.ForCurrentUser();
        this.logger = (ILogger?)logger ?? NullLogger.Instance;
        scope = new ControllerScope(pending);
    }

    /// <summary>
    /// Called with the state to show after every answered call (Swift
    /// <c>onRegistered</c>): the bridge's status, or the previous state again
    /// when the call failed, so the switch reverts.
    /// </summary>
    public event EventHandler<bool>? RegisteredChanged;

    /// <summary>Called on every change of the row's sensitivity (Swift <c>onEnabled</c>).</summary>
    public event EventHandler<bool>? EnabledChanged;

    /// <summary>Called with the text of a toast, a failed install or uninstall (Swift <c>onToast</c>).</summary>
    public event EventHandler<string>? ToastRequested;

    /// <summary>
    /// Called when the group's description changes (Windows, as GTK's
    /// <c>mcpGroup.SetDescription</c>): the text that replaces the page's own,
    /// or null for the page's own again.
    /// </summary>
    public event EventHandler<string?>? DescriptionChanged;

    /// <summary>The subcommands of the bridge (Swift <c>Command</c>).</summary>
    public enum Command
    {
        /// <summary>What is installed and what is registered.</summary>
        Status,

        /// <summary>Register with every Claude app found.</summary>
        Install,

        /// <summary>Remove the registration.</summary>
        Uninstall,
    }

    /// <summary>The last status the bridge reported; null until the first answered.</summary>
    [ObservableProperty]
    public partial McpStatus? Status { get; private set; }

    /// <summary>What the switch shows: the last state the bridge confirmed.</summary>
    [ObservableProperty]
    public partial bool IsRegistered { get; private set; }

    /// <summary>The row's sensitivity: a bridge is there, its status is known and no call is in flight.</summary>
    [ObservableProperty]
    public partial bool IsEnabled { get; private set; }

    /// <summary>What replaces the group's description, null for the page's own (Windows; U2, U3).</summary>
    [ObservableProperty]
    public partial string? Description { get; private set; }

    /// <summary>The bridge as it is registered and run (<see cref="CanonicalPath"/>); null without one.</summary>
    public string? Bridge => bridge;

    /// <summary>The window closed: late replies are dropped (Swift <c>closed</c>).</summary>
    public bool IsClosed => scope.IsClosed;

    /// <summary>
    /// The arguments of one call: the subcommand, <c>--json</c>,
    /// <c>--command</c> with <paramref name="commandPath"/>, and
    /// <c>--claude-desktop-config</c> with <paramref name="claudeDesktopConfig"/>
    /// when it is set (docs/mcp.md: the path flags go to every call, status
    /// included, or the report describes the defaults).
    /// </summary>
    public static IReadOnlyList<string> Arguments(Command command, string commandPath, string? claudeDesktopConfig)
    {
        ArgumentException.ThrowIfNullOrEmpty(commandPath);
        List<string> arguments = [CommandName(command), "--json", "--command", commandPath];
        if (claudeDesktopConfig is not null)
        {
            arguments.Add("--claude-desktop-config");
            arguments.Add(claudeDesktopConfig);
        }
        return arguments;
    }

    /// <summary>The subcommand's name on the bridge's command line.</summary>
    public static string CommandName(Command command) => command switch
    {
        Command.Install => "install",
        Command.Uninstall => "uninstall",
        _ => "status",
    };

    /// <summary>
    /// The first line of the bridge's stderr, trimmed, at most
    /// <paramref name="limit"/> UTF-8 bytes and never cut inside a character
    /// (Swift <c>firstLine</c>, mcpsetup <c>reason</c>).
    /// </summary>
    /// <remarks>
    /// The order is Go's: the text is trimmed before the line is cut, and the
    /// cut keeps every character that fits. Swift cuts first and then drops
    /// the bytes of a character that ends at the limit as if it were split,
    /// so a reason with leading spaces, or one whose limit falls right after
    /// a character of several bytes, came out shorter than Go's
    /// (TestExitReasonIsFirstLineBounded); both suites' cases hold here.
    /// </remarks>
    public static string FirstLine(ReadOnlySpan<byte> data, int limit = ReasonLimit)
    {
        var text = Encoding.UTF8.GetString(data).Trim();
        var newline = text.IndexOf('\n', StringComparison.Ordinal);
        var line = (newline < 0 ? text : text[..newline]).Trim();
        var bytes = 0;
        var length = 0;
        foreach (var rune in line.EnumerateRunes())
        {
            if (bytes + rune.Utf8SequenceLength > limit)
            {
                break;
            }
            bytes += rune.Utf8SequenceLength;
            length += rune.Utf16SequenceLength;
        }
        return line[..length];
    }

    /// <summary>
    /// How the bridge ended without a reason (Swift <c>exitDescription</c>):
    /// "malachi-mcp exited with status 3", "malachi-mcp was killed",
    /// "malachi-mcp died with status 0xC0000005" (<see cref="ExitStatus.Describe"/>).
    /// </summary>
    public static string ExitDescription(int status) => "malachi-mcp " + ExitStatus.Describe(status);

    /// <summary>
    /// The one spelling of <paramref name="path"/> every call uses: absolute
    /// and cleaned, short (8.3) names expanded, and on Windows every
    /// component in the case the file system has it, so a start through
    /// another spelling of the same folder does not make the registration
    /// look like someone else's (docs/mcp.md: <c>registered</c> compares the
    /// command). Links are not resolved; a component that cannot be read is
    /// kept as given.
    /// </summary>
    public static string CanonicalPath(string path)
    {
        ArgumentException.ThrowIfNullOrEmpty(path);
        var full = Path.GetFullPath(path);
        if (!OperatingSystem.IsWindows())
        {
            return full;
        }
        var root = Path.GetPathRoot(full) ?? "";
        var current = root.Length >= 2 && root[1] == ':' ? char.ToUpperInvariant(root[0]) + root[1..] : root;
        foreach (var name in full[root.Length..].Split(Path.DirectorySeparatorChar, StringSplitOptions.RemoveEmptyEntries))
        {
            current = Path.Combine(current, NameOnDisk(current, name));
        }
        return current;
    }

    /// <summary>Drops every reply still in flight and ends a status run; nothing is emitted afterwards.</summary>
    public void Close()
    {
        scope.VerifyAccess();
        scope.Close();
    }

    /// <summary>Closes the controller.</summary>
    public void Dispose() => Close();

    /// <summary>
    /// Asks <c>status</c>; the page calls it whenever it comes up. Skipped
    /// while a call runs (its answer is the status). Without a bridge the
    /// row stays insensitive and the description says so, once.
    /// </summary>
    public void Load()
    {
        scope.VerifyAccess();
        if (IsClosed)
        {
            return;
        }
        if (bridge is null)
        {
            ReportMissing();
            return;
        }
        if (inFlight)
        {
            return;
        }
        Run(Command.Status, bridge);
    }

    /// <summary>
    /// Runs <c>install</c> or <c>uninstall</c> and shows what the bridge
    /// reports afterwards; on failure the switch goes back and a toast says
    /// why (Swift <c>set(registered:)</c>).
    /// </summary>
    public void SetRegistered(bool want)
    {
        scope.VerifyAccess();
        if (IsClosed)
        {
            return;
        }
        if (bridge is null)
        {
            ReportMissing();
            RegisteredChanged?.Invoke(this, IsRegistered);
            return;
        }
        Run(want ? Command.Install : Command.Uninstall, bridge);
    }

    private void Run(Command command, string path)
    {
        var my = ++op;
        inFlight = true;
        SetEnabled(false);
        var run = runner;
        var limit = timeout;
        var desktop = claudeDesktop;
        // Off the UI thread, as Swift's nonisolated invoke: the process is
        // started and the package looked for from the thread pool.
        Task<(McpStatus? Status, McpCallFailure? Failure)> Invoke(CancellationToken cancellationToken) =>
            Task.Run(() => InvokeAsync(run, path, command, desktop, limit, cancellationToken), CancellationToken.None);
        void Done(Outcome<(McpStatus? Status, McpCallFailure? Failure)> outcome) => Answered(my, command, outcome);
        if (command == Command.Status)
        {
            scope.Perform(Invoke, Done);
        }
        else
        {
            // A registration change is not recalled when the page closes.
            scope.PerformPastClose(() => Invoke(CancellationToken.None), Done);
        }
    }

    private void Answered(int my, Command command, Outcome<(McpStatus? Status, McpCallFailure? Failure)> outcome)
    {
        if (my != op)
        {
            return;
        }
        inFlight = false;
        var (status, failure) = outcome.TryGetValue(out var result, out var error)
            ? result
            : (null, new McpCallFailure.Run(error!.Message));
        if (status is not null)
        {
            Status = status;
            IsRegistered = status.IsRegistered;
            // The bridge answers again: the page's own description is back.
            SetDescription(null);
        }
        else if (failure is not null)
        {
            LogCallFailed(logger, CommandName(command), failure.GetType().Name);
            if (command == Command.Status)
            {
                // The group says so, as GTK's does (preferences.go bindMCP);
                // the row stays insensitive until the page asks again.
                // TRANSLATORS: %s is a one-line reason from the malachi-mcp bridge.
                SetDescription(L10n.T("The MCP bridge did not answer: %s", failure.Reason));
                return;
            }
            ToastRequested?.Invoke(this, failure.Toast(registering: command == Command.Install));
        }
        SetEnabled(true);
        RegisteredChanged?.Invoke(this, IsRegistered);
    }

    // No bridge beside the application: the row stays insensitive and the
    // description says so, once.
    private void ReportMissing()
    {
        if (reportedMissing)
        {
            return;
        }
        reportedMissing = true;
        EnabledChanged?.Invoke(this, false);
        SetDescription(L10n.T("The MCP bridge (malachi-mcp) was not found"));
    }

    private void SetEnabled(bool on)
    {
        if (on == IsEnabled)
        {
            return;
        }
        IsEnabled = on;
        EnabledChanged?.Invoke(this, on);
    }

    private void SetDescription(string? text)
    {
        if (text == Description)
        {
            return;
        }
        Description = text;
        DescriptionChanged?.Invoke(this, text);
    }

    // Runs one subcommand and reads its status (Swift invoke): a failure is
    // a result, never an exception, except the end of a status with the page.
    private static async Task<(McpStatus? Status, McpCallFailure? Failure)> InvokeAsync(
        BridgeRunner runner, string bridge, Command command, ClaudeDesktopPackage desktop, TimeSpan timeout, CancellationToken cancellationToken)
    {
        BridgeRunnerOutput output;
        try
        {
            var arguments = Arguments(command, bridge, desktop.IsInstalled ? desktop.ConfigPath : null);
            output = await runner.RunAsync(bridge, arguments, timeout, cancellationToken);
        }
        catch (BridgeRunnerException e)
        {
            return (null, new McpCallFailure.Run(e.Message));
        }
        var reason = FirstLine(output.Stderr.Span);
        if (output.Status != 0)
        {
            if (reason.StartsWith(NoClaudeAppPrefix, StringComparison.OrdinalIgnoreCase))
            {
                return (null, new McpCallFailure.NoClaudeApp());
            }
            return (null, new McpCallFailure.Failed(reason.Length == 0 ? ExitDescription(output.Status) : reason));
        }
        try
        {
            return (McpStatus.Decode(output.Stdout.Span), null);
        }
        catch (JsonException)
        {
            return (null, new McpCallFailure.Failed("unexpected output from malachi-mcp " + CommandName(command)));
        }
    }

    // The name of one entry of directory as the file system spells it; the
    // given name when the directory cannot be read or has no such entry.
    private static string NameOnDisk(string directory, string name)
    {
        try
        {
            var options = new EnumerationOptions
            {
                MatchCasing = MatchCasing.CaseInsensitive,
                MatchType = MatchType.Simple,
                AttributesToSkip = 0,
                IgnoreInaccessible = true,
            };
            foreach (var entry in Directory.EnumerateFileSystemEntries(directory, name, options))
            {
                var found = Path.GetFileName(entry);
                if (string.Equals(found, name, StringComparison.OrdinalIgnoreCase))
                {
                    return found;
                }
            }
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException or SecurityException or ArgumentException)
        {
            // Unreadable: kept as given.
        }
        return name;
    }

    [LoggerMessage(Level = LogLevel.Warning, Message = "malachi-mcp {Command} failed: {Failure}")]
    private static partial void LogCallFailed(ILogger logger, string command, string failure);
}
