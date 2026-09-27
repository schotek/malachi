// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of FakeBridge in macos/Tests/MalachiCoreTests/MCPRegistrationTests.swift
// and fakeBridge in ui/internal/mcpsetup/mcpsetup_test.go: a stand-in
// malachi-mcp in a fresh directory, answering status, install and
// uninstall as scripted and logging every invocation's arguments to
// "calls" in that directory. Swift writes a #!/bin/sh script there and Go
// re-runs its test binary; on Windows the program is this project's, copied
// into the directory under the bridge's name (the apphost loads its
// assembly beside itself), with its script beside it, so that tests running
// in parallel never share an environment variable. Unscripted install and
// uninstall exit 9, as in Swift.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using System.Text.Encodings.Web;
using System.Text.Json;

namespace Malachi.FakeBridge;

/// <summary>What a stand-in malachi-mcp does for each subcommand.</summary>
public sealed class FakeBridgeScript
{
    /// <summary>Where every invocation's arguments are appended, one line each.</summary>
    public const string CallsFileName = "calls";

    /// <summary>The script beside the program.</summary>
    public const string ScriptFileName = "fake-bridge.json";

    /// <summary>Set in the child of <see cref="FakeBridgeStep.HoldingChild"/>: "path|milliseconds".</summary>
    public const string HoldEnv = "MALACHI_FAKE_BRIDGE_HOLD";

    /// <summary>
    /// The child of <see cref="FakeBridgeStep.HoldingChild"/> writes its
    /// process ID to the hold file's path with this suffix.
    /// </summary>
    public const string ChildPidSuffix = ".pid";

    // Where the #!/bin/sh bridge of a system without apphost copies says
    // its directory is.
    private const string DirectoryEnv = "MALACHI_FAKE_BRIDGE_DIR";

    private const string ProgramName = "Malachi.FakeBridge";

    /// <summary>A script; install and uninstall exit 9 unless given.</summary>
    public FakeBridgeScript(
        IReadOnlyList<FakeBridgeStep> status,
        IReadOnlyList<FakeBridgeStep>? install = null,
        IReadOnlyList<FakeBridgeStep>? uninstall = null)
    {
        ArgumentNullException.ThrowIfNull(status);
        Status = status;
        Install = install ?? [FakeBridgeStep.Exit(9)];
        Uninstall = uninstall ?? [FakeBridgeStep.Exit(9)];
    }

    /// <summary>The bridge's file name: malachi-mcp.exe, as beside the app.</summary>
    public static string BridgeFileName => OperatingSystem.IsWindows() ? "malachi-mcp.exe" : "malachi-mcp";

    /// <summary>The steps of <c>status</c>.</summary>
    public IReadOnlyList<FakeBridgeStep> Status { get; }

    /// <summary>The steps of <c>install</c>.</summary>
    public IReadOnlyList<FakeBridgeStep> Install { get; }

    /// <summary>The steps of <c>uninstall</c>.</summary>
    public IReadOnlyList<FakeBridgeStep> Uninstall { get; }

    /// <summary>
    /// The arguments of every invocation of the bridge in
    /// <paramref name="directory"/> so far, one line each ("status --json").
    /// </summary>
    public static IReadOnlyList<string> Calls(string directory)
    {
        var path = Path.Combine(directory, CallsFileName);
        try
        {
            return File.ReadAllText(path, Encoding.UTF8).Split('\n', StringSplitOptions.RemoveEmptyEntries);
        }
        catch (Exception e) when (e is FileNotFoundException or DirectoryNotFoundException)
        {
            return [];
        }
    }

    /// <summary>
    /// Puts the stand-in into <paramref name="directory"/>, which exists:
    /// the program under <see cref="BridgeFileName"/> and this script beside
    /// it. Returns the bridge's path.
    /// </summary>
    public string CreateIn(string directory)
    {
        ArgumentNullException.ThrowIfNull(directory);
        var source = Path.GetDirectoryName(typeof(FakeBridgeScript).Assembly.Location)
            ?? throw new InvalidOperationException("the stand-in bridge's program is not in a directory");
        var apphost = ProgramName + (OperatingSystem.IsWindows() ? ".exe" : "");
        var bridge = Path.Combine(directory, BridgeFileName);
        File.WriteAllBytes(Path.Combine(directory, ScriptFileName), Serialize());
        if (!OperatingSystem.IsWindows())
        {
            // A #!/bin/sh bridge as in Swift, which runs the program where it
            // is (a self-contained one needs the runtime beside it) and names
            // this directory.
            File.WriteAllText(
                bridge,
                "#!/bin/sh\n" + DirectoryEnv + "=" + Quote(directory) + "\nexport " + DirectoryEnv + "\nexec "
                    + Quote(Path.Combine(source, apphost)) + " \"$@\"\n");
            File.SetUnixFileMode(bridge, UnixFileMode.UserRead | UnixFileMode.UserWrite | UnixFileMode.UserExecute);
            return bridge;
        }
        File.Copy(Path.Combine(source, apphost), bridge);
        foreach (var file in new[] { ".dll", ".runtimeconfig.json", ".deps.json" })
        {
            var from = Path.Combine(source, ProgramName + file);
            if (File.Exists(from))
            {
                File.Copy(from, Path.Combine(directory, ProgramName + file));
            }
        }
        return bridge;
    }

    /// <summary>The directory of the stand-in whose script runs, when it is not the program's own.</summary>
    internal static string DirectoryOf(string? programPath) =>
        Environment.GetEnvironmentVariable(DirectoryEnv) is { Length: > 0 } directory
            ? directory
            : Path.GetDirectoryName(programPath) ?? AppContext.BaseDirectory;

    private static string Quote(string text) => "'" + text.Replace("'", "'\\''", StringComparison.Ordinal) + "'";

    /// <summary>The steps of <paramref name="subcommand"/>, or null for one the bridge does not know.</summary>
    internal IReadOnlyList<FakeBridgeStep>? For(string subcommand) => subcommand switch
    {
        "status" => Status,
        "install" => Install,
        "uninstall" => Uninstall,
        _ => null,
    };

    internal static FakeBridgeScript Load(string path)
    {
        using var document = JsonDocument.Parse(File.ReadAllBytes(path));
        var root = document.RootElement;
        return new FakeBridgeScript(
            FakeBridgeStep.ReadSteps(root.GetProperty("status")),
            FakeBridgeStep.ReadSteps(root.GetProperty("install")),
            FakeBridgeStep.ReadSteps(root.GetProperty("uninstall")));
    }

    private byte[] Serialize()
    {
        using var buffer = new MemoryStream();
        using (var writer = new Utf8JsonWriter(buffer, new JsonWriterOptions
        {
            Encoder = JavaScriptEncoder.UnsafeRelaxedJsonEscaping,
            Indented = true,
        }))
        {
            writer.WriteStartObject();
            FakeBridgeStep.WriteSteps(writer, "status", Status.ToList());
            FakeBridgeStep.WriteSteps(writer, "install", Install.ToList());
            FakeBridgeStep.WriteSteps(writer, "uninstall", Uninstall.ToList());
            writer.WriteEndObject();
        }
        return buffer.ToArray();
    }
}
