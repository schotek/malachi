// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The commands a stand-in malachi-mcp runs for one subcommand: what the
// #!/bin/sh bodies of macos/Tests/MalachiCoreTests/MCPRegistrationTests.swift
// (prints, fails, "exit 9", "echo garbage", "sleep 30", "sleep 0.5; …",
// "[ -e flag ] && … || { touch flag; … }", "kill -9 $$", "head -c 3145728
// /dev/zero | tr '\0' x") and the modes of fakeBridgeMain in
// ui/internal/mcpsetup/mcpsetup_test.go (report, args, noclient, reason,
// silent, notjson, hang, hold) do, as data a C# program can run on Windows.

using System;
using System.Collections.Generic;
using System.Text.Json;

namespace Malachi.FakeBridge;

/// <summary>One step of a scripted subcommand.</summary>
public sealed class FakeBridgeStep
{
    private FakeBridgeStep(string op)
    {
        Op = op;
    }

    /// <summary>What the step does.</summary>
    public string Op { get; }

    /// <summary>The text of <c>stdout</c> and <c>stderr</c>, the path of <c>touch</c>, <c>ifExists</c>, <c>hold</c> and <c>child</c>.</summary>
    public string? Text { get; private init; }

    /// <summary>The status of <c>exit</c>, the milliseconds of <c>sleep</c>, <c>hold</c> and <c>child</c>, the byte count of <c>fill</c>.</summary>
    public long Number { get; private init; }

    /// <summary>The byte <c>fill</c> writes; whether it goes to stderr.</summary>
    public byte Byte { get; private init; }

    /// <summary>For <c>fill</c>: stderr instead of stdout.</summary>
    public bool ToStderr { get; private init; }

    /// <summary>For <c>ifExists</c>: the steps when the file exists.</summary>
    public IReadOnlyList<FakeBridgeStep> Then { get; private init; } = [];

    /// <summary>For <c>ifExists</c>: the steps when it does not.</summary>
    public IReadOnlyList<FakeBridgeStep> Else { get; private init; } = [];

    /// <summary>Writes <paramref name="text"/> to stdout as it is (UTF-8).</summary>
    public static FakeBridgeStep Stdout(string text) => new("stdout") { Text = text };

    /// <summary>Writes <paramref name="text"/> to stderr as it is (UTF-8).</summary>
    public static FakeBridgeStep Stderr(string text) => new("stderr") { Text = text };

    /// <summary>Exits with <paramref name="status"/> at once (an NTSTATUS as a negative number).</summary>
    public static FakeBridgeStep Exit(int status) => new("exit") { Number = status };

    /// <summary>Waits <paramref name="milliseconds"/>.</summary>
    public static FakeBridgeStep Sleep(int milliseconds) => new("sleep") { Number = milliseconds };

    /// <summary>Writes <paramref name="count"/> copies of <paramref name="value"/> to stdout (or stderr).</summary>
    public static FakeBridgeStep Fill(byte value, long count, bool toStderr = false) =>
        new("fill") { Byte = value, Number = count, ToStderr = toStderr };

    /// <summary>
    /// Prints a report whose command is the arguments it got
    /// (mcpsetup_test.go "args"): <c>{"command":"status --json","clients":[]}</c>.
    /// </summary>
    public static FakeBridgeStep EchoArguments() => new("args");

    /// <summary>Ends itself the way a kill does (TerminateProcess, status -1; "kill -9 $$").</summary>
    public static FakeBridgeStep KillSelf() => new("kill");

    /// <summary>Creates the file <paramref name="path"/> ("touch").</summary>
    public static FakeBridgeStep Touch(string path) => new("touch") { Text = path };

    /// <summary>Runs <paramref name="then"/> when <paramref name="path"/> exists, else <paramref name="otherwise"/> ("[ -e … ]").</summary>
    public static FakeBridgeStep IfExists(string path, IReadOnlyList<FakeBridgeStep> then, IReadOnlyList<FakeBridgeStep> otherwise) =>
        new("ifExists") { Text = path, Then = then, Else = otherwise };

    /// <summary>
    /// Starts a child that inherits stdout and stderr and keeps them open
    /// while <paramref name="holdFile"/> exists, at most
    /// <paramref name="milliseconds"/> (mcpsetup_test.go "hang"; the
    /// orphaned <c>sleep</c> of the Swift test).
    /// </summary>
    public static FakeBridgeStep HoldingChild(string holdFile, int milliseconds = 10_000) =>
        new("child") { Text = holdFile, Number = milliseconds };

    /// <summary>Waits while <paramref name="holdFile"/> exists, at most <paramref name="milliseconds"/> ("hold").</summary>
    public static FakeBridgeStep Hold(string holdFile, int milliseconds = 10_000) =>
        new("hold") { Text = holdFile, Number = milliseconds };

    /// <summary>
    /// Prints <paramref name="json"/> and a line break (the Swift test's
    /// <c>prints</c>: <c>printf '%s\n'</c>).
    /// </summary>
    public static IReadOnlyList<FakeBridgeStep> Prints(string json) => [Stdout(json + "\n")];

    /// <summary>
    /// Writes <paramref name="reason"/> and a line break to stderr and exits
    /// 1, as the bridge fails (the Swift test's <c>fails</c>).
    /// </summary>
    public static IReadOnlyList<FakeBridgeStep> Fails(string reason) => [Stderr(reason + "\n"), Exit(1)];

    internal void Write(Utf8JsonWriter writer)
    {
        writer.WriteStartObject();
        writer.WriteString("op", Op);
        if (Text is not null)
        {
            writer.WriteString("text", Text);
        }
        writer.WriteNumber("number", Number);
        writer.WriteNumber("byte", Byte);
        writer.WriteBoolean("stderr", ToStderr);
        WriteSteps(writer, "then", Then);
        WriteSteps(writer, "else", Else);
        writer.WriteEndObject();
    }

    internal static void WriteSteps(Utf8JsonWriter writer, string name, IReadOnlyList<FakeBridgeStep> steps)
    {
        writer.WriteStartArray(name);
        foreach (var step in steps)
        {
            step.Write(writer);
        }
        writer.WriteEndArray();
    }

    internal static List<FakeBridgeStep> ReadSteps(JsonElement array)
    {
        var steps = new List<FakeBridgeStep>();
        foreach (var element in array.EnumerateArray())
        {
            steps.Add(new FakeBridgeStep(element.GetProperty("op").GetString() ?? throw new FormatException("a step without op"))
            {
                Text = element.TryGetProperty("text", out var text) ? text.GetString() : null,
                Number = element.GetProperty("number").GetInt64(),
                Byte = element.GetProperty("byte").GetByte(),
                ToStderr = element.GetProperty("stderr").GetBoolean(),
                Then = ReadSteps(element.GetProperty("then")),
                Else = ReadSteps(element.GetProperty("else")),
            });
        }
        return steps;
    }
}
