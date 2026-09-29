// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of FakeClaude in macos/Tests/MalachiCoreTests/
// AssistantPanelControllerTests.swift (and the stand-ins of
// ClaudeCodeProcessTests.swift and AssistantRequestTests.swift); GTK:
// ui/internal/assistantpanel harness_test.go. A stand-in claude in a
// fresh directory: it answers --version and auth status --json as
// scripted, recording each call (the locator's stand-in of
// ClaudeCodeProcessTests.swift, `echo "$*" >> calls`); any other command
// line starts a conversation, which records the start, its arguments,
// environment and working directory, runs the start's steps, then runs one
// turn per line of stdin (the turns counted
// over every start, the last repeating) and ends at the end of stdin.
// Swift writes a #!/bin/sh script; on Windows the program is this
// project's, copied into the directory as claude.exe (the apphost loads its
// assembly beside itself, and finds the runtime without an environment
// variable, so the child's minimal environment is enough), with its script
// beside it. The records are JSON where Swift writes lines, so that an
// argument or a value with a newline survives.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using System.Text.Encodings.Web;
using System.Text.Json;

namespace Malachi.FakeClaude;

/// <summary>What a stand-in claude does, and what it recorded.</summary>
public sealed class FakeClaudeScript
{
    /// <summary>The program's file name: claude.exe, as Anthropic's installer names it.</summary>
    public const string FileName = "claude.exe";

    /// <summary>The script beside the program.</summary>
    public const string ScriptFileName = "fake-claude.json";

    /// <summary>A line per conversation started.</summary>
    public const string StartsFileName = "starts";

    /// <summary>The arguments of the last conversation, a JSON array.</summary>
    public const string ArgsFileName = "args.json";

    /// <summary>The environment of the last conversation, a JSON object.</summary>
    public const string EnvFileName = "env.json";

    /// <summary>The working directory of the last conversation.</summary>
    public const string CwdFileName = "cwd";

    /// <summary>Every line read from stdin, over every conversation.</summary>
    public const string StdinFileName = "stdin";

    /// <summary>The arguments of every --version and auth run, a line each.</summary>
    public const string CallsFileName = "calls";

    /// <summary>The version line Swift's stand-in prints.</summary>
    public const string DefaultVersion = "2.1.178 (Claude Code)";

    private const string ProgramName = "Malachi.FakeClaude";

    /// <summary>
    /// A script: <paramref name="turns"/> in order (the last repeats), the
    /// steps of every start, and what --version and auth status print.
    /// </summary>
    public FakeClaudeScript(
        IReadOnlyList<IReadOnlyList<FakeClaudeStep>> turns,
        IReadOnlyList<FakeClaudeStep>? onStart = null,
        IReadOnlyList<FakeClaudeStep>? version = null,
        IReadOnlyList<FakeClaudeStep>? auth = null)
    {
        ArgumentNullException.ThrowIfNull(turns);
        Turns = turns;
        OnStart = onStart ?? [];
        Version = version ?? [FakeClaudeStep.Stdout(DefaultVersion + "\n")];
        Auth = auth ?? SignedIn(true);
    }

    /// <summary>The turns, one per stdin line.</summary>
    public IReadOnlyList<IReadOnlyList<FakeClaudeStep>> Turns { get; }

    /// <summary>The steps of every start, before the first turn.</summary>
    public IReadOnlyList<FakeClaudeStep> OnStart { get; }

    /// <summary>The steps of <c>--version</c>.</summary>
    public IReadOnlyList<FakeClaudeStep> Version { get; }

    /// <summary>The steps of <c>auth status --json</c>.</summary>
    public IReadOnlyList<FakeClaudeStep> Auth { get; }

    /// <summary>What <c>auth status --json</c> prints as Swift's stand-in does.</summary>
    public static IReadOnlyList<FakeClaudeStep> SignedIn(bool loggedIn) =>
        [FakeClaudeStep.Stdout("{\"loggedIn\": " + (loggedIn ? "true" : "false") + "}\n")];

    /// <summary>The conversations started in <paramref name="directory"/> so far.</summary>
    public static int Starts(string directory) => ReadText(directory, StartsFileName).Split('\n', StringSplitOptions.RemoveEmptyEntries).Length;

    /// <summary>The arguments of the last conversation started in <paramref name="directory"/>.</summary>
    public static IReadOnlyList<string> Args(string directory)
    {
        var text = ReadText(directory, ArgsFileName);
        return text.Length == 0 ? [] : JsonSerializer.Deserialize(text, FakeClaudeJson.Default.StringArray) ?? [];
    }

