// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the stand-in malachi-mcp of macos/Tests/MalachiCoreTests/
// MCPRegistrationTests.swift (the #!/bin/sh script FakeBridge writes) and
// of fakeBridgeMain in ui/internal/mcpsetup/mcpsetup_test.go. As the
// script: every invocation's arguments go to "calls" first, a second
// argument other than --json is refused (exit 2), an unknown subcommand
// too, and the subcommand's steps run (FakeBridgeScript beside the
// program). The child of FakeBridgeStep.HoldingChild runs this program
// again with FakeBridgeScript.HoldEnv and only holds.

using System;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Text;
using System.Threading;

namespace Malachi.FakeBridge;

internal static class Program
{
    private static readonly Stream StandardOutput = Console.OpenStandardOutput();
    private static readonly Stream StandardError = Console.OpenStandardError();

    private static int Main(string[] args)
    {
        if (Environment.GetEnvironmentVariable(FakeBridgeScript.HoldEnv) is { Length: > 0 } hold)
        {
            // The child: says who it is (the tests check that a timeout
            // ended it too), then holds.
            var parts = hold.Split('|');
            File.WriteAllText(parts[0] + FakeBridgeScript.ChildPidSuffix, Environment.ProcessId.ToString(CultureInfo.InvariantCulture));
            Hold(parts[0], int.Parse(parts[1], CultureInfo.InvariantCulture));
            return 0;
        }
        var directory = FakeBridgeScript.DirectoryOf(Environment.ProcessPath);
        AppendCall(Path.Combine(directory, FakeBridgeScript.CallsFileName), string.Join(' ', args) + "\n");
        if (args.Length < 2 || args[1] != "--json")
        {
            Write(StandardError, "expected --json, got " + (args.Length < 2 ? "" : args[1]) + "\n");
            return 2;
        }
        var script = FakeBridgeScript.Load(Path.Combine(directory, FakeBridgeScript.ScriptFileName));
        var steps = script.For(args[0]);
        if (steps is null)
        {
            Write(StandardError, "unknown command " + args[0] + "\n");
            return 2;
        }
        return Run(steps, args) ?? 0;
    }

    // The status of an exit step, or null to go on after the last step.
    private static int? Run(System.Collections.Generic.IReadOnlyList<FakeBridgeStep> steps, string[] args)
    {
        foreach (var step in steps)
        {
            switch (step.Op)
            {
                case "stdout":
                    Write(StandardOutput, step.Text ?? "");
                    break;
                case "stderr":
                    Write(StandardError, step.Text ?? "");
                    break;
                case "exit":
                    return (int)step.Number;
                case "sleep":
                    Thread.Sleep((int)step.Number);
                    break;
                case "fill":
                    Fill(step.ToStderr ? StandardError : StandardOutput, step.Byte, step.Number);
                    break;
                case "args":
                    Write(StandardOutput, "{\"command\":\"" + string.Join(' ', args) + "\",\"clients\":[]}\n");
                    break;
                case "kill":
                    using (var self = Process.GetCurrentProcess())
                    {
                        self.Kill();
                    }
                    Thread.Sleep(Timeout.Infinite);
                    break;
                case "touch":
                    File.WriteAllBytes(step.Text!, []);
                    break;
                case "ifExists":
                    var outcome = Run(File.Exists(step.Text) ? step.Then : step.Else, args);
                    if (outcome is not null)
                    {
                        return outcome;
                    }
                    break;
                case "child":
                    StartHoldingChild(step.Text!, (int)step.Number);
                    break;
                case "hold":
                    Hold(step.Text!, (int)step.Number);
                    break;
                default:
                    Write(StandardError, "unknown step " + step.Op + "\n");
                    return 5;
            }
        }
        return null;
    }

    // A child with this program's stdout and stderr (inherited, not
    // redirected), which it keeps open while it holds; returns once the
    // child runs (it wrote its process ID).
    private static void StartHoldingChild(string holdFile, int milliseconds)
    {
        var start = new ProcessStartInfo(Environment.ProcessPath!)
        {
            UseShellExecute = false,
        };
        start.Environment[FakeBridgeScript.HoldEnv] = holdFile + "|" + milliseconds.ToString(CultureInfo.InvariantCulture);
        using var child = Process.Start(start);
        var pid = holdFile + FakeBridgeScript.ChildPidSuffix;
        var deadline = Environment.TickCount64 + 10_000;
        while (!File.Exists(pid) && Environment.TickCount64 < deadline)
        {
            Thread.Sleep(5);
        }
    }

    // Returns once the file is gone, or after the time.
    private static void Hold(string holdFile, int milliseconds)
    {
        var deadline = Environment.TickCount64 + milliseconds;
        while (Environment.TickCount64 < deadline && File.Exists(holdFile))
        {
            Thread.Sleep(10);
        }
    }

    // Appends one line to the calls file. Two invocations may run at once
    // (an install overtaken by an uninstall), and Windows refuses a second
    // writer while the first has the file open, where the script's `>>`
    // appends in turn: so each waits for the other's line.
    private static void AppendCall(string path, string line)
    {
        var bytes = new UTF8Encoding(false).GetBytes(line);
        for (var attempt = 0; ; attempt++)
        {
            try
            {
                using var file = new FileStream(path, FileMode.Append, FileAccess.Write, FileShare.Read);
                file.Write(bytes);
                return;
            }
            catch (IOException) when (attempt < 400)
            {
                Thread.Sleep(5);
            }
        }
    }

    private static void Fill(Stream stream, byte value, long count)
    {
        var chunk = Enumerable.Repeat(value, 64 << 10).ToArray();
        for (var left = count; left > 0; left -= chunk.Length)
        {
            stream.Write(chunk, 0, (int)Math.Min(left, chunk.Length));
        }
        stream.Flush();
    }

    private static void Write(Stream stream, string text)
    {
        stream.Write(Encoding.UTF8.GetBytes(text));
        stream.Flush();
    }
}
