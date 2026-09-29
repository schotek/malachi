// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the #!/bin/sh stand-in claude of macos/Tests/MalachiCoreTests/
// AssistantPanelControllerTests.swift (FakeClaude): --version and auth run
// their steps; anything else is a conversation that records its start,
// arguments, environment and working directory, runs the start's steps,
// then a turn per stdin line (recorded; the turns counted over every start
// of the directory, the last repeating) and exits 0 at the end of stdin.
// Everything is read and written as UTF-8 bytes: the console's code page
// never touches a turn.

using System;
using System.Collections;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Text;
using System.Text.Json;
using System.Threading;

namespace Malachi.FakeClaude;

internal static class Program
{
    private static readonly Stream StandardOutput = Console.OpenStandardOutput();
    private static readonly Stream StandardError = Console.OpenStandardError();
    private static readonly StreamReader StandardInput = new(Console.OpenStandardInput(), new UTF8Encoding(false));

    private static int Main(string[] args)
    {
        var directory = Path.GetDirectoryName(Environment.ProcessPath) ?? AppContext.BaseDirectory;
        var script = FakeClaudeScript.Load(Path.Combine(directory, FakeClaudeScript.ScriptFileName));
        if (args.Length > 0 && args[0] == "--version")
        {
            return Run(script.Version, 0) ?? 0;
        }
        if (args.Length > 0 && args[0] == "auth")
        {
            return Run(script.Auth, 0) ?? 0;
        }

        File.AppendAllText(Path.Combine(directory, FakeClaudeScript.StartsFileName), "start\n");
        var start = FakeClaudeScript.Starts(directory);
        File.WriteAllText(
            Path.Combine(directory, FakeClaudeScript.ArgsFileName),
            JsonSerializer.Serialize(args, FakeClaudeJson.Default.StringArray));
        var env = new Dictionary<string, string>(StringComparer.Ordinal);
        foreach (DictionaryEntry e in Environment.GetEnvironmentVariables())
        {
            env[(string)e.Key] = (string?)e.Value ?? "";
        }
        File.WriteAllText(
            Path.Combine(directory, FakeClaudeScript.EnvFileName),
            JsonSerializer.Serialize(env, FakeClaudeJson.Default.DictionaryStringString));
        File.WriteAllText(Path.Combine(directory, FakeClaudeScript.CwdFileName), Environment.CurrentDirectory);

        if (Run(script.OnStart, start) is { } early)
        {
            return early;
        }
        var stdin = Path.Combine(directory, FakeClaudeScript.StdinFileName);
        while (StandardInput.ReadLine() is { } line)
        {
            File.AppendAllText(stdin, line + "\n", new UTF8Encoding(false));
            if (script.Turns.Count == 0)
            {
                continue;
            }
            var k = Math.Min(FakeClaudeScript.StdinLines(directory).Count, script.Turns.Count);
            if (Run(script.Turns[k - 1], start) is { } exit)
            {
                return exit;
            }
        }
        return 0;
    }

    // Runs the steps; the status of an exit step, or null to go on.
    private static int? Run(IReadOnlyList<FakeClaudeStep> steps, int start)
    {
        foreach (var step in steps)
        {
            switch (step.Kind)
            {
                case "stdout":
                    Write(StandardOutput, step.Text);
                    break;
                case "stderr":
                    Write(StandardError, step.Text);
                    break;
                case "exit":
                    return (int)step.Number;
                case "sleep":
                    Thread.Sleep((int)step.Number);
                    break;
                case "readLine":
                    StandardInput.ReadLine();
                    break;
                case "fill":
                    Write(StandardError, new string('x', (int)step.Number));
                    break;
                case "printFile":
                    Write(StandardOutput, File.ReadAllText(step.Text, Encoding.UTF8));
                    break;
                case "hang":
                    while (true)
                    {
                        Thread.Sleep(100);
                    }
                case "ifStart":
                    if (step.Number == start && Run(step.Then, start) is { } exit)
                    {
                        return exit;
                    }
                    break;
                default:
                    Write(StandardError, "unknown step " + step.Kind + "\n");
                    return 2;
            }
        }
        return null;
    }

    private static void Write(Stream stream, string text)
    {
        var bytes = Encoding.UTF8.GetBytes(text);
        try
        {
            stream.Write(bytes);
            stream.Flush();
        }
        catch (IOException)
        {
            // The reader is gone.
        }
    }
}