    /// <summary>The environment of the last conversation started in <paramref name="directory"/>.</summary>
    public static IReadOnlyDictionary<string, string> Env(string directory)
    {
        var text = ReadText(directory, EnvFileName);
        return text.Length == 0
            ? new Dictionary<string, string>()
            : JsonSerializer.Deserialize(text, FakeClaudeJson.Default.DictionaryStringString) ?? [];
    }

    /// <summary>The arguments of every --version and auth run in <paramref name="directory"/> so far ("--version", "auth status --json").</summary>
    public static IReadOnlyList<string> Calls(string directory) =>
        ReadText(directory, CallsFileName).Split('\n', StringSplitOptions.RemoveEmptyEntries);

    /// <summary>The working directory of the last conversation started in <paramref name="directory"/>.</summary>
    public static string Cwd(string directory) => ReadText(directory, CwdFileName).Trim();

    /// <summary>Every stdin line of every conversation in <paramref name="directory"/>.</summary>
    public static IReadOnlyList<string> StdinLines(string directory) =>
        ReadText(directory, StdinFileName).Split('\n', StringSplitOptions.RemoveEmptyEntries);

    /// <summary>The text of every turn written to stdin (message.content[0].text of each stream-json line).</summary>
    public static IReadOnlyList<string> Prompts(string directory)
    {
        var prompts = new List<string>();
        foreach (var line in StdinLines(directory))
        {
            try
            {
                using var document = JsonDocument.Parse(line);
                if (document.RootElement.TryGetProperty("message", out var message)
                    && message.TryGetProperty("content", out var content)
                    && content.ValueKind == JsonValueKind.Array && content.GetArrayLength() > 0
                    && content[0].TryGetProperty("text", out var text) && text.GetString() is { } s)
                {
                    prompts.Add(s);
                }
            }
            catch (JsonException)
            {
                // Not a turn.
            }
        }
        return prompts;
    }

    /// <summary>
    /// Puts the stand-in into <paramref name="directory"/>, which exists:
    /// the program as <see cref="FileName"/> and this script beside it.
    /// Returns the program's path.
    /// </summary>
    public string CreateIn(string directory)
    {
        ArgumentNullException.ThrowIfNull(directory);
        if (!OperatingSystem.IsWindows())
        {
            throw new PlatformNotSupportedException("the stand-in claude is a Windows program");
        }
        var source = Path.GetDirectoryName(typeof(FakeClaudeScript).Assembly.Location)
            ?? throw new InvalidOperationException("the stand-in claude's program is not in a directory");
        var claude = Path.Combine(directory, FileName);
        File.WriteAllBytes(Path.Combine(directory, ScriptFileName), Serialize());
        File.Copy(Path.Combine(source, ProgramName + ".exe"), claude);
        foreach (var file in new[] { ".dll", ".runtimeconfig.json", ".deps.json" })
        {
            var from = Path.Combine(source, ProgramName + file);
            if (File.Exists(from))
            {
                File.Copy(from, Path.Combine(directory, ProgramName + file));
            }
        }
        return claude;
    }

    internal static FakeClaudeScript Load(string path)
    {
        using var document = JsonDocument.Parse(File.ReadAllBytes(path));
        var root = document.RootElement;
        return new FakeClaudeScript(
            root.GetProperty("turns").EnumerateArray().Select(FakeClaudeStep.ReadSteps).ToList(),
            FakeClaudeStep.ReadSteps(root.GetProperty("onStart")),
            FakeClaudeStep.ReadSteps(root.GetProperty("version")),
            FakeClaudeStep.ReadSteps(root.GetProperty("auth")));
    }

    private static string ReadText(string directory, string name)
    {
        try
        {
            return File.ReadAllText(Path.Combine(directory, name), Encoding.UTF8);
        }
        catch (Exception e) when (e is FileNotFoundException or DirectoryNotFoundException or IOException)
        {
            return "";
        }
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
            writer.WritePropertyName("turns");
            writer.WriteStartArray();
            foreach (var turn in Turns)
            {
                FakeClaudeStep.WriteSteps(writer, turn);
            }
            writer.WriteEndArray();
            writer.WritePropertyName("onStart");
            FakeClaudeStep.WriteSteps(writer, OnStart);
            writer.WritePropertyName("version");
            FakeClaudeStep.WriteSteps(writer, Version);
            writer.WritePropertyName("auth");
            FakeClaudeStep.WriteSteps(writer, Auth);
            writer.WriteEndObject();
        }
        return buffer.ToArray();
    }
}
